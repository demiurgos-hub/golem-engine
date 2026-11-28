package inspector

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/demiurgos-hub/golem-engine/golem"
	"github.com/demiurgos-hub/golem-engine/golem/inspector/wire"
)

// WebSocket close codes shared with the hub.
const (
	CloseAuthFailed       = 4001
	CloseReplaced         = 4002
	CloseProtocol         = 4003
	CloseRegisterTimeout  = 4004
	CloseStale            = 4005
	CloseSlowConsumer     = 4008
	CloseStaleClient      = 4009
	CloseServiceRestart   = 1012
	CloseConnectionsLimit = 1013
)

// Agent streams a golem.Server's full entity state to a Golem Inspector hub.
// It connects out to the hub, registers the server's schema, and stays idle
// until the hub asks it to observe; then it sends a snapshot followed by
// coalesced changes at most RateHz times per second. All entity reads happen
// on the server's tick goroutine; a slow hub never blocks the tick loop.
type Agent struct {
	srv    *golem.Server
	schema *wire.Schema
	opts   Options

	out  chan [][]byte // tick goroutine -> writer: one snapshot or batch per item
	ctrl chan []byte   // network goroutines -> writer: status frames

	observing atomic.Bool   // hub asked to stream and the connection is up
	restart   atomic.Bool   // next frame must start a fresh snapshot
	epoch     atomic.Uint64 // last epoch begun on the tick goroutine
	connected atomic.Bool

	stream stream // owned by the tick goroutine

	unobserve func()
	cancel    context.CancelFunc
	done      chan struct{}
	closeOnce sync.Once
	termErr   atomic.Pointer[error]
}

// Attach starts an agent for srv. It returns (nil, nil) when opts.HubURL is
// empty, so callers can attach unconditionally with OptionsFromEnv. schema is
// usually the generated InspectorSchema(). Attach may be called before or after
// srv.Run; the agent stops when ctx is cancelled or Close is called.
func Attach(ctx context.Context, srv *golem.Server, schema *wire.Schema, opts Options) (*Agent, error) {
	if strings.TrimSpace(opts.HubURL) == "" {
		return nil, nil
	}
	if srv == nil {
		return nil, errors.New("inspector: nil server")
	}
	if err := schema.Validate(); err != nil {
		return nil, fmt.Errorf("inspector: %w", err)
	}
	if err := validateHubURL(opts.HubURL); err != nil {
		return nil, err
	}
	if strings.TrimSpace(opts.Token) == "" {
		return nil, fmt.Errorf("inspector: Options.Token is required when HubURL is set")
	}
	opts = opts.withDefaults()
	runCtx, cancel := context.WithCancel(ctx)
	a := &Agent{
		srv:    srv,
		schema: schema,
		opts:   opts,
		out:    make(chan [][]byte, opts.QueueFrames),
		ctrl:   make(chan []byte, 8),
		cancel: cancel,
		done:   make(chan struct{}),
	}
	a.stream.agent = a
	a.unobserve = srv.ObserveReplication(a.stream.onFrame)
	go a.run(runCtx)
	return a, nil
}

// Close stops the agent and waits for its connection goroutine to exit. It is
// safe to call more than once and on a nil Agent.
func (a *Agent) Close() error {
	if a == nil {
		return nil
	}
	a.closeOnce.Do(func() {
		a.cancel()
		a.unobserve()
		<-a.done
	})
	return nil
}

// Done is closed when the agent stops, either through Close, context
// cancellation or a terminal hub rejection (see Err).
func (a *Agent) Done() <-chan struct{} { return a.done }

// Err returns the terminal error that stopped reconnecting (bad token or
// incompatible protocol), or nil.
func (a *Agent) Err() error {
	if p := a.termErr.Load(); p != nil {
		return *p
	}
	return nil
}

// Connected reports whether the agent currently holds a hub connection.
func (a *Agent) Connected() bool { return a.connected.Load() }

var errTerminal = errors.New("inspector: hub rejected the agent")

func (a *Agent) run(ctx context.Context) {
	defer close(a.done)
	defer a.unobserve()
	attempt := 0
	for ctx.Err() == nil {
		established, err := a.connectOnce(ctx)
		if ctx.Err() != nil {
			return
		}
		if errors.Is(err, errTerminal) {
			a.termErr.Store(&err)
			a.opts.Logger.Printf("golem/inspector: %v; not reconnecting", err)
			return
		}
		if established {
			attempt = 0 // restart the backoff after a registered session
		}
		attempt++
		delay := backoff(attempt, a.opts.MinBackoff, a.opts.MaxBackoff)
		if err != nil {
			a.opts.Logger.Printf("golem/inspector: hub connection to %s: %v (retry in %s)", redactURL(a.opts.HubURL), err, delay.Round(time.Millisecond))
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
	}
}

// backoff returns a capped exponential delay with full jitter in its upper half.
func backoff(attempt int, minDelay, maxDelay time.Duration) time.Duration {
	d := minDelay << min(attempt-1, 10)
	if d > maxDelay || d <= 0 {
		d = maxDelay
	}
	return d/2 + time.Duration(rand.Int64N(int64(d/2)+1))
}

// connectOnce dials and serves one session. established reports whether the
// agent registered before the session ended.
func (a *Agent) connectOnce(ctx context.Context) (established bool, err error) {
	dialCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	header := http.Header{}
	header.Set("Authorization", "Bearer "+a.opts.Token)
	conn, resp, err := websocket.Dial(dialCtx, a.opts.HubURL, &websocket.DialOptions{HTTPClient: a.opts.HTTPClient, HTTPHeader: header})
	if err != nil {
		if resp != nil && (resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden) {
			return false, fmt.Errorf("%w: HTTP %d (check the token)", errTerminal, resp.StatusCode)
		}
		return false, sanitizeError(err, a.opts.HubURL)
	}
	conn.SetReadLimit(1 << 20)
	return a.serve(ctx, conn)
}

func (a *Agent) serve(ctx context.Context, conn *websocket.Conn) (bool, error) {
	defer conn.CloseNow()
	a.drainQueues()
	sessionCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	reg := &wire.Register{
		TargetID:      a.opts.TargetID,
		Labels:        a.opts.Labels,
		Epoch:         a.epoch.Load(),
		EngineVersion: a.opts.EngineVersion,
		Schema:        a.schema,
	}
	if err := a.write(sessionCtx, conn, wire.Encode(reg)); err != nil {
		return false, err
	}
	a.connected.Store(true)
	defer func() {
		a.connected.Store(false)
		a.observing.Store(false)
	}()
	a.opts.Logger.Printf("golem/inspector: registered %q with %s", a.opts.TargetID, redactURL(a.opts.HubURL))

	readErr := make(chan error, 1)
	go func() { readErr <- a.readLoop(sessionCtx, conn) }()

	ping := time.NewTicker(a.opts.PingInterval)
	defer ping.Stop()
	for {
		var frames [][]byte
		select {
		case <-ctx.Done():
			conn.Close(websocket.StatusNormalClosure, "agent stopping")
			return true, nil
		case err := <-readErr:
			return true, classifyClose(err)
		case frame := <-a.ctrl:
			frames = [][]byte{frame}
		case frames = <-a.out:
		case <-ping.C:
			frames = [][]byte{wire.Encode(&wire.Ping{ClientTime: time.Now().UnixMilli()})}
		}
		for _, frame := range frames {
			if err := a.write(sessionCtx, conn, frame); err != nil {
				select {
				case rerr := <-readErr:
					return true, classifyClose(rerr)
				default:
					return true, err
				}
			}
		}
	}
}

func (a *Agent) write(ctx context.Context, conn *websocket.Conn, frame []byte) error {
	wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return conn.Write(wctx, websocket.MessageBinary, frame)
}

func (a *Agent) readLoop(ctx context.Context, conn *websocket.Conn) error {
	for {
		typ, data, err := conn.Read(ctx)
		if err != nil {
			return err
		}
		if typ != websocket.MessageBinary {
			continue
		}
		msg, err := wire.Decode(data)
		if err != nil {
			a.opts.Logger.Printf("golem/inspector: ignoring hub frame: %v", err)
			continue
		}
		switch m := msg.(type) {
		case *wire.ObserveStart:
			a.restart.Store(true)
			a.observing.Store(true)
			a.sendCtrl(&wire.ObserveStatus{State: wire.ObserveStarting, Epoch: a.epoch.Load()})
		case *wire.ObserveStop:
			a.observing.Store(false)
			a.sendCtrl(&wire.ObserveStatus{State: wire.ObserveIdle, Epoch: a.epoch.Load()})
		case *wire.Error:
			a.opts.Logger.Printf("golem/inspector: hub error %s: %s", m.Code, m.Message)
		}
	}
}

func (a *Agent) sendCtrl(msg wire.Message) {
	select {
	case a.ctrl <- wire.Encode(msg):
	default: // status is advisory; the hub reconciles on the next one
	}
}

// drainQueues discards frames produced for a previous connection.
func (a *Agent) drainQueues() {
	for {
		select {
		case <-a.out:
		case <-a.ctrl:
		default:
			return
		}
	}
}

func classifyClose(err error) error {
	switch websocket.CloseStatus(err) {
	case CloseAuthFailed:
		return fmt.Errorf("%w: authentication failed", errTerminal)
	case CloseProtocol:
		return fmt.Errorf("%w: incompatible protocol or registration", errTerminal)
	case CloseReplaced:
		return errors.New("replaced by another agent with the same target id")
	case -1:
		return err
	default:
		return fmt.Errorf("hub closed the connection: %w", err)
	}
}

func redactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "<invalid url>"
	}
	u.User = nil
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

// sanitizeError keeps dial errors from echoing a URL that might carry secrets.
func sanitizeError(err error, raw string) error {
	msg := err.Error()
	if raw != "" {
		msg = strings.ReplaceAll(msg, raw, redactURL(raw))
	}
	return errors.New(msg)
}
