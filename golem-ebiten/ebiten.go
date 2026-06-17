// Package golemebiten provides Ebiten helpers for Golem Engine clients.
package golemebiten

import (
	"context"
	"log"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	golemclient "golem-engine/golem-go-client"
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

// ConnectionOptionsFunc returns connection options for each connect attempt.
type ConnectionOptionsFunc func(context.Context) (golemclient.ConnectOptions, error)

// GameConfig configures the Ebiten lifecycle helper.
type GameConfig struct {
	Client               Client
	Connection           golemclient.ConnectOptions
	ConnectionOptions    ConnectionOptionsFunc
	MaxReconnectAttempts int
	ReconnectBaseDelay   time.Duration
	OnConnect            func()
	OnDisconnect         func(golemclient.DisconnectInfo)
	OnReconnectFailed    func()
}

// Game owns a Golem client lifecycle inside an Ebiten game loop.
type Game struct {
	client               Client
	connection           golemclient.ConnectOptions
	connectionOptions    ConnectionOptionsFunc
	maxReconnectAttempts int
	reconnectBaseDelay   time.Duration
	onConnect            func()
	onDisconnect         func(golemclient.DisconnectInfo)
	onReconnectFailed    func()
	attempts             int
	nextReconnect        time.Time
	connected            bool
	reconnectPending     bool
	views                []View
}

// NewGame creates an Ebiten lifecycle helper around a Golem client.
func NewGame(cfg GameConfig) *Game {
	baseDelay := cfg.ReconnectBaseDelay
	if baseDelay == 0 {
		baseDelay = 1500 * time.Millisecond
	}
	g := &Game{
		client:               cfg.Client,
		connection:           cfg.Connection,
		connectionOptions:    cfg.ConnectionOptions,
		maxReconnectAttempts: cfg.MaxReconnectAttempts,
		reconnectBaseDelay:   baseDelay,
		onConnect:            cfg.OnConnect,
		onDisconnect:         cfg.OnDisconnect,
		onReconnectFailed:    cfg.OnReconnectFailed,
	}
	if g.client != nil {
		g.client.OnDisconnect(func(info golemclient.DisconnectInfo) {
			g.connected = false
			log.Printf("golem-ebiten: disconnected was_clean=%v error=%v", info.WasClean, info.Err)
			if g.onDisconnect != nil {
				g.onDisconnect(info)
			}
			if !info.WasClean {
				g.scheduleReconnect()
			}
		})
	}
	return g
}

// Connect opens the configured Golem connection.
func (g *Game) Connect(ctx context.Context) error {
	if g.client == nil {
		return nil
	}
	options, err := g.resolveConnectionOptions(ctx)
	if err != nil {
		log.Printf("golem-ebiten: connection options failed error=%v", err)
		g.scheduleReconnect()
		return err
	}
	if err := g.client.Connect(ctx, options); err != nil {
		log.Printf(
			"golem-ebiten: connect failed transport=%s url=%q error=%v",
			options.Transport,
			golemclient.RedactURL(options.URL),
			err,
		)
		g.scheduleReconnect()
		return err
	}
	g.connected = true
	g.reconnectPending = false
	g.attempts = 0
	log.Printf(
		"golem-ebiten: connected transport=%s url=%q",
		options.Transport,
		golemclient.RedactURL(options.URL),
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
	g.connected = false
	g.reconnectPending = false
}

// AddView registers a generated entity view for Update and Draw.
func (g *Game) AddView(view View) {
	if view != nil {
		g.views = append(g.views, view)
	}
}

// Update advances reconnect timers and registered views.
func (g *Game) Update(ctx context.Context) error {
	if g.reconnectPending && !time.Now().Before(g.nextReconnect) {
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
func (g *Game) Connected() bool { return g.connected }

func (g *Game) resolveConnectionOptions(ctx context.Context) (golemclient.ConnectOptions, error) {
	if g.connectionOptions != nil {
		return g.connectionOptions(ctx)
	}
	return g.connection, nil
}

func (g *Game) scheduleReconnect() {
	if g.maxReconnectAttempts > 0 && g.attempts >= g.maxReconnectAttempts {
		g.reconnectPending = false
		log.Printf("golem-ebiten: reconnect failed attempts=%d", g.attempts)
		if g.onReconnectFailed != nil {
			g.onReconnectFailed()
		}
		return
	}
	g.attempts++
	delay := g.reconnectBaseDelay << max(g.attempts-1, 0)
	g.nextReconnect = time.Now().Add(delay)
	g.reconnectPending = true
	log.Printf("golem-ebiten: reconnect scheduled attempt=%d delay=%s", g.attempts, delay)
}
