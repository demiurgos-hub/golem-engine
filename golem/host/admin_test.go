package host

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/demiurgos-hub/golem-engine/golem"
)

// newTestAdmin builds an admin handler outside Run so tests control readiness
// and shutdown directly.
func newTestAdmin(t *testing.T, opts AdminOptions, srv *golem.Server) (*adminServer, context.CancelFunc) {
	t.Helper()
	if opts.Addr == "" {
		opts.Addr = "127.0.0.1:0"
	}
	cfg, err := newAdminConfig(opts)
	if err != nil {
		t.Fatalf("newAdminConfig: %v", err)
	}
	if srv == nil {
		srv = newTestServer()
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return newAdminServer(ctx, cfg, srv, nil), cancel
}

func serveAdmin(t *testing.T, a *adminServer, method, path, token string) adminResponse {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	req.Host = "127.0.0.1:9090"
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	a.ServeHTTP(rec, req)
	resp := rec.Result()
	defer resp.Body.Close()
	out := adminResponse{status: resp.StatusCode, header: resp.Header, raw: rec.Body.String()}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Fatalf("%s %s Content-Type = %q", method, path, ct)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("%s %s Cache-Control = %q", method, path, cc)
	}
	if err := jsonUnmarshalObject(rec.Body.Bytes(), &out.body); err != nil {
		t.Fatalf("%s %s: %v: %q", method, path, err, out.raw)
	}
	return out
}

func TestAdminConfigFailsClosed(t *testing.T) {
	tests := []struct {
		name    string
		addr    string
		token   string
		wantErr bool
	}{
		{name: "all interfaces without token", addr: ":9090", wantErr: true},
		{name: "unspecified IPv4 without token", addr: "0.0.0.0:9090", wantErr: true},
		{name: "unspecified IPv6 without token", addr: "[::]:9090", wantErr: true},
		{name: "public host without token", addr: "example.com:9090", wantErr: true},
		{name: "private IP without token", addr: "10.0.0.5:9090", wantErr: true},
		{name: "malformed address", addr: "nonsense", wantErr: true},
		{name: "IPv4 loopback without token", addr: "127.0.0.1:9090"},
		{name: "IPv6 loopback without token", addr: "[::1]:9090"},
		{name: "localhost without token", addr: "localhost:9090"},
		{name: "LOCALHOST without token", addr: "LOCALHOST:9090"},
		{name: "all interfaces with token", addr: ":9090", token: testToken},
		{name: "short token", addr: "127.0.0.1:9090", token: "short-secret", wantErr: true},
		{name: "minimum token", addr: ":9090", token: strings.Repeat("k", MinAdminTokenLength)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := newAdminConfig(AdminOptions{Addr: tt.addr, Token: tt.token})
			if (err != nil) != tt.wantErr {
				t.Fatalf("newAdminConfig(%q) error = %v, wantErr %v", tt.addr, err, tt.wantErr)
			}
			if err != nil && tt.token != "" && strings.Contains(err.Error(), tt.token) {
				t.Fatalf("error leaks the token: %v", err)
			}
		})
	}
}

func TestRunAdminFailsClosedBeforeStarting(t *testing.T) {
	srv := newTestServer()
	for _, admin := range []AdminOptions{{Addr: ":0"}, {Addr: "127.0.0.1:0", Token: "too-short"}} {
		err := Run(context.Background(), RunOptions{Server: srv, Admin: &admin})
		if err == nil {
			t.Fatalf("Run accepted admin options %+v", admin)
		}
		if admin.Token != "" && strings.Contains(err.Error(), admin.Token) {
			t.Fatalf("error leaks the token: %v", err)
		}
	}
	if err := Run(context.Background(), RunOptions{HTTPServer: &http.Server{}, Admin: &AdminOptions{Addr: "127.0.0.1:0"}}); err == nil {
		t.Fatal("Run accepted Admin without Server")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := srv.Run(ctx); errors.Is(err, golem.ErrServerAlreadyRun) {
		t.Fatal("Run started the server despite invalid admin options")
	}
}

func TestAdminAuthMatrix(t *testing.T) {
	a, _ := newTestAdmin(t, AdminOptions{Token: testToken}, nil)
	a.ready.Store(true)
	endpoints := []struct {
		method, path string
		authorized   int
	}{
		{http.MethodGet, "/ready", http.StatusOK},
		{http.MethodGet, "/info", http.StatusOK},
		{http.MethodGet, "/stats", http.StatusOK},
		{http.MethodPost, "/snapshot", http.StatusNotImplemented},
		{http.MethodPost, "/reload-tls", http.StatusOK},
		{http.MethodGet, "/unknown", http.StatusNotFound},
		{http.MethodPost, "/ready", http.StatusMethodNotAllowed},
	}
	for _, ep := range endpoints {
		for _, tc := range []struct {
			name  string
			token string
			raw   string
			want  int
		}{
			{name: "no token", want: http.StatusUnauthorized},
			{name: "wrong token", token: testToken + "x", want: http.StatusUnauthorized},
			{name: "prefix of token", token: testToken[:len(testToken)-1], want: http.StatusUnauthorized},
			{name: "basic scheme", raw: "Basic " + testToken, want: http.StatusUnauthorized},
			{name: "right token", token: testToken, want: ep.authorized},
			{name: "lowercase scheme", raw: "bearer " + testToken, want: ep.authorized},
		} {
			req := httptest.NewRequest(ep.method, ep.path, nil)
			switch {
			case tc.raw != "":
				req.Header.Set("Authorization", tc.raw)
			case tc.token != "":
				req.Header.Set("Authorization", "Bearer "+tc.token)
			}
			rec := httptest.NewRecorder()
			a.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Errorf("%s %s with %s = %d, want %d (%s)", ep.method, ep.path, tc.name, rec.Code, tc.want, rec.Body.String())
			}
			if tc.want == http.StatusUnauthorized {
				if got := rec.Header().Get("WWW-Authenticate"); !strings.HasPrefix(got, "Bearer") {
					t.Errorf("%s %s 401 WWW-Authenticate = %q", ep.method, ep.path, got)
				}
				if strings.Contains(rec.Body.String(), testToken) {
					t.Errorf("401 body leaks the token")
				}
			}
		}
	}
	// /live is open.
	if resp := serveAdmin(t, a, http.MethodGet, "/live", ""); resp.status != http.StatusOK || resp.body["live"] != true {
		t.Fatalf("/live without token = %d %s", resp.status, resp.raw)
	}
}

func TestAdminLoopbackWithoutTokenIsOpen(t *testing.T) {
	a, _ := newTestAdmin(t, AdminOptions{Addr: "127.0.0.1:0"}, nil)
	a.ready.Store(true)
	if resp := serveAdmin(t, a, http.MethodGet, "/ready", ""); resp.status != http.StatusOK {
		t.Fatalf("/ready without token on loopback = %d %s", resp.status, resp.raw)
	}
}

func TestAdminReadyTransitions(t *testing.T) {
	a, stop := newTestAdmin(t, AdminOptions{Token: testToken}, nil)
	check := func(wantStatus int, wantReason string) {
		t.Helper()
		resp := serveAdmin(t, a, http.MethodGet, "/ready", testToken)
		reason, _ := resp.body["reason"].(string)
		if resp.status != wantStatus || reason != wantReason || resp.body["ready"] != (wantStatus == http.StatusOK) {
			t.Fatalf("/ready = %d %s, want %d reason %q", resp.status, resp.raw, wantStatus, wantReason)
		}
		if live := serveAdmin(t, a, http.MethodGet, "/live", ""); live.status != http.StatusOK {
			t.Fatalf("/live = %d", live.status)
		}
	}
	check(http.StatusServiceUnavailable, "starting")
	a.ready.Store(true)
	check(http.StatusOK, "")
	stop()
	check(http.StatusServiceUnavailable, "stopping")
}

func TestAdminMethodAndNotFoundJSON(t *testing.T) {
	a, _ := newTestAdmin(t, AdminOptions{Token: testToken}, nil)
	for _, tc := range []struct {
		method, path, token string
		status              int
		allow, code         string
	}{
		{http.MethodPost, "/live", "", http.StatusMethodNotAllowed, http.MethodGet, "method_not_allowed"},
		{http.MethodGet, "/snapshot", testToken, http.StatusMethodNotAllowed, http.MethodPost, "method_not_allowed"},
		{http.MethodGet, "/reload-tls", testToken, http.StatusMethodNotAllowed, http.MethodPost, "method_not_allowed"},
		{http.MethodDelete, "/stats", testToken, http.StatusMethodNotAllowed, http.MethodGet, "method_not_allowed"},
		{http.MethodGet, "/debug/pprof/", testToken, http.StatusNotFound, "", "not_found"},
		{http.MethodGet, "/", testToken, http.StatusNotFound, "", "not_found"},
		{http.MethodGet, "/ready/", testToken, http.StatusNotFound, "", "not_found"},
	} {
		resp := serveAdmin(t, a, tc.method, tc.path, tc.token)
		if resp.status != tc.status || resp.body["error"] != tc.code || resp.header.Get("Allow") != tc.allow {
			t.Errorf("%s %s = %d Allow=%q %s", tc.method, tc.path, resp.status, resp.header.Get("Allow"), resp.raw)
		}
	}
}

func TestAdminSnapshotBodyLimit(t *testing.T) {
	a, _ := newTestAdmin(t, AdminOptions{Token: testToken, Snapshot: func(context.Context) error { return nil }}, nil)
	a.ready.Store(true)
	req := httptest.NewRequest(http.MethodPost, "/snapshot", strings.NewReader(strings.Repeat("x", adminMaxBodyBytes+1)))
	req.Header.Set("Authorization", "Bearer "+testToken)
	rec := httptest.NewRecorder()
	a.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized body = %d %s", rec.Code, rec.Body.String())
	}
}

func TestAdminSnapshot(t *testing.T) {
	t.Run("not configured", func(t *testing.T) {
		a, _ := newTestAdmin(t, AdminOptions{Token: testToken}, nil)
		a.ready.Store(true)
		if resp := serveAdmin(t, a, http.MethodPost, "/snapshot", testToken); resp.status != http.StatusNotImplemented || resp.body["error"] != "not_implemented" {
			t.Fatalf("= %d %s", resp.status, resp.raw)
		}
	})

	t.Run("not ready and stopping", func(t *testing.T) {
		var calls atomic.Int32
		a, stop := newTestAdmin(t, AdminOptions{Token: testToken, Snapshot: func(context.Context) error { calls.Add(1); return nil }}, nil)
		resp := serveAdmin(t, a, http.MethodPost, "/snapshot", testToken)
		if resp.status != http.StatusServiceUnavailable || resp.body["reason"] != "starting" || resp.body["error"] != "not_ready" {
			t.Fatalf("starting = %d %s", resp.status, resp.raw)
		}
		a.ready.Store(true)
		stop()
		resp = serveAdmin(t, a, http.MethodPost, "/snapshot", testToken)
		if resp.status != http.StatusServiceUnavailable || resp.body["reason"] != "stopping" {
			t.Fatalf("stopping = %d %s", resp.status, resp.raw)
		}
		if calls.Load() != 0 {
			t.Fatal("snapshot ran while not ready")
		}
	})

	t.Run("success and single flight", func(t *testing.T) {
		entered := make(chan struct{})
		release := make(chan struct{})
		a, _ := newTestAdmin(t, AdminOptions{Token: testToken, Snapshot: func(ctx context.Context) error {
			close(entered)
			<-release
			return nil
		}}, nil)
		a.ready.Store(true)
		first := make(chan adminResponse, 1)
		go func() { first <- serveAdmin(t, a, http.MethodPost, "/snapshot", testToken) }()
		<-entered
		if resp := serveAdmin(t, a, http.MethodPost, "/snapshot", testToken); resp.status != http.StatusConflict || resp.body["error"] != "snapshot_in_progress" {
			t.Fatalf("concurrent snapshot = %d %s", resp.status, resp.raw)
		}
		close(release)
		resp := <-first
		if resp.status != http.StatusOK || resp.body["ok"] != true {
			t.Fatalf("snapshot = %d %s", resp.status, resp.raw)
		}
		if d, ok := resp.body["duration_ms"].(float64); !ok || d < 0 {
			t.Fatalf("duration_ms = %v", resp.body["duration_ms"])
		}
	})

	t.Run("failure is generic", func(t *testing.T) {
		a, _ := newTestAdmin(t, AdminOptions{Token: testToken, Snapshot: func(context.Context) error {
			return errors.New("disk /secret/path full")
		}}, nil)
		a.ready.Store(true)
		resp := serveAdmin(t, a, http.MethodPost, "/snapshot", testToken)
		if resp.status != http.StatusInternalServerError || resp.body["error"] != "snapshot_failed" || strings.Contains(resp.raw, "/secret/path") {
			t.Fatalf("= %d %s", resp.status, resp.raw)
		}
	})

	t.Run("panic is contained", func(t *testing.T) {
		a, _ := newTestAdmin(t, AdminOptions{Token: testToken, Snapshot: func(context.Context) error { panic("boom") }}, nil)
		a.ready.Store(true)
		if resp := serveAdmin(t, a, http.MethodPost, "/snapshot", testToken); resp.status != http.StatusInternalServerError {
			t.Fatalf("= %d %s", resp.status, resp.raw)
		}
		// The slot was released.
		waitFor(t, "slot release", func() bool { return len(a.snapshotSem) == 0 })
	})

	t.Run("timeout honouring context", func(t *testing.T) {
		a, _ := newTestAdmin(t, AdminOptions{Token: testToken, SnapshotTimeout: 30 * time.Millisecond, Snapshot: func(ctx context.Context) error {
			<-ctx.Done()
			return ctx.Err()
		}}, nil)
		a.ready.Store(true)
		if resp := serveAdmin(t, a, http.MethodPost, "/snapshot", testToken); resp.status != http.StatusGatewayTimeout || resp.body["error"] != "snapshot_timeout" {
			t.Fatalf("= %d %s", resp.status, resp.raw)
		}
	})

	t.Run("timeout ignoring context keeps the slot", func(t *testing.T) {
		release := make(chan struct{})
		a, _ := newTestAdmin(t, AdminOptions{Token: testToken, SnapshotTimeout: 30 * time.Millisecond, Snapshot: func(context.Context) error {
			<-release
			return nil
		}}, nil)
		a.ready.Store(true)
		if resp := serveAdmin(t, a, http.MethodPost, "/snapshot", testToken); resp.status != http.StatusGatewayTimeout {
			t.Fatalf("= %d %s", resp.status, resp.raw)
		}
		if resp := serveAdmin(t, a, http.MethodPost, "/snapshot", testToken); resp.status != http.StatusConflict {
			t.Fatalf("while the timed-out snapshot still runs = %d %s", resp.status, resp.raw)
		}
		close(release)
		waitFor(t, "slot release", func() bool { return len(a.snapshotSem) == 0 })
	})
}

func TestAdminReloadTLSUnsupportedAndNotLoaded(t *testing.T) {
	a, _ := newTestAdmin(t, AdminOptions{Token: testToken}, nil)
	resp := serveAdmin(t, a, http.MethodPost, "/reload-tls", testToken)
	if resp.status != http.StatusOK || resp.body["ok"] != true || resp.body["webtransport"] != "unsupported" || resp.body["http"] != "unsupported" {
		t.Fatalf("headless server = %d %s", resp.status, resp.raw)
	}

	f := newSidecarTLSFixture(t, newTestCertPair(t))
	notRunning := golem.NewServer(golem.ServerConfig{Addr: "127.0.0.1:0", TLSCertFile: f.certPath, TLSKeyFile: f.keyPath})
	a, _ = newTestAdmin(t, AdminOptions{Token: testToken}, notRunning)
	resp = serveAdmin(t, a, http.MethodPost, "/reload-tls", testToken)
	if resp.status != http.StatusServiceUnavailable || resp.body["ok"] != false || resp.body["webtransport"] != "not_loaded" {
		t.Fatalf("listener not serving = %d %s", resp.status, resp.raw)
	}
}

func TestRunAdminEndToEnd(t *testing.T) {
	pairA, pairB := newTestCertPair(t), newTestCertPair(t)
	f := newSidecarTLSFixture(t, pairA)
	srv := golem.NewServer(golem.ServerConfig{
		Addr:        "127.0.0.1:0",
		TickRate:    100,
		TLSCertFile: f.certPath,
		TLSKeyFile:  f.keyPath,
	})
	snapshotPath := filepath.Join(t.TempDir(), "admin.snapshot")
	hookEntered := make(chan struct{})
	hookRelease := make(chan struct{})
	h := startRun(t, RunOptions{
		Server: srv,
		Admin: &AdminOptions{
			Token: testToken,
			Info: AdminInfo{
				Name:                       "end-to-end",
				SchemaFingerprint:          "fp-123",
				CertificateHashesPublished: true,
				Extra:                      map[string]string{"region": "eu"},
			},
			Snapshot: func(ctx context.Context) error {
				return <-srv.SaveSnapshotOnTick("fp-123", snapshotPath)
			},
		},
		AfterServerStop: func() error {
			close(hookEntered)
			<-hookRelease
			return nil
		},
	})
	url := h.adminURL

	if resp := adminDo(t, http.MethodGet, url+"/live", "", nil); resp.status != http.StatusOK {
		t.Fatalf("/live = %d", resp.status)
	}
	waitFor(t, "ready", func() bool {
		return adminDo(t, http.MethodGet, url+"/ready", testToken, nil).status == http.StatusOK
	})

	info := adminDo(t, http.MethodGet, url+"/info", testToken, nil)
	if info.status != http.StatusOK ||
		info.body["name"] != "end-to-end" || info.body["schema_fingerprint"] != "fp-123" ||
		info.body["transport"] != "webtransport" || info.body["tick_rate"] != float64(100) ||
		info.body["certificate_hashes_published"] != true || info.body["http_tls_reloadable"] != false ||
		info.body["engine_version"] == "" || !strings.HasPrefix(info.body["go_version"].(string), "go") {
		t.Fatalf("/info = %s", info.raw)
	}
	if extra, _ := info.body["extra"].(map[string]any); extra["region"] != "eu" {
		t.Fatalf("/info extra = %v", info.body["extra"])
	}
	if _, err := time.Parse(time.RFC3339Nano, info.body["started_at"].(string)); err != nil {
		t.Fatalf("started_at: %v", err)
	}
	if hashes, _ := info.body["webtransport_certificate_hashes"].([]any); len(hashes) != 1 {
		t.Fatalf("webtransport_certificate_hashes = %v", info.body["webtransport_certificate_hashes"])
	}
	if hashes, ok := info.body["http_certificate_hashes"].([]any); !ok || len(hashes) != 0 {
		t.Fatalf("http_certificate_hashes = %v", info.body["http_certificate_hashes"])
	}

	first := adminDo(t, http.MethodGet, url+"/stats", testToken, nil)
	time.Sleep(50 * time.Millisecond)
	second := adminDo(t, http.MethodGet, url+"/stats", testToken, nil)
	for _, key := range []string{"uptime_seconds", "sessions", "entities", "tick", "replication", "tasks"} {
		if _, ok := second.body[key]; !ok {
			t.Fatalf("/stats lacks %q: %s", key, second.raw)
		}
	}
	tick1 := first.body["tick"].(map[string]any)
	tick2 := second.body["tick"].(map[string]any)
	if tick2["tick"].(float64) <= tick1["tick"].(float64) {
		t.Fatalf("tick did not advance: %v -> %v", tick1["tick"], tick2["tick"])
	}
	for _, key := range []string{"rate", "budget_ms", "last_ms", "max_ms", "overruns"} {
		if _, ok := tick2[key]; !ok {
			t.Fatalf("/stats tick lacks %q: %s", key, second.raw)
		}
	}
	if tick2["budget_ms"] != float64(10) || second.body["sessions"] != float64(0) || second.body["entities"] != float64(0) {
		t.Fatalf("/stats = %s", second.raw)
	}

	if resp := adminDo(t, http.MethodPost, url+"/snapshot", testToken, nil); resp.status != http.StatusOK || resp.body["ok"] != true {
		t.Fatalf("/snapshot = %d %s", resp.status, resp.raw)
	}

	f.write(t, pairB.certPEM, pairB.keyPEM)
	resp := adminDo(t, http.MethodPost, url+"/reload-tls", testToken, nil)
	if resp.status != http.StatusOK || resp.body["webtransport"] != "reloaded" || resp.body["http"] != "unsupported" {
		t.Fatalf("/reload-tls = %d %s", resp.status, resp.raw)
	}
	if got := srv.WebTransportCertificateHashes(); len(got) != 1 || [32]byte(got[0].Value) != pairB.hash {
		t.Fatal("WebTransport certificate was not reloaded")
	}

	// Shutdown: while AfterServerStop runs, /live answers and /ready fails.
	h.cancel()
	select {
	case <-hookEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("AfterServerStop not called")
	}
	if resp := adminDo(t, http.MethodGet, url+"/live", "", nil); resp.status != http.StatusOK {
		t.Fatalf("/live during AfterServerStop = %d", resp.status)
	}
	ready := adminDo(t, http.MethodGet, url+"/ready", testToken, nil)
	if ready.status != http.StatusServiceUnavailable || ready.body["reason"] != "stopping" {
		t.Fatalf("/ready during AfterServerStop = %d %s", ready.status, ready.raw)
	}
	if resp := adminDo(t, http.MethodPost, url+"/snapshot", testToken, nil); resp.status != http.StatusServiceUnavailable {
		t.Fatalf("/snapshot during AfterServerStop = %d %s", resp.status, resp.raw)
	}
	close(hookRelease)
	if err := h.wait(t); err != nil {
		t.Fatalf("Run = %v", err)
	}
	client := &http.Client{Timeout: time.Second}
	if r, err := client.Get(url + "/live"); err == nil {
		r.Body.Close()
		t.Fatal("admin listener still serving after Run returned")
	}
}

func TestRunWaitsForAdminSnapshotBeforeAfterServerStop(t *testing.T) {
	srv := newTestServer()
	entered := make(chan struct{})
	var snapshotDone, hookSawDone atomic.Bool
	h := startRun(t, RunOptions{
		Server: srv,
		Admin: &AdminOptions{Token: testToken, Snapshot: func(ctx context.Context) error {
			close(entered)
			<-ctx.Done() // shutdown cancels it
			time.Sleep(30 * time.Millisecond)
			snapshotDone.Store(true)
			return ctx.Err()
		}},
		AfterServerStop: func() error {
			hookSawDone.Store(snapshotDone.Load())
			return nil
		},
	})
	waitFor(t, "ready", func() bool {
		return adminDo(t, http.MethodGet, h.adminURL+"/ready", testToken, nil).status == http.StatusOK
	})
	result := make(chan adminResponse, 1)
	go func() { result <- adminDo(t, http.MethodPost, h.adminURL+"/snapshot", testToken, nil) }()
	<-entered
	if err := h.stop(t); err != nil {
		t.Fatalf("Run = %v", err)
	}
	if !hookSawDone.Load() {
		t.Fatal("AfterServerStop ran before the admin snapshot returned")
	}
	if resp := <-result; resp.status != http.StatusServiceUnavailable || resp.body["reason"] != "stopping" {
		t.Fatalf("interrupted /snapshot = %d %s", resp.status, resp.raw)
	}
}

func TestAdminRejectsBrowserRequests(t *testing.T) {
	const token = "0123456789abcdef0123"
	withToken, _ := newTestAdmin(t, AdminOptions{Addr: "0.0.0.0:0", Token: token}, nil)
	withToken.ready.Store(true)
	open, _ := newTestAdmin(t, AdminOptions{Addr: "127.0.0.1:0"}, nil)
	open.ready.Store(true)

	serve := func(a *adminServer, host, origin, bearer string) int {
		req := httptest.NewRequest(http.MethodGet, "/stats", nil)
		req.Host = host
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		rec := httptest.NewRecorder()
		a.ServeHTTP(rec, req)
		return rec.Code
	}

	// A request carrying Origin comes from a web page, valid token or not.
	if got := serve(withToken, "203.0.113.5:9090", "https://evil.example", token); got != http.StatusForbidden {
		t.Fatalf("token + Origin = %d, want 403", got)
	}
	if got := serve(withToken, "203.0.113.5:9090", "", token); got != http.StatusOK {
		t.Fatalf("token without Origin = %d, want 200", got)
	}
	// Without a token only loopback Hosts are served, so a rebound DNS name fails.
	if got := serve(open, "rebound.evil.example:9090", "", ""); got != http.StatusForbidden {
		t.Fatalf("open admin with foreign Host = %d, want 403", got)
	}
	if got := serve(open, "localhost:9090", "http://localhost:5173", ""); got != http.StatusForbidden {
		t.Fatalf("open admin with Origin = %d, want 403", got)
	}
	for _, host := range []string{"127.0.0.1:9090", "localhost:9090", "[::1]:9090"} {
		if got := serve(open, host, "", ""); got != http.StatusOK {
			t.Fatalf("open admin with Host %q = %d, want 200", host, got)
		}
	}
}
