// Package golemebiten provides Ebiten helpers for Golem Engine clients.
package golemebiten

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	golemclient "github.com/demiurgos-hub/golem-engine/golem-go-client"
	"github.com/hajimehoshi/ebiten/v2"
)

// Screen is the Ebiten render target type used by generated views.
type Screen = *ebiten.Image

// Drawable is the minimal render handle used by generated entity views.
type Drawable interface {
	Draw(Screen)
	Destroy()
}

// Positioned can be implemented by drawables that sync to entity coordinates.
type Positioned interface {
	SetPosition(x, y float32)
}

// View is the optional interface implemented by generated entity views.
type View interface {
	Update() error
	Draw(Screen)
}

// Client is the subset of golem-go-client.GameClient used by Game.
type Client interface {
	Connect(context.Context, golemclient.ConnectOptions) error
	Disconnect()
	OnDisconnect(func(golemclient.DisconnectInfo))
}

// ConnectionPlanClient is implemented by clients that support sequential
// transport fallback plans and actual connected-transport reporting.
type ConnectionPlanClient interface {
	Client
	ConnectPlan(context.Context, golemclient.ConnectionPlan) error
	ConnectedTransport() golemclient.TransportKind
}

// ConnectionOptionsFunc returns connection options for each connect attempt.
type ConnectionOptionsFunc func(context.Context) (golemclient.ConnectOptions, error)

// ConnectionPlanProvider returns a credential-free connection plan for each
// initial or reconnect attempt. Per-dial credentials belong in the plan's
// resolver.
type ConnectionPlanProvider func(context.Context) (golemclient.ConnectionPlan, error)

// GameConfig configures the Ebiten lifecycle helper.
type GameConfig struct {
	Client                 Client
	ConnectionPlan         golemclient.ConnectionPlan
	ConnectionPlanProvider ConnectionPlanProvider
	Connection             golemclient.ConnectOptions
	ConnectionOptions      ConnectionOptionsFunc
	MaxReconnectAttempts   int
	ReconnectBaseDelay     time.Duration
	OnConnect              func()
	OnDisconnect           func(golemclient.DisconnectInfo)
	OnReconnectFailed      func()
}

// Game owns a Golem client lifecycle inside an Ebiten game loop.
type Game struct {
	client                 Client
	connectionPlan         golemclient.ConnectionPlan
	connectionPlanProvider ConnectionPlanProvider
	connection             golemclient.ConnectOptions
	connectionOptions      ConnectionOptionsFunc
	maxReconnectAttempts   int
	reconnectBaseDelay     time.Duration
	onConnect              func()
	onDisconnect           func(golemclient.DisconnectInfo)
	onReconnectFailed      func()
	stateMu                sync.RWMutex
	connectionGeneration   uint64
	attempts               int
	nextReconnect          time.Time
	connected              bool
	connectedTransport     golemclient.TransportKind
	reconnectPending       bool
	views                  []View
}

// NewGame creates an Ebiten lifecycle helper around a Golem client.
func NewGame(cfg GameConfig) *Game {
	baseDelay := cfg.ReconnectBaseDelay
	if baseDelay == 0 {
		baseDelay = 1500 * time.Millisecond
	}
	g := &Game{
		client:                 cfg.Client,
		connectionPlan:         cfg.ConnectionPlan,
		connectionPlanProvider: cfg.ConnectionPlanProvider,
		connection:             cfg.Connection,
		connectionOptions:      cfg.ConnectionOptions,
		maxReconnectAttempts:   cfg.MaxReconnectAttempts,
		reconnectBaseDelay:     baseDelay,
		onConnect:              cfg.OnConnect,
		onDisconnect:           cfg.OnDisconnect,
		onReconnectFailed:      cfg.OnReconnectFailed,
	}
	if g.client != nil {
		g.client.OnDisconnect(func(info golemclient.DisconnectInfo) {
			g.stateMu.Lock()
			g.connected = false
			g.connectedTransport = ""
			var reconnect reconnectSchedule
			if !info.WasClean {
				// Only a close that owns the reconnect lifecycle invalidates an
				// in-flight completion. GameClient intentionally emits a clean
				// close when Connect replaces an already-open session.
				g.connectionGeneration++
				reconnect = g.scheduleReconnectLocked()
			}
			g.stateMu.Unlock()

			log.Printf("golem-ebiten: disconnected was_clean=%v error=%v", info.WasClean, info.Err)
			if g.onDisconnect != nil {
				g.onDisconnect(info)
			}
			g.reportReconnectSchedule(reconnect)
		})
	}
	return g
}

// Connect opens the configured Golem connection.
func (g *Game) Connect(ctx context.Context) error {
	if g.client == nil {
		return nil
	}
	g.stateMu.Lock()
	g.connectionGeneration++
	generation := g.connectionGeneration
	// A pending reconnect is consumed by this physical attempt. A failure below
	// schedules the next backoff step; a close callback during the attempt owns
	// the replacement schedule through the generation fence.
	g.reconnectPending = false
	g.stateMu.Unlock()

	transport, endpoint, err := g.connectConfigured(ctx)
	if err != nil {
		log.Printf("golem-ebiten: connect failed transport=%s url=%q error=%v", transport, endpoint, err)
		g.stateMu.Lock()
		var reconnect reconnectSchedule
		if g.connectionGeneration == generation {
			reconnect = g.scheduleReconnectLocked()
		}
		g.stateMu.Unlock()
		g.reportReconnectSchedule(reconnect)
		return err
	}

	g.stateMu.Lock()
	if g.connectionGeneration != generation {
		g.stateMu.Unlock()
		return nil
	}
	g.connected = true
	g.connectedTransport = transport
	g.reconnectPending = false
	g.attempts = 0
	g.stateMu.Unlock()

	log.Printf(
		"golem-ebiten: connected transport=%s url=%q",
		transport,
		endpoint,
	)
	if g.onConnect != nil {
		g.onConnect()
	}
	return nil
}

// Disconnect closes the Golem connection.
func (g *Game) Disconnect() {
	log.Print("golem-ebiten: disconnecting")
	if g.client != nil {
		g.client.Disconnect()
	}
	g.stateMu.Lock()
	g.connectionGeneration++
	g.connected = false
	g.connectedTransport = ""
	g.reconnectPending = false
	g.stateMu.Unlock()
}

// AddView registers a generated entity view for Update and Draw.
func (g *Game) AddView(view View) {
	if view != nil {
		g.views = append(g.views, view)
	}
}

// Update advances reconnect timers and registered views.
func (g *Game) Update(ctx context.Context) error {
	g.stateMu.RLock()
	reconnectReady := g.reconnectPending && !time.Now().Before(g.nextReconnect)
	g.stateMu.RUnlock()
	if reconnectReady {
		if err := g.Connect(ctx); err != nil {
			return err
		}
	}
	for _, view := range g.views {
		if err := view.Update(); err != nil {
			return err
		}
	}
	return nil
}

// Draw renders registered views.
func (g *Game) Draw(screen Screen) {
	for _, view := range g.views {
		view.Draw(screen)
	}
}

// Connected reports whether the latest connection attempt succeeded.
func (g *Game) Connected() bool {
	g.stateMu.RLock()
	defer g.stateMu.RUnlock()
	return g.connected
}

// ConnectedTransport reports the transport used by the active connection.
func (g *Game) ConnectedTransport() golemclient.TransportKind {
	g.stateMu.RLock()
	defer g.stateMu.RUnlock()
	return g.connectedTransport
}

func (g *Game) connectConfigured(ctx context.Context) (golemclient.TransportKind, string, error) {
	if g.connectionPlanProvider != nil || hasConnectionPlan(g.connectionPlan) {
		plan := g.connectionPlan
		if g.connectionPlanProvider != nil {
			var err error
			plan, err = g.connectionPlanProvider(ctx)
			if err != nil {
				return "", "", newProviderError("connection plan provider", err)
			}
		}
		client, ok := g.client.(ConnectionPlanClient)
		if !ok {
			return "", "", fmt.Errorf("golem-ebiten: configured client does not support connection plans")
		}
		if err := client.ConnectPlan(ctx, plan); err != nil {
			return "", "", err
		}
		transport := client.ConnectedTransport()
		return transport, golemclient.RedactURL(connectionPlanURL(plan, transport)), nil
	}

	options, err := g.resolveConnectionOptions(ctx)
	if err != nil {
		return "", "", newProviderError("connection options provider", err)
	}
	if err := g.client.Connect(ctx, options); err != nil {
		return options.Transport, golemclient.RedactURL(options.URL), err
	}
	transport := options.Transport
	if transport == "" {
		transport = golemclient.TransportWebSocket
	}
	if client, ok := g.client.(interface {
		ConnectedTransport() golemclient.TransportKind
	}); ok && client.ConnectedTransport() != "" {
		transport = client.ConnectedTransport()
	}
	return transport, golemclient.RedactURL(options.URL), nil
}

func (g *Game) resolveConnectionOptions(ctx context.Context) (golemclient.ConnectOptions, error) {
	if g.connectionOptions != nil {
		return g.connectionOptions(ctx)
	}
	return g.connection, nil
}

func hasConnectionPlan(plan golemclient.ConnectionPlan) bool {
	return plan.Primary.Transport != "" ||
		plan.Primary.URL != "" ||
		plan.Fallback != nil ||
		plan.ResolveOptions != nil ||
		plan.WebTransportTimeout != 0
}

func connectionPlanURL(plan golemclient.ConnectionPlan, transport golemclient.TransportKind) string {
	if transport == plan.Primary.Transport {
		return plan.Primary.URL
	}
	if plan.Fallback != nil && transport == plan.Fallback.Transport {
		return plan.Fallback.URL
	}
	return ""
}

type providerError struct {
	provider string
	cause    error
}

func newProviderError(provider string, cause error) error {
	return &providerError{provider: provider, cause: cause}
}

func (e *providerError) Error() string {
	return fmt.Sprintf("golem-ebiten: %s failed", e.provider)
}

func (e *providerError) Unwrap() error { return e.cause }

type reconnectSchedule struct {
	scheduled bool
	failed    bool
	attempts  int
	delay     time.Duration
}

func (g *Game) scheduleReconnectLocked() reconnectSchedule {
	if g.maxReconnectAttempts > 0 && g.attempts >= g.maxReconnectAttempts {
		g.reconnectPending = false
		return reconnectSchedule{failed: true, attempts: g.attempts}
	}
	g.attempts++
	delay := g.reconnectBaseDelay << max(g.attempts-1, 0)
	g.nextReconnect = time.Now().Add(delay)
	g.reconnectPending = true
	return reconnectSchedule{scheduled: true, attempts: g.attempts, delay: delay}
}

func (g *Game) reportReconnectSchedule(reconnect reconnectSchedule) {
	if reconnect.failed {
		log.Printf("golem-ebiten: reconnect failed attempts=%d", reconnect.attempts)
		if g.onReconnectFailed != nil {
			g.onReconnectFailed()
		}
		return
	}
	if reconnect.scheduled {
		log.Printf("golem-ebiten: reconnect scheduled attempt=%d delay=%s", reconnect.attempts, reconnect.delay)
	}
}
