package host

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/demiurgos-hub/golem-engine/golem"
	"github.com/demiurgos-hub/golem-engine/golem/inspector"
	"github.com/demiurgos-hub/golem-engine/golem/inspector/wire"
	"github.com/demiurgos-hub/golem-engine/golem/snapshot"
)

// Well-known sidecar paths and the snapshot file name used by RunGame.
const (
	// DefaultRealtimeConfigPath serves the realtime bootstrap JSON
	// (golem.Server.RealtimeConfigHandler) that golem-js fetchRealtimeConfig,
	// golem-go-client FetchRealtimeConfig and the Unity bootstrap read.
	DefaultRealtimeConfigPath = "/api/realtime-config"
	// DefaultWebSocketPath serves golem.Server.WebSocketHandler: the primary
	// endpoint for a WebSocket server, the fallback for a WebTransport one.
	DefaultWebSocketPath = "/api/ws"
	// SnapshotFileName is the entity snapshot RunGame keeps in
	// SnapshotOptions.Dir.
	SnapshotFileName = "world.snapshot"
)

// Keys RunGame adds to AdminInfo.Extra when snapshots are enabled, unless the
// game already set them.
const (
	// AdminExtraSnapshotFile is the snapshot file path.
	AdminExtraSnapshotFile = "snapshot_file"
	// AdminExtraSnapshotRestored is "true" when startup loaded the snapshot.
	AdminExtraSnapshotRestored = "snapshot_restored"
)

// SnapshotOptions configures RunGame's entity snapshots. SnapshotOptionsFromEnv
// overlays GOLEM_SNAPSHOT_DIR and GOLEM_SNAPSHOT_INTERVAL.
type SnapshotOptions struct {
	// Dir enables snapshots: RunGame creates it when missing, loads
	// Dir/SnapshotFileName before the server starts when it exists, and saves
	// it again after the tick loop stops. Empty disables snapshots.
	Dir string
	// Interval, when positive, also saves the snapshot this often while the
	// server runs, captured on the tick goroutine. Zero disables autosave.
	Interval time.Duration
}

// GameBindings are the schema-specific facts and helpers RunGame needs from a
// generated go-server runtime. The generated Runtime.Host fills them in; games
// do not build them by hand.
type GameBindings struct {
	// SchemaFingerprint is the generated SchemaFingerprint constant.
	SchemaFingerprint string
	// InspectorSchema is the generated InspectorSchema(); nil disables the
	// inspector agent.
	InspectorSchema *wire.Schema
	// SaveSnapshot is Runtime.SaveSnapshot, used for the final save once the
	// tick loop has stopped.
	SaveSnapshot func(path string) <-chan error
	// SaveSnapshotOnTick is Runtime.SaveSnapshotOnTick, used by the admin
	// POST /snapshot endpoint while the server runs.
	SaveSnapshotOnTick func(path string) <-chan error
	// LoadSnapshotIfExists is Runtime.LoadSnapshotIfExists.
	LoadSnapshotIfExists func(path string) (bool, error)
	// RunSnapshotAutosave is Runtime.RunSnapshotAutosave.
	RunSnapshotAutosave func(ctx context.Context, path string, interval time.Duration) error
}

// GameOptions is what a game passes to RunGame (as the generated HostOptions).
// The zero value runs only the server, exactly like Run(ctx,
// RunOptions{Server: srv}); environment variables then add the sidecar, admin
// listener, snapshots and inspector agent without code changes.
type GameOptions struct {
	// Name identifies the game in admin GET /info and logs. Default: the
	// executable name without extension.
	Name string

	// HTTPAddr is the sidecar listen address used when GOLEM_HTTP_ADDR is
	// unset, e.g. "127.0.0.1:8080" for local development; it overrides
	// HTTPServer.Addr. When neither is set (and GOLEM_HTTP_ADDR is unset) no
	// sidecar runs and API and assets are not served.
	HTTPAddr string
	// HTTPServer is an optional base sidecar server for timeouts, ErrorLog or
	// a TLSConfig. Its Handler must be nil: RunGame installs the sidecar mux.
	// RunGame modifies it in place (Addr, Handler, TLSConfig).
	HTTPServer *http.Server
	// API serves requests under APIPrefix on the sidecar, below the fixed
	// realtime-config and WebSocket routes.
	API http.Handler
	// APIPrefix routes requests to API before assets; default "/api".
	APIPrefix string
	// AssetsDir serves a built browser client from disk; when the directory
	// exists it wins over Assets (see SPAHandler).
	AssetsDir string
	// Assets serves an embedded browser client, with index.html fallback.
	Assets fs.FS

	// Realtime is the base realtime bootstrap configuration;
	// golem.RealtimeConfigOptionsFromEnv overlays GOLEM_PUBLIC_URL,
	// GOLEM_WS_FALLBACK_URL and GOLEM_TLS_PINNED.
	Realtime golem.RealtimeConfigOptions
	// RealtimeConfigPath overrides DefaultRealtimeConfigPath.
	RealtimeConfigPath string
	// WebSocketPath overrides DefaultWebSocketPath.
	WebSocketPath string

	// Admin is the base admin listener configuration; GOLEM_ADMIN_ADDR and
	// GOLEM_ADMIN_TOKEN overlay Addr and Token. Info.Name,
	// Info.SchemaFingerprint and Info.CertificateHashesPublished are always
	// filled in by RunGame. When snapshots are enabled and Snapshot is nil,
	// POST /snapshot saves the snapshot file on the tick goroutine.
	Admin AdminOptions
	// Snapshots is the base snapshot configuration; GOLEM_SNAPSHOT_DIR and
	// GOLEM_SNAPSHOT_INTERVAL overlay it.
	Snapshots SnapshotOptions

	// AfterRestore, when set, is called once before the server starts, after
	// the snapshot load attempt (or immediately when snapshots are disabled);
	// restored reports whether a snapshot was loaded. Seed the initial world
	// here when restored is false, and adopt restored entities (for example
	// through the generated Register<Entity>Wrapper hooks) when it is true. An
	// error aborts startup.
	AfterRestore func(restored bool) error

	// ShutdownTimeout is RunOptions.ShutdownTimeout.
	ShutdownTimeout time.Duration
}

// RunGame runs srv with everything a deploy tool configures from the
// environment, then blocks like Run until ctx is cancelled, a signal arrives
// or a component fails.
//
// Startup, in order: resolve options from the environment
// (RealtimeConfigOptionsFromEnv, SnapshotOptionsFromEnv, RunOptionsFromEnv,
// inspector.OptionsFromEnv) and validate them → create the snapshot directory
// and load SnapshotFileName if it exists (a fingerprint mismatch or any other
// load error fails startup, naming both fingerprints for a mismatch) → call
// AfterRestore → attach the inspector agent when GOLEM_INSPECTOR_HUB_URL is
// set → start autosave → Run (admin listener, Server.Run, WaitReady, sidecar,
// ready).
//
// Shutdown follows Run: /ready fails, the tick loop stops, autosave is stopped
// and awaited, the final snapshot is saved (RunOptions.AfterServerStop), the
// sidecar and admin listener stop, and the inspector agent is closed.
//
// The sidecar, when an address is configured, serves Realtime at
// RealtimeConfigPath (503 while no public URL is configured, because clients
// require one), srv.WebSocketHandler at WebSocketPath, and API plus assets
// through SPAHandler for every other path. RunGame never invents realtime
// URLs: without GOLEM_WS_FALLBACK_URL (or Realtime.WebSocketFallbackURL) no
// fallback is advertised, and a fallback with a non-WebTransport primary fails
// startup.
func RunGame(ctx context.Context, srv *golem.Server, b GameBindings, opts GameOptions) error {
	if srv == nil {
		return errors.New("golem/host: RunGame requires a server")
	}
	g, err := newGame(srv, b, opts)
	if err != nil {
		return err
	}
	restored, err := g.restore()
	if err != nil {
		return err
	}
	if opts.AfterRestore != nil {
		if err := opts.AfterRestore(restored); err != nil {
			return fmt.Errorf("golem/host: after restore: %w", err)
		}
	}
	runOpts := g.runOptions(restored)

	var agent *inspector.Agent
	if b.InspectorSchema != nil {
		agent, err = inspector.Attach(ctx, srv, b.InspectorSchema, inspector.OptionsFromEnv())
		if err != nil {
			return fmt.Errorf("golem/host: %w", err)
		}
		defer agent.Close()
	}

	g.logStartup(runOpts, agent != nil)
	g.startAutosave(ctx)
	defer g.stopAutosave()
	return Run(ctx, runOpts)
}

// game is one resolved RunGame configuration.
type game struct {
	srv      *golem.Server
	b        GameBindings
	opts     GameOptions
	name     string
	realtime golem.RealtimeConfigOptions
	snaps    SnapshotOptions
	path     string // snapshot file; empty when snapshots are disabled
	side     *http.Server

	autosaveCancel context.CancelFunc
	autosaveDone   chan struct{}
	stopOnce       sync.Once
}

func newGame(srv *golem.Server, b GameBindings, opts GameOptions) (*game, error) {
	g := &game{
		srv:      srv,
		b:        b,
		opts:     opts,
		name:     opts.Name,
		realtime: golem.RealtimeConfigOptionsFromEnv(opts.Realtime),
		snaps:    SnapshotOptionsFromEnv(opts.Snapshots),
	}
	if g.name == "" {
		g.name = strings.TrimSuffix(filepath.Base(os.Args[0]), filepath.Ext(os.Args[0]))
	}
	if g.realtime.WebSocketFallbackURL != "" && srv.Transport() != golem.TransportWebTransport {
		return nil, fmt.Errorf("golem/host: a WebSocket fallback URL (%s) requires a WebTransport primary transport, have %q", golem.EnvWebSocketFallbackURL, srv.Transport())
	}
	if g.snaps.Dir != "" {
		if b.LoadSnapshotIfExists == nil || b.SaveSnapshot == nil || b.SaveSnapshotOnTick == nil || b.RunSnapshotAutosave == nil {
			return nil, errors.New("golem/host: snapshots require the generated snapshot bindings")
		}
		g.path = filepath.Join(g.snaps.Dir, SnapshotFileName)
	}
	side, err := g.sidecar()
	if err != nil {
		return nil, err
	}
	g.side = side
	return g, nil
}

// sidecar returns the configured sidecar server, or nil when no address is
// configured.
func (g *game) sidecar() (*http.Server, error) {
	opts := g.opts
	if opts.HTTPServer != nil && opts.HTTPServer.Handler != nil {
		return nil, errors.New("golem/host: GameOptions.HTTPServer.Handler must be nil; pass routes as API and assets")
	}
	addr := opts.HTTPAddr
	if addr == "" && opts.HTTPServer != nil {
		addr = opts.HTTPServer.Addr
	}
	if v, ok := lookupEnvTrimmed(EnvHTTPAddr); ok {
		addr = v
	}
	if addr == "" {
		if opts.API != nil || opts.Assets != nil || opts.AssetsDir != "" {
			log.Printf("golem/host: no sidecar address (%s); API and assets are not served", EnvHTTPAddr)
		}
		return nil, nil
	}
	realtimePath := opts.RealtimeConfigPath
	if realtimePath == "" {
		realtimePath = DefaultRealtimeConfigPath
	}
	wsPath := opts.WebSocketPath
	if wsPath == "" {
		wsPath = DefaultWebSocketPath
	}
	for _, p := range []string{realtimePath, wsPath} {
		if !strings.HasPrefix(p, "/") || strings.ContainsAny(p, " {}") {
			return nil, fmt.Errorf("golem/host: sidecar path %q must be an absolute URL path", p)
		}
	}
	if realtimePath == wsPath {
		return nil, fmt.Errorf("golem/host: realtime config and WebSocket paths are both %q", wsPath)
	}

	mux := http.NewServeMux()
	mux.Handle(realtimePath, g.realtimeHandler())
	mux.Handle(wsPath, g.srv.WebSocketHandler())
	rest := SPAHandler(SPAOptions{
		APIHandler: opts.API,
		APIPrefix:  opts.APIPrefix,
		EmbeddedFS: opts.Assets,
		DiskDir:    opts.AssetsDir,
	})
	if rest == nil {
		rest = http.NotFoundHandler()
	}
	mux.Handle("/", rest)

	side := opts.HTTPServer
	if side == nil {
		side = &http.Server{ReadHeaderTimeout: 10 * time.Second}
	}
	side.Addr = addr
	side.Handler = mux
	return side, nil
}

// realtimeHandler serves the realtime bootstrap JSON, or 503 while no public
// URL is configured: clients reject a bootstrap without a URL, and RunGame
// does not guess one.
func (g *game) realtimeHandler() http.Handler {
	if g.realtime.PublicURL == "" {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			http.Error(w, "golem: realtime public URL is not configured; set "+golem.EnvPublicURL, http.StatusServiceUnavailable)
		})
	}
	return g.srv.RealtimeConfigHandler(g.realtime)
}

// restore creates the snapshot directory and loads the snapshot when it
// exists. It reports whether a snapshot was loaded.
func (g *game) restore() (bool, error) {
	if g.path == "" {
		return false, nil
	}
	if err := os.MkdirAll(g.snaps.Dir, 0o755); err != nil {
		return false, fmt.Errorf("golem/host: snapshot directory: %w", err)
	}
	restored, err := g.b.LoadSnapshotIfExists(g.path)
	if err == nil {
		if restored {
			log.Printf("golem/host: restored snapshot path=%q entities=%d", g.path, g.srv.Len())
		}
		return restored, nil
	}
	if errors.Is(err, snapshot.ErrFingerprintMismatch) {
		stored, readErr := snapshot.ReadFingerprint(g.path)
		if readErr != nil {
			stored = "unreadable: " + readErr.Error()
		}
		return false, fmt.Errorf("golem/host: snapshot %s has schema fingerprint %s but this server's schema fingerprint is %s; move the file away to start empty, or deploy a build with the matching schema: %w", g.path, stored, g.b.SchemaFingerprint, err)
	}
	return false, fmt.Errorf("golem/host: loading snapshot %s: %w", g.path, err)
}

// runOptions builds the Run options for the resolved configuration.
func (g *game) runOptions(restored bool) RunOptions {
	admin := g.opts.Admin
	admin.Info.Name = g.name
	admin.Info.SchemaFingerprint = g.b.SchemaFingerprint
	admin.Info.CertificateHashesPublished = g.srv.IncludesServerCertificateHashes(g.realtime)
	if g.path != "" {
		extra := make(map[string]string, len(admin.Info.Extra)+2)
		extra[AdminExtraSnapshotFile] = g.path
		extra[AdminExtraSnapshotRestored] = strconv.FormatBool(restored)
		for k, v := range admin.Info.Extra {
			extra[k] = v
		}
		admin.Info.Extra = extra
		if admin.Snapshot == nil {
			admin.Snapshot = g.saveOnTick
		}
	}
	opts := RunOptionsFromEnv(RunOptions{
		Server:          g.srv,
		HTTPServer:      g.side,
		ShutdownTimeout: g.opts.ShutdownTimeout,
		Admin:           &admin,
	})
	if g.path != "" {
		opts.AfterServerStop = g.finalSave
	}
	return opts
}

// saveOnTick is the admin snapshot: a capture on the tick goroutine.
func (g *game) saveOnTick(ctx context.Context) error {
	select {
	case err := <-g.b.SaveSnapshotOnTick(g.path):
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// finalSave stops autosave and saves the snapshot once the tick loop has
// stopped.
func (g *game) finalSave() error {
	g.stopAutosave()
	start := time.Now()
	if err := <-g.b.SaveSnapshot(g.path); err != nil {
		return fmt.Errorf("final snapshot %s: %w", g.path, err)
	}
	log.Printf("golem/host: saved final snapshot path=%q entities=%d in %s", g.path, g.srv.Len(), time.Since(start).Round(time.Millisecond))
	return nil
}

// startAutosave runs the generated autosave loop until stopAutosave. A failed
// save is logged and the loop restarts, so one transient write error does not
// end autosaving; the loop also ends when the server stops.
func (g *game) startAutosave(ctx context.Context) {
	if g.path == "" || g.snaps.Interval <= 0 {
		return
	}
	ctx, g.autosaveCancel = context.WithCancel(ctx)
	g.autosaveDone = make(chan struct{})
	go func() {
		defer close(g.autosaveDone)
		for {
			err := g.b.RunSnapshotAutosave(ctx, g.path, g.snaps.Interval)
			if ctx.Err() != nil || errors.Is(err, golem.ErrServerNotRunning) {
				return
			}
			log.Printf("golem/host: autosave to %q failed; retrying in %s: %v", g.path, g.snaps.Interval, err)
		}
	}()
}

// stopAutosave cancels autosave and waits for it to return. It is safe to call
// more than once and when autosave never started.
func (g *game) stopAutosave() {
	g.stopOnce.Do(func() {
		if g.autosaveCancel == nil {
			return
		}
		g.autosaveCancel()
		<-g.autosaveDone
	})
}

func (g *game) logStartup(opts RunOptions, inspectorAttached bool) {
	sidecar, admin := "", ""
	if opts.HTTPServer != nil {
		sidecar = opts.HTTPServer.Addr
	}
	if opts.Admin != nil {
		admin = opts.Admin.Addr
	}
	log.Printf("golem/host: starting game=%q transport=%q sidecar=%q admin=%q snapshot=%q autosave=%s inspector=%t",
		g.name, g.srv.Transport(), sidecar, admin, g.path, g.snaps.Interval, inspectorAttached)
}
