package host

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/demiurgos-hub/golem-engine/golem"
	"github.com/demiurgos-hub/golem-engine/golem/inspector"
	"github.com/demiurgos-hub/golem-engine/golem/inspector/wire"
	"github.com/demiurgos-hub/golem-engine/golem/snapshot"
)

// clearGameEnv blanks every variable RunGame reads.
func clearGameEnv(t *testing.T) {
	t.Helper()
	clearHostEnv(t)
	for _, name := range []string{
		EnvSnapshotDir, EnvSnapshotInterval,
		golem.EnvPublicURL, golem.EnvWebSocketFallbackURL, golem.EnvTLSPinned,
		inspector.EnvHubURL, inspector.EnvToken, inspector.EnvTargetID, inspector.EnvLabels, inspector.EnvRateHz,
	} {
		t.Setenv(name, "")
	}
}

// probe is the smallest snapshot-capable entity: an ID and one byte of state.
type probe struct {
	id    int64
	token atomic.Uint32
}

func (p *probe) EntityID() int64              { return p.id }
func (p *probe) SetEntityID(id int64)         { p.id = id }
func (p *probe) TypeName() string             { return "Probe" }
func (p *probe) Position() (float32, float32) { return 0, 0 }
func (p *probe) IsGlobal() bool               { return true }
func (p *probe) FlushUpdate() ([]byte, error) { return nil, nil }
func (p *probe) FullUpdate() ([]byte, error) {
	buf := make([]byte, 12)
	binary.BigEndian.PutUint64(buf, uint64(p.id))
	binary.BigEndian.PutUint32(buf[8:], p.token.Load())
	return buf, nil
}

func restoreProbe(rec snapshot.Record) (golem.Entity, error) {
	if len(rec.StateData) != 12 {
		return nil, fmt.Errorf("bad probe record")
	}
	p := &probe{id: int64(binary.BigEndian.Uint64(rec.StateData))}
	p.token.Store(binary.BigEndian.Uint32(rec.StateData[8:]))
	return p, nil
}

// probeBindings mirrors the generated Runtime snapshot wrappers for srv.
func probeBindings(srv *golem.Server, fingerprint string) GameBindings {
	return GameBindings{
		SchemaFingerprint: fingerprint,
		SaveSnapshot: func(path string) <-chan error {
			return srv.SaveSnapshot(fingerprint, path)
		},
		SaveSnapshotOnTick: func(path string) <-chan error {
			return srv.SaveSnapshotOnTick(fingerprint, path)
		},
		LoadSnapshotIfExists: func(path string) (bool, error) {
			if err := srv.LoadSnapshot(path, fingerprint, restoreProbe); err != nil {
				if errors.Is(err, os.ErrNotExist) {
					return false, nil
				}
				return false, err
			}
			return true, nil
		},
		RunSnapshotAutosave: func(ctx context.Context, path string, interval time.Duration) error {
			ticker := time.NewTicker(interval)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-ticker.C:
					if err := <-srv.SaveSnapshotOnTick(fingerprint, path); err != nil {
						return err
					}
				}
			}
		},
	}
}

// readProbes loads the probes saved at path. It retries briefly because on
// Windows a read can collide with an autosave renaming over the file.
func readProbes(t *testing.T, path, fingerprint string) map[int64]uint32 {
	t.Helper()
	var records []snapshot.Record
	waitFor(t, "readable snapshot", func() bool {
		var err error
		records, err = snapshot.Load(path, fingerprint)
		return err == nil
	})
	out := map[int64]uint32{}
	for _, rec := range records {
		e, err := restoreProbe(rec)
		if err != nil {
			t.Fatal(err)
		}
		out[e.EntityID()] = e.(*probe).token.Load()
	}
	return out
}

type gameHandle struct {
	cancel   context.CancelFunc
	done     chan error
	adminURL string
}

// startGame runs RunGame in the background and waits for the admin listener
// when one is configured through the environment.
func startGame(t *testing.T, srv *golem.Server, b GameBindings, opts GameOptions) *gameHandle {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	h := &gameHandle{cancel: cancel, done: make(chan error, 1)}
	addrCh := make(chan string, 1)
	opts.Admin.listening = func(a net.Addr) { addrCh <- a.String() }
	go func() { h.done <- RunGame(ctx, srv, b, opts) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-h.done:
		case <-time.After(10 * time.Second):
			t.Error("RunGame did not return during cleanup")
		}
	})
	if os.Getenv(EnvAdminAddr) != "" {
		select {
		case addr := <-addrCh:
			h.adminURL = "http://" + addr
		case err := <-h.done:
			t.Fatalf("RunGame returned before the admin listener started: %v", err)
		case <-time.After(5 * time.Second):
			t.Fatal("admin listener did not start")
		}
	}
	return h
}

func (h *gameHandle) stop(t *testing.T) error {
	t.Helper()
	h.cancel()
	select {
	case err := <-h.done:
		h.done <- err
		return err
	case <-time.After(10 * time.Second):
		t.Fatal("RunGame did not return")
		return nil
	}
}

func waitReady(t *testing.T, adminURL string) {
	t.Helper()
	waitFor(t, "admin ready", func() bool {
		return adminDo(t, http.MethodGet, adminURL+"/ready", "", nil).status == http.StatusOK
	})
}

func TestSnapshotOptionsFromEnv(t *testing.T) {
	base := SnapshotOptions{Dir: "base", Interval: time.Minute}
	tests := []struct {
		name    string
		env     map[string]string
		want    SnapshotOptions
		logPart string
	}{
		{name: "unset keeps base", want: base},
		{name: "blank keeps base", env: map[string]string{EnvSnapshotDir: " ", EnvSnapshotInterval: " "}, want: base},
		{name: "overrides", env: map[string]string{EnvSnapshotDir: " /data/snap ", EnvSnapshotInterval: " 30s "}, want: SnapshotOptions{Dir: "/data/snap", Interval: 30 * time.Second}},
		{name: "zero disables autosave", env: map[string]string{EnvSnapshotInterval: "0"}, want: SnapshotOptions{Dir: "base"}},
		{name: "malformed interval ignored", env: map[string]string{EnvSnapshotInterval: "5"}, want: base, logPart: EnvSnapshotInterval},
		{name: "negative interval ignored", env: map[string]string{EnvSnapshotInterval: "-1s"}, want: base, logPart: "negative"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearGameEnv(t)
			logs := captureLog(t)
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			if got := SnapshotOptionsFromEnv(base); got != tt.want {
				t.Fatalf("SnapshotOptionsFromEnv = %+v, want %+v", got, tt.want)
			}
			if tt.logPart != "" && !strings.Contains(logs.String(), tt.logPart) {
				t.Fatalf("log %q does not mention %q", logs.String(), tt.logPart)
			}
		})
	}
}

func TestRunGameZeroOptionsIsPlainRun(t *testing.T) {
	clearGameEnv(t)
	srv := newTestServer()
	g, err := newGame(srv, probeBindings(srv, "fp"), GameOptions{})
	if err != nil {
		t.Fatal(err)
	}
	opts := g.runOptions(false)
	if opts.HTTPServer != nil || opts.AfterServerStop != nil || opts.Admin == nil || opts.Admin.Addr != "" || opts.HTTPTLSCertFile != "" {
		t.Fatalf("zero options configured more than the server: %+v", opts)
	}

	called := false
	h := startGame(t, srv, probeBindings(srv, "fp"), GameOptions{AfterRestore: func(restored bool) error {
		called = true
		if restored {
			t.Error("restored without snapshots")
		}
		return nil
	}})
	waitFor(t, "server ticking", func() bool { return srv.TickStats().Tick > 2 })
	if err := h.stop(t); err != nil {
		t.Fatalf("RunGame = %v", err)
	}
	if !called {
		t.Fatal("AfterRestore was not called")
	}
}

func TestRunGameSnapshotLifecycle(t *testing.T) {
	clearGameEnv(t)
	dir := filepath.Join(t.TempDir(), "missing", "snapshots")
	path := filepath.Join(dir, SnapshotFileName)
	t.Setenv(EnvSnapshotDir, dir)
	t.Setenv(EnvSnapshotInterval, "20ms")
	t.Setenv(EnvAdminAddr, "127.0.0.1:0")
	const fp = "fingerprint-v1"

	// First start: nothing to restore, so the game seeds its world.
	srv := newTestServer()
	seeded := &probe{}
	h := startGame(t, srv, probeBindings(srv, fp), GameOptions{Name: "probe-game", AfterRestore: func(restored bool) error {
		if restored {
			return errors.New("unexpected restore")
		}
		seeded.token.Store(1)
		return srv.CreateEntity(seeded)
	}})
	waitReady(t, h.adminURL)
	waitFor(t, "autosave file", func() bool {
		_, err := os.Stat(path)
		return err == nil
	})
	if got := readProbes(t, path, fp)[seeded.id]; got != 1 {
		t.Fatalf("autosave token = %d, want 1", got)
	}

	info := adminDo(t, http.MethodGet, h.adminURL+"/info", "", nil)
	extra, _ := info.body["extra"].(map[string]any)
	if info.body["name"] != "probe-game" || info.body["schema_fingerprint"] != fp || extra[AdminExtraSnapshotRestored] != "false" || extra[AdminExtraSnapshotFile] != path {
		t.Fatalf("/info = %s", info.raw)
	}

	// Mutate on the tick goroutine, then capture through the admin endpoint.
	srv.Post(func(*golem.Server) { seeded.token.Store(2) })
	if resp := adminDo(t, http.MethodPost, h.adminURL+"/snapshot", "", nil); resp.status != http.StatusOK {
		t.Fatalf("POST /snapshot = %d %s", resp.status, resp.raw)
	}
	if got := readProbes(t, path, fp)[seeded.id]; got != 2 {
		t.Fatalf("admin snapshot token = %d, want 2", got)
	}

	// The last tick's state reaches disk only through the final save.
	srv.Post(func(*golem.Server) { seeded.token.Store(3) })
	waitFor(t, "post applied", func() bool { return seeded.token.Load() == 3 })
	if err := h.stop(t); err != nil {
		t.Fatalf("RunGame = %v", err)
	}
	if got := readProbes(t, path, fp)[seeded.id]; got != 3 {
		t.Fatalf("final snapshot token = %d, want 3", got)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Fatalf("snapshot dir holds %d entries, want only %s", len(entries), SnapshotFileName)
	}

	// Second start restores before the server runs and skips seeding.
	srv2 := newTestServer()
	var restoredFlag atomic.Bool
	h2 := startGame(t, srv2, probeBindings(srv2, fp), GameOptions{AfterRestore: func(restored bool) error {
		restoredFlag.Store(restored)
		if srv2.Len() != 1 {
			return fmt.Errorf("entities before start = %d, want 1", srv2.Len())
		}
		return nil
	}})
	waitReady(t, h2.adminURL)
	if !restoredFlag.Load() {
		t.Fatal("AfterRestore(false) on second start")
	}
	e, ok := srv2.Get(seeded.id)
	if !ok || e.(*probe).token.Load() != 3 {
		t.Fatalf("restored entity = %v, %v", e, ok)
	}
	info = adminDo(t, http.MethodGet, h2.adminURL+"/info", "", nil)
	if extra, _ := info.body["extra"].(map[string]any); extra[AdminExtraSnapshotRestored] != "true" {
		t.Fatalf("/info after restore = %s", info.raw)
	}
	if err := h2.stop(t); err != nil {
		t.Fatalf("second RunGame = %v", err)
	}
}

func TestRunGameFingerprintMismatchFailsStartup(t *testing.T) {
	clearGameEnv(t)
	dir := t.TempDir()
	t.Setenv(EnvSnapshotDir, dir)
	old := newTestServer()
	if err := old.CreateEntity(&probe{}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, SnapshotFileName)
	if err := <-old.SaveSnapshot("schema-old", path); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	srv := newTestServer()
	err = RunGame(context.Background(), srv, probeBindings(srv, "schema-new"), GameOptions{AfterRestore: func(bool) error {
		t.Error("AfterRestore called after a failed load")
		return nil
	}})
	if !errors.Is(err, snapshot.ErrFingerprintMismatch) {
		t.Fatalf("RunGame = %v, want ErrFingerprintMismatch", err)
	}
	for _, want := range []string{"schema-old", "schema-new", path} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not name %q", err, want)
		}
	}
	if srv.TickStats().Tick != 0 {
		t.Fatal("server ran despite the mismatch")
	}
	if after, _ := os.ReadFile(path); string(after) != string(before) {
		t.Fatal("snapshot file was modified")
	}
}

func TestRunGameSidecarRoutes(t *testing.T) {
	clearGameEnv(t)
	addr := freeAddr(t)
	t.Setenv(EnvHTTPAddr, addr)
	t.Setenv(EnvAdminAddr, "127.0.0.1:0")
	t.Setenv(golem.EnvPublicURL, "wss://game.example/api/ws")
	t.Setenv(golem.EnvTLSPinned, "true")

	srv := golem.NewServer(golem.ServerConfig{TickRate: 100, Transport: golem.TransportWebSocket, StateUpdateLane: golem.StateUpdateLaneStream})
	api := http.NewServeMux()
	api.HandleFunc("/api/hello", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "hello") })
	h := startGame(t, srv, probeBindings(srv, "fp"), GameOptions{
		HTTPAddr: "127.0.0.1:1", // overridden by GOLEM_HTTP_ADDR
		API:      api,
		Assets:   fstest.MapFS{"index.html": {Data: []byte("<html>spa</html>")}},
	})
	waitReady(t, h.adminURL)
	// Ready implies the sidecar is listening.
	conn, err := net.DialTimeout("tcp", addr, time.Second)
	if err != nil {
		t.Fatalf("sidecar not listening once ready: %v", err)
	}
	_ = conn.Close()

	get := func(path string) (int, string) {
		t.Helper()
		resp, err := http.Get("http://" + addr + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(body)
	}
	status, body := get(DefaultRealtimeConfigPath)
	var cfg map[string]any
	if err := json.Unmarshal([]byte(body), &cfg); status != http.StatusOK || err != nil {
		t.Fatalf("realtime config = %d %q", status, body)
	}
	if cfg["transport"] != "websocket" || cfg["url"] != "wss://game.example/api/ws" || cfg["fallback"] != nil {
		t.Fatalf("realtime config = %s", body)
	}
	if status, body := get("/api/hello"); status != http.StatusOK || body != "hello" {
		t.Fatalf("/api/hello = %d %q", status, body)
	}
	if status, body := get("/rooms/1"); status != http.StatusOK || !strings.Contains(body, "spa") {
		t.Fatalf("SPA fallback = %d %q", status, body)
	}
	if status, _ := get(DefaultWebSocketPath); status < 400 || status == http.StatusNotFound {
		t.Fatalf("plain GET %s = %d, want a WebSocket handshake rejection", DefaultWebSocketPath, status)
	}
	info := adminDo(t, http.MethodGet, h.adminURL+"/info", "", nil)
	if info.body["certificate_hashes_published"] != true {
		t.Fatalf("/info = %s", info.raw)
	}
	if resp := adminDo(t, http.MethodPost, h.adminURL+"/snapshot", "", nil); resp.status != http.StatusNotImplemented {
		t.Fatalf("POST /snapshot without snapshots = %d", resp.status)
	}
	if err := h.stop(t); err != nil {
		t.Fatalf("RunGame = %v", err)
	}
}

func TestRunGameRealtimeConfigWithoutPublicURL(t *testing.T) {
	clearGameEnv(t)
	addr := freeAddr(t)
	t.Setenv(EnvHTTPAddr, addr)
	srv := golem.NewServer(golem.ServerConfig{TickRate: 100, Transport: golem.TransportWebSocket, StateUpdateLane: golem.StateUpdateLaneStream})
	h := startGame(t, srv, probeBindings(srv, "fp"), GameOptions{RealtimeConfigPath: "/bootstrap", WebSocketPath: "/play"})
	var resp *http.Response
	waitFor(t, "sidecar", func() bool {
		var err error
		resp, err = http.Get("http://" + addr + "/bootstrap")
		return err == nil
	})
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable || !strings.Contains(string(body), golem.EnvPublicURL) {
		t.Fatalf("realtime config without public URL = %d %q", resp.StatusCode, body)
	}
	if resp, err := http.Get("http://" + addr + "/missing"); err != nil || resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unrouted path = %v %v", resp, err)
	}
	if err := h.stop(t); err != nil {
		t.Fatalf("RunGame = %v", err)
	}
}

func TestRunGameRejectsInvalidConfiguration(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		opts GameOptions
		cfg  golem.ServerConfig
		want string
	}{
		{name: "fallback needs WebTransport primary", env: map[string]string{golem.EnvWebSocketFallbackURL: "wss://x/ws"},
			cfg: golem.ServerConfig{Transport: golem.TransportWebSocket, StateUpdateLane: golem.StateUpdateLaneStream}, want: golem.EnvWebSocketFallbackURL},
		{name: "caller handler", env: map[string]string{EnvHTTPAddr: "127.0.0.1:0"},
			opts: GameOptions{HTTPServer: &http.Server{Handler: http.NotFoundHandler()}}, want: "Handler must be nil"},
		{name: "relative path", env: map[string]string{EnvHTTPAddr: "127.0.0.1:0"},
			opts: GameOptions{WebSocketPath: "ws"}, want: "absolute URL path"},
		{name: "inspector without token", env: map[string]string{inspector.EnvHubURL: "ws://127.0.0.1:1/ws/agent"},
			want: "Token is required"},
		{name: "admin without token off loopback", env: map[string]string{EnvAdminAddr: ":0"}, want: "not loopback-only"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearGameEnv(t)
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			srv := golem.NewServer(tt.cfg)
			b := probeBindings(srv, "fp")
			b.InspectorSchema = inspectorTestSchema()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			err := RunGame(ctx, srv, b, tt.opts)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("RunGame = %v, want error containing %q", err, tt.want)
			}
		})
	}
}

func TestRunGameAutosaveSurvivesFailedSave(t *testing.T) {
	clearGameEnv(t)
	dir := t.TempDir()
	t.Setenv(EnvSnapshotDir, dir)
	t.Setenv(EnvSnapshotInterval, "5ms")
	srv := newTestServer()
	b := probeBindings(srv, "fp")
	var calls atomic.Int32
	inner := b.RunSnapshotAutosave
	b.RunSnapshotAutosave = func(ctx context.Context, path string, interval time.Duration) error {
		if calls.Add(1) == 1 {
			return errors.New("disk full")
		}
		return inner(ctx, path, interval)
	}
	logs := captureLog(t)
	h := startGame(t, srv, b, GameOptions{})
	waitFor(t, "autosave restart", func() bool {
		_, err := os.Stat(filepath.Join(dir, SnapshotFileName))
		return calls.Load() >= 2 && err == nil
	})
	if err := h.stop(t); err != nil {
		t.Fatalf("RunGame = %v", err)
	}
	if !strings.Contains(logs.String(), "disk full") {
		t.Fatalf("autosave failure not logged: %q", logs.String())
	}
}

func inspectorTestSchema() *wire.Schema {
	return &wire.Schema{Dimensions: 2, RemovedTag: 3, Entities: []wire.Entity{{Name: "Probe", StateTag: 1, DeltaTag: 2}}}
}
