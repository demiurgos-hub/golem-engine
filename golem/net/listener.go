package net

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/quic-go/quic-go/http3"
	"github.com/quic-go/webtransport-go"
	"github.com/demiurgos-hub/golem-engine/golem/registry"
)

// Listener manages client sessions and bridges the game-loop deltas to the
// active integrated transport.
type Listener struct {
	registry *registry.Registry
	config   Config

	mu       sync.RWMutex
	sessions map[int64]*Session
	nextID   atomic.Int64

	interestEnabled         bool // when true, skip entity snapshot on connect (interest system handles it)
	onUpgrade               func(*http.Request) (any, error)
	onConnect               func(*Session)
	onMessage               func(*Session, []byte)
	onDatagram              func(*Session, []byte)
	onReliableUnordered     func(*Session, []byte)
	onReliableOrdered       func(*Session, []byte)
	onEventualStateFeedback func(*Session, []EventualStateDelivery)
	onDisconnect            func(*Session)
	messageWrapper          func([]byte) []byte      // wraps outgoing entity frames (ServerMessage tag 1)
	worldSnapshotFunc       func() ([][]byte, error) // returns fully-wrapped ServerMessage bytes for world data

	wtServer          *webtransport.Server
	certificateHashes []CertificateHash
	wtCheckOrigin     func(*http.Request) bool
	readyOnce         sync.Once
	readyCh           chan struct{}
	readyErr          error

	// Deltas for OutboundBacklogForLog: previous cumulative wire counters per session.
	outboundLogMu      sync.Mutex
	outboundDgramPrev  map[int64]uint64
	outboundStreamPrev map[int64]uint64
	inboundPacketPrev  map[int64]uint64
	inboundAckPrev     map[int64]uint64
}

const webTransportAcceptStreamTimeout = 10 * time.Second

// NewListener creates a Listener bound to the given registry and config.
func NewListener(reg *registry.Registry, cfg Config) *Listener {
	cfg.Transport = normalizeTransport(cfg.Transport)
	if cfg.Path == "" {
		if cfg.Transport == TransportWebTransport {
			cfg.Path = "/wt"
		} else {
			cfg.Path = "/ws"
		}
	}
	return &Listener{
		registry:           reg,
		config:             cfg,
		sessions:           make(map[int64]*Session),
		wtCheckOrigin:      newWebTransportCheckOrigin(cfg),
		readyCh:            make(chan struct{}),
		outboundDgramPrev:  make(map[int64]uint64),
		outboundStreamPrev: make(map[int64]uint64),
		inboundPacketPrev:  make(map[int64]uint64),
		inboundAckPrev:     make(map[int64]uint64),
	}
}

// WaitReady blocks until the listener has prepared its transport endpoint.
// For WebTransport, readiness means the TLS certificate has been resolved and
// any certificate hashes have been computed.
func (l *Listener) WaitReady(ctx context.Context) error {
	select {
	case <-l.readyCh:
		return l.readyErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (l *Listener) signalReady(err error) {
	l.readyOnce.Do(func() {
		l.readyErr = err
		close(l.readyCh)
	})
}

// Handler returns the active transport endpoint handler for external mounting
// on a custom router. When using WebTransport, pair it with WebTransportServer
// on a caller-owned HTTP/3 server.
func (l *Listener) Handler() http.HandlerFunc {
	if l.config.Transport == TransportWebTransport {
		l.WebTransportServer(nil)
		return l.handleWebTransport
	}
	return l.handleWS
}

// OnUpgrade registers a hook called before transport session acceptance. The
// hook receives the HTTP request for auth inspection (headers, query params,
// cookies). Returning a non-nil error rejects the request with HTTP 401;
// otherwise the returned value is stored in Session.Data before OnConnect.
func (l *Listener) OnUpgrade(fn func(*http.Request) (any, error)) { l.onUpgrade = fn }

// SetWebTransportCheckOrigin overrides the WebTransport origin policy. Passing
// nil restores the policy built from Config.
func (l *Listener) SetWebTransportCheckOrigin(fn func(*http.Request) bool) {
	l.wtCheckOrigin = fn
	if l.wtCheckOrigin == nil {
		l.wtCheckOrigin = newWebTransportCheckOrigin(l.config)
	}
	if l.wtServer != nil {
		l.wtServer.CheckOrigin = l.wtCheckOrigin
	}
}

// OnConnect registers a hook called after a new client receives
// the world-state snapshot and is fully connected.
func (l *Listener) OnConnect(fn func(*Session)) { l.onConnect = fn }

// OnMessage registers a hook called when a client sends a binary message.
func (l *Listener) OnMessage(fn func(*Session, []byte)) { l.onMessage = fn }

// OnDatagram registers a hook called when a client sends a raw unreliable datagram.
func (l *Listener) OnDatagram(fn func(*Session, []byte)) { l.onDatagram = fn }

// OnReliableUnordered registers a hook called when a client sends a reliable unordered datagram.
func (l *Listener) OnReliableUnordered(fn func(*Session, []byte)) { l.onReliableUnordered = fn }

// OnReliableOrdered registers a hook called when a client sends a reliable ordered datagram.
func (l *Listener) OnReliableOrdered(fn func(*Session, []byte)) { l.onReliableOrdered = fn }

// OnEventualStateFeedback registers a hook called when eventual state datagrams are ACKed or lost.
func (l *Listener) OnEventualStateFeedback(fn func(*Session, []EventualStateDelivery)) {
	l.onEventualStateFeedback = fn
}

// OnDisconnect registers a hook called when a client disconnects.
func (l *Listener) OnDisconnect(fn func(*Session)) { l.onDisconnect = fn }

// SetMessageWrapper registers a function that wraps every outgoing entity
// frame (broadcasts and snapshots). World frames bypass this wrapper entirely.
func (l *Listener) SetMessageWrapper(fn func([]byte) []byte) { l.messageWrapper = fn }

// SetWorldSnapshotFunc registers a closure that returns fully-wrapped
// ServerMessage bytes for all world data. Called during sendSnapshot to
// deliver world state before entity snapshots on connect.
func (l *Listener) SetWorldSnapshotFunc(fn func() ([][]byte, error)) { l.worldSnapshotFunc = fn }

// CertificateHashes returns the WebTransport certificate hashes known by the listener.
func (l *Listener) CertificateHashes() []CertificateHash {
	if len(l.certificateHashes) == 0 {
		return nil
	}
	out := make([]CertificateHash, len(l.certificateHashes))
	copy(out, l.certificateHashes)
	return out
}

// BroadcastRaw sends a single pre-wrapped frame to every connected session.
// Does NOT apply messageWrapper — callers are responsible for framing.
func (l *Listener) BroadcastRaw(data []byte) error {
	if err := validateReliableMessageSize(data); err != nil {
		return err
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	for _, s := range l.sessions {
		s.Send(data)
	}
	return nil
}

// Broadcast sends a set of serialized entity messages to every connected
// session in a single queued batch.
func (l *Listener) Broadcast(deltas [][]byte) error {
	if len(deltas) == 0 {
		return nil
	}
	wrapped := make([][]byte, len(deltas))
	for i, d := range deltas {
		wrapped[i] = l.wrap(d)
	}
	if err := validateReliableMessageSizes(wrapped); err != nil {
		return err
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	for _, s := range l.sessions {
		s.SendBatch(wrapped)
	}
	return nil
}

// Send delivers a single binary message to a specific session without
// altering bytes. When a messageWrapper is set, Broadcast and snapshots wrap
// entity frames automatically; Send does not—callers must supply fully
// framed wire bytes if clients expect a ServerMessage envelope.
func (l *Listener) Send(sessionID int64, data []byte) error {
	if err := validateReliableMessageSize(data); err != nil {
		return err
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	s, ok := l.sessions[sessionID]
	if !ok {
		return fmt.Errorf("%w: %d", ErrSessionNotFound, sessionID)
	}
	s.Send(data)
	return nil
}

// SetInterestEnabled toggles interest-managed mode. When enabled, the entity
// snapshot on connect is skipped (the interest system sends initial visibility
// on the first tick after FOI assignment). World data is still sent.
func (l *Listener) SetInterestEnabled(v bool) { l.interestEnabled = v }

// SessionIDs returns a snapshot of all currently connected session IDs.
func (l *Listener) SessionIDs() []int64 {
	l.mu.RLock()
	defer l.mu.RUnlock()
	ids := make([]int64, 0, len(l.sessions))
	for id := range l.sessions {
		ids = append(ids, id)
	}
	return ids
}

// OutboundBacklogForLog returns a single-line description of every session's
// outbound stream channels, reliable datagram protocol queues, and (since the
// last call) how many WebTransport datagrams and stream batches were
// successfully written. Call about once per second for meaningful d_1s / st_1s.
func (l *Listener) OutboundBacklogForLog(now time.Time) string {
	l.mu.RLock()
	n := len(l.sessions)
	if n == 0 {
		l.mu.RUnlock()
		return "sessions=0"
	}
	ids := make([]int64, 0, n)
	for id := range l.sessions {
		ids = append(ids, id)
	}
	sessionsCopy := make(map[int64]*Session, n)
	for id, sess := range l.sessions {
		sessionsCopy[id] = sess
	}
	l.mu.RUnlock()

	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	var b strings.Builder
	b.Grow(96 * n)
	fmt.Fprintf(&b, "sessions=%d", n)
	l.outboundLogMu.Lock()
	for _, id := range ids {
		sess := sessionsCopy[id]
		sn := sess.OutboundSnapshot(now)
		dTot := sess.wireDatagramOK.Load()
		stTot := sess.streamBatchOK.Load()
		pInTot := sn.InboundPackets
		ackTot := sn.InboundAckOnly
		d1s, st1s, pin1s, ack1s := uint64(0), uint64(0), uint64(0), uint64(0)
		if prev, ok := l.outboundDgramPrev[id]; ok {
			d1s = uint64Delta(dTot, prev)
		}
		if prev, ok := l.outboundStreamPrev[id]; ok {
			st1s = uint64Delta(stTot, prev)
		}
		if prev, ok := l.inboundPacketPrev[id]; ok {
			pin1s = uint64Delta(pInTot, prev)
		}
		if prev, ok := l.inboundAckPrev[id]; ok {
			ack1s = uint64Delta(ackTot, prev)
		}
		l.outboundDgramPrev[id] = dTot
		l.outboundStreamPrev[id] = stTot
		l.inboundPacketPrev[id] = pInTot
		l.inboundAckPrev[id] = ackTot

		roAge := "-"
		if sn.PendingOrdered > 0 {
			roAge = sn.HeadAgeOrdered.Round(time.Millisecond).String()
		}
		ruAge := "-"
		if sn.PendingUnordered > 0 {
			ruAge = sn.HeadAgeUnordered.Round(time.Millisecond).String()
		}
		fmt.Fprintf(&b, " | s%d[str=%d unrel=%d es=%d es_if=%d ro=%d ro_if=%d ro_age=%s ru=%d ru_if=%d ru_age=%s d_1s=%d st_1s=%d pin_1s=%d ack_1s=%d]",
			id, sn.StreamBatchesQueued, sn.UnreliableQueued,
			sn.EventualQueued, sn.EventualInFlight,
			sn.PendingOrdered, sn.InFlightOrdered, roAge,
			sn.PendingUnordered, sn.InFlightUnordered, ruAge,
			d1s, st1s, pin1s, ack1s,
		)
	}
	l.outboundLogMu.Unlock()
	return b.String()
}

// SendWrapped delivers a single entity frame to a specific session, applying
// the messageWrapper (e.g. WrapEntityUpdate) if one is configured.
func (l *Listener) SendWrapped(sessionID int64, data []byte) error {
	wrapped := l.wrap(data)
	if err := validateReliableMessageSize(wrapped); err != nil {
		return err
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	s, ok := l.sessions[sessionID]
	if !ok {
		return fmt.Errorf("%w: %d", ErrSessionNotFound, sessionID)
	}
	s.Send(wrapped)
	return nil
}

// Wrap applies the messageWrapper if one is set, otherwise returns data as-is.
// Exported so the game loop can pre-wrap entity frames before batching.
func (l *Listener) Wrap(data []byte) []byte { return l.wrap(data) }

// SendBatch queues a set of pre-wrapped frames to a specific session. The
// caller is responsible for wrapping each entity frame before enqueueing.
func (l *Listener) SendBatch(sessionID int64, frames [][]byte) error {
	if len(frames) == 0 {
		return nil
	}
	if err := validateReliableMessageSizes(frames); err != nil {
		return err
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	s, ok := l.sessions[sessionID]
	if !ok {
		return fmt.Errorf("%w: %d", ErrSessionNotFound, sessionID)
	}
	s.SendBatch(frames)
	return nil
}

// SendReliableOrderedBatch queues a set of already-framed state records to a
// specific session over one or more reliable ordered datagrams.
func (l *Listener) SendReliableOrderedBatch(sessionID int64, frames [][]byte) error {
	if l.config.Transport != TransportWebTransport {
		return ErrReliableDatagramsNotSupported
	}
	if len(frames) == 0 {
		return nil
	}
	payloads, err := chunkReliableOrderedDatagramFrames(frames)
	if err != nil {
		return err
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	s, ok := l.sessions[sessionID]
	if !ok {
		return fmt.Errorf("%w: %d", ErrSessionNotFound, sessionID)
	}
	for _, payload := range payloads {
		if err := s.SendReliableOrdered(payload); err != nil {
			return err
		}
	}
	return nil
}

// SendUnreliableStateBatch queues already-framed state records to a specific
// session over one or more raw WebTransport datagrams.
func (l *Listener) SendUnreliableStateBatch(sessionID int64, frames [][]byte) error {
	if l.config.Transport != TransportWebTransport {
		return ErrUnreliableNotSupported
	}
	if len(frames) == 0 {
		return nil
	}
	payloads, err := UnreliableStateDatagramPayloads(frames)
	if err != nil {
		return err
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	s, ok := l.sessions[sessionID]
	if !ok {
		return fmt.Errorf("%w: %d", ErrSessionNotFound, sessionID)
	}
	for _, payload := range payloads {
		if err := s.sendUnreliableStateOwned(payload); err != nil {
			return err
		}
	}
	return nil
}

// SendUnreliableStateOwned queues one already-packed raw state datagram payload.
// The caller must own data and must not mutate it after this call.
func (l *Listener) SendUnreliableStateOwned(sessionID int64, data []byte) error {
	if l.config.Transport != TransportWebTransport {
		return ErrUnreliableNotSupported
	}
	if err := validateDatagramSize(data); err != nil {
		return err
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	s, ok := l.sessions[sessionID]
	if !ok {
		return fmt.Errorf("%w: %d", ErrSessionNotFound, sessionID)
	}
	return s.sendUnreliableStateOwned(data)
}

// BroadcastBatch wraps all entity frames once, validates them against the
// reliable-message cap, and queues one batch per session. The active transport
// emits the batch using its own reliable delivery rules.
func (l *Listener) BroadcastBatch(deltas [][]byte) error {
	if len(deltas) == 0 {
		return nil
	}
	wrapped := make([][]byte, len(deltas))
	for i, d := range deltas {
		wrapped[i] = l.wrap(d)
	}
	if err := validateReliableMessageSizes(wrapped); err != nil {
		return err
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	for _, s := range l.sessions {
		s.SendBatch(wrapped)
	}
	return nil
}

// BroadcastReliableOrderedBatch wraps all entity frames once, packs them into
// one or more reliable ordered datagram payloads, and queues every payload for
// every connected session. Compact state records should use SendReliableOrderedBatch.
func (l *Listener) BroadcastReliableOrderedBatch(deltas [][]byte) error {
	if l.config.Transport != TransportWebTransport {
		return ErrReliableDatagramsNotSupported
	}
	if len(deltas) == 0 {
		return nil
	}
	wrapped := make([][]byte, len(deltas))
	for i, d := range deltas {
		wrapped[i] = l.wrap(d)
	}
	payloads, err := chunkReliableOrderedDatagramFrames(wrapped)
	if err != nil {
		return err
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	for _, s := range l.sessions {
		for _, payload := range payloads {
			if err := s.SendReliableOrdered(payload); err != nil {
				return err
			}
		}
	}
	return nil
}

// BroadcastUnreliableStateBatch wraps entity frames once, packs them into raw
// WebTransport datagrams, and queues every payload for every connected session.
func (l *Listener) BroadcastUnreliableStateBatch(deltas [][]byte) error {
	if l.config.Transport != TransportWebTransport {
		return ErrUnreliableNotSupported
	}
	if len(deltas) == 0 {
		return nil
	}
	wrapped := make([][]byte, len(deltas))
	for i, d := range deltas {
		wrapped[i] = l.wrap(d)
	}
	return l.BroadcastUnreliableStateWrappedBatch(wrapped)
}

// BroadcastUnreliableStateWrappedBatch packs already-framed state records into
// raw WebTransport datagrams and queues every payload for every connected session.
func (l *Listener) BroadcastUnreliableStateWrappedBatch(frames [][]byte) error {
	if l.config.Transport != TransportWebTransport {
		return ErrUnreliableNotSupported
	}
	if len(frames) == 0 {
		return nil
	}
	payloads, err := UnreliableStateDatagramPayloads(frames)
	if err != nil {
		return err
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	for _, s := range l.sessions {
		for _, payload := range payloads {
			if err := s.sendUnreliableStateOwned(payload); err != nil {
				return err
			}
		}
	}
	return nil
}

// SendUnreliable sends a single datagram to a specific session when supported.
func (l *Listener) SendUnreliable(sessionID int64, data []byte) error {
	if l.config.Transport != TransportWebTransport {
		return ErrUnreliableNotSupported
	}
	if err := validateDatagramSize(data); err != nil {
		return err
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	s, ok := l.sessions[sessionID]
	if !ok {
		return fmt.Errorf("%w: %d", ErrSessionNotFound, sessionID)
	}
	return s.SendUnreliable(data)
}

// BroadcastUnreliable sends one datagram to every connected session when supported.
func (l *Listener) BroadcastUnreliable(data []byte) error {
	if l.config.Transport != TransportWebTransport {
		return ErrUnreliableNotSupported
	}
	if err := validateDatagramSize(data); err != nil {
		return err
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	for _, s := range l.sessions {
		if err := s.SendUnreliable(data); err != nil {
			return err
		}
	}
	return nil
}

// SendReliableUnordered sends one reliable unordered datagram to a specific session when supported.
func (l *Listener) SendReliableUnordered(sessionID int64, data []byte) error {
	if l.config.Transport != TransportWebTransport {
		return ErrReliableDatagramsNotSupported
	}
	if err := validateReliableDatagramMessageSize(datagramLaneReliableUnordered, data); err != nil {
		return err
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	s, ok := l.sessions[sessionID]
	if !ok {
		return fmt.Errorf("%w: %d", ErrSessionNotFound, sessionID)
	}
	return s.SendReliableUnordered(data)
}

// BroadcastReliableUnordered sends one reliable unordered datagram to every connected session when supported.
func (l *Listener) BroadcastReliableUnordered(data []byte) error {
	if l.config.Transport != TransportWebTransport {
		return ErrReliableDatagramsNotSupported
	}
	if err := validateReliableDatagramMessageSize(datagramLaneReliableUnordered, data); err != nil {
		return err
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	for _, s := range l.sessions {
		if err := s.SendReliableUnordered(data); err != nil {
			return err
		}
	}
	return nil
}

// SendReliableOrdered sends one reliable ordered datagram to a specific session when supported.
func (l *Listener) SendReliableOrdered(sessionID int64, data []byte) error {
	if l.config.Transport != TransportWebTransport {
		return ErrReliableDatagramsNotSupported
	}
	if err := validateReliableDatagramMessageSize(datagramLaneReliableOrdered, data); err != nil {
		return err
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	s, ok := l.sessions[sessionID]
	if !ok {
		return fmt.Errorf("%w: %d", ErrSessionNotFound, sessionID)
	}
	return s.SendReliableOrdered(data)
}

// BroadcastReliableOrdered sends one reliable ordered datagram to every connected session when supported.
func (l *Listener) BroadcastReliableOrdered(data []byte) error {
	if l.config.Transport != TransportWebTransport {
		return ErrReliableDatagramsNotSupported
	}
	if err := validateReliableDatagramMessageSize(datagramLaneReliableOrdered, data); err != nil {
		return err
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	for _, s := range l.sessions {
		if err := s.SendReliableOrdered(data); err != nil {
			return err
		}
	}
	return nil
}

// SendEventualState sends one state-aware datagram payload with a feedback token.
func (l *Listener) SendEventualState(sessionID int64, token uint64, data []byte) error {
	if l.config.Transport != TransportWebTransport {
		return ErrReliableDatagramsNotSupported
	}
	if err := validateEventualStateDatagramPayloadSize(data); err != nil {
		return err
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	s, ok := l.sessions[sessionID]
	if !ok {
		return fmt.Errorf("%w: %d", ErrSessionNotFound, sessionID)
	}
	return s.SendEventualState(token, data)
}

// SendEventualStateOwned sends one state-aware datagram payload without copying
// data. The caller must own data and must not mutate it after this call.
func (l *Listener) SendEventualStateOwned(sessionID int64, token uint64, data []byte) error {
	if l.config.Transport != TransportWebTransport {
		return ErrReliableDatagramsNotSupported
	}
	if err := validateEventualStateDatagramPayloadSize(data); err != nil {
		return err
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	s, ok := l.sessions[sessionID]
	if !ok {
		return fmt.Errorf("%w: %d", ErrSessionNotFound, sessionID)
	}
	return s.sendEventualStateOwned(token, data)
}

// ListenAndServe starts the configured integrated listener.
// When StaticDir is configured, non-transport requests are served from that
// directory. When MapDir is configured, map files are served at /maps/.
// Blocks until ctx is cancelled, then shuts down gracefully.
func (l *Listener) ListenAndServe(ctx context.Context) error {
	mux := l.newServeMux()
	if l.config.Transport == TransportWebTransport {
		return l.listenAndServeWebTransport(ctx, mux)
	}
	return l.listenAndServeWebSocket(ctx, mux)
}

func (l *Listener) listenAndServeWebSocket(ctx context.Context, mux *http.ServeMux) error {
	srv := &http.Server{
		Addr:    l.config.Addr,
		Handler: mux,
	}

	go func() {
		<-ctx.Done()
		srv.Shutdown(context.Background())
	}()

	log.Printf("golem/net: listening transport=%q addr=%q path=%q", l.config.Transport, l.config.Addr, l.config.Path)
	ln, err := net.Listen("tcp", l.config.Addr)
	if err != nil {
		l.signalReady(err)
		return err
	}
	l.signalReady(nil)
	err = srv.Serve(ln)
	if err == http.ErrServerClosed || errors.Is(err, net.ErrClosed) {
		return nil
	}
	if err != nil {
		l.signalReady(err)
		log.Printf("golem/net: listener stopped transport=%q addr=%q path=%q error=%v", l.config.Transport, l.config.Addr, l.config.Path, err)
	}
	return err
}

func (l *Listener) listenAndServeWebTransport(ctx context.Context, mux *http.ServeMux) error {
	h3 := &http3.Server{
		Addr:    l.config.Addr,
		Handler: mux,
	}
	server := l.WebTransportServer(h3)
	certificate, hashes, cleanup, err := prepareWebTransportTLS(l.config)
	if err != nil {
		l.signalReady(err)
		return err
	}
	defer cleanup()
	l.certificateHashes = hashes
	h3.TLSConfig = newWebTransportServerTLSConfig(certificate)
	if l.config.DevSelfSignedCert && len(hashes) > 0 {
		log.Printf("golem/net: webtransport dev self-signed certificate ready addr=%q hashes=%s", l.config.Addr, formatCertificateHashes(hashes))
	}
	packetConn, err := net.ListenPacket("udp", l.config.Addr)
	if err != nil {
		l.signalReady(err)
		return err
	}
	defer packetConn.Close()
	go func() {
		<-ctx.Done()
		_ = server.Close()
	}()
	log.Printf("golem/net: listening transport=%q addr=%q path=%q", l.config.Transport, l.config.Addr, l.config.Path)
	l.signalReady(nil)
	err = server.Serve(packetConn)
	if err == http.ErrServerClosed || errors.Is(err, context.Canceled) || errors.Is(err, net.ErrClosed) {
		return nil
	}
	if err != nil {
		l.signalReady(err)
		log.Printf("golem/net: listener stopped transport=%q addr=%q path=%q error=%v", l.config.Transport, l.config.Addr, l.config.Path, err)
	}
	return err
}

func formatCertificateHashes(hashes []CertificateHash) string {
	parts := make([]string, 0, len(hashes))
	for _, hash := range hashes {
		parts = append(parts, fmt.Sprintf("%s:%s", hash.Algorithm, hex.EncodeToString(hash.Value)))
	}
	return strings.Join(parts, ",")
}

func (l *Listener) newServeMux() *http.ServeMux {
	mux := http.NewServeMux()
	if l.config.Transport == TransportWebTransport {
		mux.HandleFunc(l.config.Path, l.handleWebTransport)
	} else {
		mux.HandleFunc(l.config.Path, l.handleWS)
	}
	if l.config.StaticDir != "" {
		mux.Handle("/", http.FileServer(http.Dir(l.config.StaticDir)))
	}
	if l.config.MapDir != "" {
		mux.Handle("/maps/", http.StripPrefix("/maps/", http.FileServer(http.Dir(l.config.MapDir))))
	}
	return mux
}

func (l *Listener) handleWS(w http.ResponseWriter, r *http.Request) {
	sessionData, ok := l.authorizeSessionRequest(w, r, TransportWebSocket)
	if !ok {
		return
	}

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
	})
	if err != nil {
		logSessionRequest("websocket accept failed", r, TransportWebSocket, "error=%v", err)
		return
	}

	ctx := r.Context()
	id := l.nextID.Add(1)
	sess := newWebSocketSession(id, conn)
	sess.Data = sessionData
	sess.RemoteAddr = r.RemoteAddr
	sess.Transport = TransportWebSocket
	l.serveAcceptedSession(ctx, sess, r)
}

func (l *Listener) handleWebTransport(w http.ResponseWriter, r *http.Request) {
	sessionData, ok := l.authorizeSessionRequest(w, r, TransportWebTransport)
	if !ok {
		return
	}

	server := l.WebTransportServer(nil)
	wtSession, err := server.Upgrade(w, r)
	if err != nil {
		logSessionRequest("webtransport upgrade failed", r, TransportWebTransport, "error=%v", err)
		http.Error(w, "webtransport_upgrade_failed", http.StatusInternalServerError)
		return
	}
	acceptCtx, cancel := context.WithTimeout(r.Context(), webTransportAcceptStreamTimeout)
	defer cancel()
	stream, err := wtSession.AcceptStream(acceptCtx)
	if err != nil {
		logSessionRequest("webtransport accept stream failed", r, TransportWebTransport, "error=%v", err)
		_ = wtSession.CloseWithError(0, "stream_accept_timeout")
		return
	}
	id := l.nextID.Add(1)
	sess := newWebTransportSession(id, wtSession, stream)
	sess.Data = sessionData
	sess.RemoteAddr = r.RemoteAddr
	sess.Transport = TransportWebTransport
	l.serveAcceptedSession(wtSession.Context(), sess, r)
}

// sendSnapshot writes world data (if any) then entity state to a newly
// connected session. World frames are already fully wrapped by the
// worldSnapshotFunc closure and are written directly — they never pass
// through messageWrapper (which applies entity-only WrapEntityUpdate).
// When interest management is enabled, entity snapshots are skipped;
// the interest system sends initial visibility on the first tick.
func (l *Listener) sendSnapshot(ctx context.Context, sess *Session) error {
	if l.worldSnapshotFunc != nil {
		worldFrames, err := l.worldSnapshotFunc()
		if err != nil {
			return err
		}
		for _, frame := range worldFrames {
			if err := validateReliableMessageSize(frame); err != nil {
				return err
			}
			if err := sess.writeBatch(ctx, [][]byte{frame}); err != nil {
				return err
			}
		}
	}

	if l.interestEnabled {
		return nil
	}

	snapshots, err := l.registry.SnapshotAll()
	if err != nil {
		return err
	}
	for _, data := range snapshots {
		wrapped := l.wrap(data)
		if err := validateReliableMessageSize(wrapped); err != nil {
			return err
		}
		if err := sess.writeBatch(ctx, [][]byte{wrapped}); err != nil {
			return err
		}
	}
	return nil
}

// wrap applies the messageWrapper if one is set, otherwise returns data as-is.
func (l *Listener) wrap(data []byte) []byte {
	if l.messageWrapper != nil {
		return l.messageWrapper(data)
	}
	return data
}

func (l *Listener) addSession(s *Session) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sessions[s.ID] = s
}

func (l *Listener) removeSession(s *Session) {
	l.mu.Lock()
	delete(l.sessions, s.ID)
	l.mu.Unlock()
	l.outboundLogMu.Lock()
	delete(l.outboundDgramPrev, s.ID)
	delete(l.outboundStreamPrev, s.ID)
	l.outboundLogMu.Unlock()
	s.shutdown()
}

func (l *Listener) authorizeSessionRequest(w http.ResponseWriter, r *http.Request, transport Transport) (any, bool) {
	if l.onUpgrade == nil {
		return nil, true
	}
	data, err := l.onUpgrade(r)
	if err != nil {
		logSessionRequest("refused session upgrade", r, transport, "error=%v", err)
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return nil, false
	}
	return data, true
}

func logSessionRequest(prefix string, r *http.Request, transport Transport, extra string, args ...any) {
	origin := ""
	host := ""
	remote := ""
	if r != nil {
		origin = r.Header.Get("Origin")
		host = r.Host
		remote = r.RemoteAddr
	}
	msg := fmt.Sprintf("golem/net: %s remote=%q origin=%q host=%q transport=%q", prefix, remote, origin, host, transport)
	if extra != "" {
		msg += " " + fmt.Sprintf(extra, args...)
	}
	log.Print(msg)
}

func (l *Listener) serveAcceptedSession(ctx context.Context, sess *Session, r *http.Request) {
	if err := l.sendSnapshot(ctx, sess); err != nil {
		log.Printf("golem/net: session %d setup failed: snapshot remote=%q transport=%q error=%v", sess.ID, sess.RemoteAddr, sess.Transport, err)
		sess.Close()
		return
	}
	origin := ""
	host := ""
	if r != nil {
		origin = r.Header.Get("Origin")
		host = r.Host
	}
	log.Printf("golem/net: session %d accepted remote=%q origin=%q host=%q transport=%q", sess.ID, sess.RemoteAddr, origin, host, sess.Transport)
	l.addSession(sess)
	if l.onConnect != nil {
		l.onConnect(sess)
	}
	go sess.writePump(ctx)
	sess.readPump(ctx, l.onMessage, l.onDatagram, l.onReliableUnordered, l.onReliableOrdered, l.onEventualStateFeedback)
	log.Printf("golem/net: session %d disconnected remote=%q transport=%q", sess.ID, sess.RemoteAddr, sess.Transport)
	l.removeSession(sess)
	if l.onDisconnect != nil {
		l.onDisconnect(sess)
	}
}

// ConfigureWebTransportHTTP3Server applies the HTTP/3 settings required by golem WebTransport handlers.
func ConfigureWebTransportHTTP3Server(h3 *http3.Server) *http3.Server {
	if h3 == nil {
		h3 = &http3.Server{}
	}
	h3.EnableDatagrams = true
	webtransport.ConfigureHTTP3Server(h3)
	return h3
}

// WebTransportServer returns the configured WebTransport server for callers
// that mount the listener on a caller-owned HTTP/3 server. Callers still own
// TLS configuration and server startup.
func (l *Listener) WebTransportServer(h3 *http3.Server) *webtransport.Server {
	if l.config.Transport != TransportWebTransport {
		return nil
	}
	return l.ensureWebTransportServer(h3)
}

func (l *Listener) ensureWebTransportServer(h3 *http3.Server) *webtransport.Server {
	if l.wtServer != nil {
		if h3 != nil {
			l.wtServer.H3 = ConfigureWebTransportHTTP3Server(h3)
		}
		l.wtServer.CheckOrigin = l.wtCheckOrigin
		return l.wtServer
	}
	l.wtServer = &webtransport.Server{
		H3:          ConfigureWebTransportHTTP3Server(h3),
		CheckOrigin: l.wtCheckOrigin,
	}
	return l.wtServer
}

func newWebTransportCheckOrigin(cfg Config) func(*http.Request) bool {
	allowedOrigins := make(map[string]struct{}, len(cfg.WebTransportAllowedOrigins))
	for _, origin := range cfg.WebTransportAllowedOrigins {
		canonical, ok := canonicalWebTransportOrigin(origin)
		if !ok {
			log.Printf("golem/net: ignoring invalid WebTransport allowed origin %q", origin)
			continue
		}
		allowedOrigins[canonical] = struct{}{}
	}

	return func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		if origin == "" {
			return true
		}
		u, err := url.Parse(origin)
		if err != nil {
			log.Printf("golem/net: rejected WebTransport origin %q for host %q: invalid Origin header: %v", origin, r.Host, err)
			return false
		}
		if u.Scheme == "" || u.Host == "" {
			log.Printf("golem/net: rejected WebTransport origin %q for host %q: missing origin scheme or host", origin, r.Host)
			return false
		}
		if equalASCIIFold(u.Host, r.Host) {
			return true
		}

		canonical, ok := canonicalWebTransportOrigin(origin)
		if ok {
			if _, allowed := allowedOrigins[canonical]; allowed {
				return true
			}
		}

		originHost := u.Hostname()
		requestHost := requestHostname(r.Host)
		sameHostname := equalASCIIFold(originHost, requestHost)
		if cfg.WebTransportAllowSameHostOrigin {
			if sameHostname && equalASCIIFold(u.Scheme, "https") {
				return true
			}
			if sameHostname && !cfg.DevSelfSignedCert {
				log.Printf("golem/net: rejected WebTransport origin %q for host %q: same-host mode requires https origin", origin, r.Host)
				return false
			}
		}

		if !cfg.DevSelfSignedCert {
			if cfg.WebTransportAllowSameHostOrigin {
				log.Printf("golem/net: rejected WebTransport origin %q for host %q: origin hostname %q does not match request hostname %q", origin, r.Host, originHost, requestHost)
				return false
			}
			if len(allowedOrigins) > 0 {
				log.Printf("golem/net: rejected WebTransport origin %q for host %q: origin is not same host and does not match WebTransportAllowedOrigins", origin, r.Host)
				return false
			}
			log.Printf("golem/net: rejected WebTransport origin %q for host %q: origin is not same host and is not configured as allowed", origin, r.Host)
			return false
		}
		if isLoopbackHost(originHost) && isLoopbackHost(requestHost) {
			return true
		}
		log.Printf("golem/net: rejected WebTransport origin %q for host %q: DevSelfSignedCert only allows loopback cross-port origins", origin, r.Host)
		return false
	}
}

func canonicalWebTransportOrigin(origin string) (string, bool) {
	u, err := url.Parse(origin)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", false
	}
	if u.User != nil {
		return "", false
	}
	if u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return "", false
	}
	return strings.ToLower(u.Scheme) + "://" + strings.ToLower(u.Host), true
}

func requestHostname(hostport string) string {
	host, _, err := net.SplitHostPort(hostport)
	if err == nil {
		return host
	}
	return hostport
}

func isLoopbackHost(host string) bool {
	if equalASCIIFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func equalASCIIFold(s string, t string) bool {
	if len(s) != len(t) {
		return false
	}
	for i := 0; i < len(s); i++ {
		a, b := s[i], t[i]
		if 'A' <= a && a <= 'Z' {
			a += 'a' - 'A'
		}
		if 'A' <= b && b <= 'Z' {
			b += 'a' - 'A'
		}
		if a != b {
			return false
		}
	}
	return true
}
