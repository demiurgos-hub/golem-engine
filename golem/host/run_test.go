package host

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/demiurgos-hub/golem-engine/golem"
)

// stoppedServerProbe checks, from inside AfterServerStop, that Server.Run has
// returned: Post is refused and the tick counter no longer advances.
func stoppedServerProbe(t *testing.T, srv *golem.Server) error {
	if err := srv.Post(func(*golem.Server) {}); !errors.Is(err, golem.ErrServerNotRunning) {
		t.Errorf("Post inside AfterServerStop = %v, want ErrServerNotRunning", err)
	}
	before := srv.TickStats().Tick
	time.Sleep(50 * time.Millisecond) // five tick intervals at 100 Hz
	if after := srv.TickStats().Tick; after != before {
		t.Errorf("tick advanced inside AfterServerStop: %d -> %d", before, after)
	}
	return nil
}

func TestRunAfterServerStopOnCancelSavesFinalSnapshot(t *testing.T) {
	srv := newTestServer()
	path := filepath.Join(t.TempDir(), "final.snapshot")
	var calls atomic.Int32
	h := startRun(t, RunOptions{
		Server: srv,
		AfterServerStop: func() error {
			calls.Add(1)
			_ = stoppedServerProbe(t, srv)
			return <-srv.SaveSnapshot("test-fingerprint", path)
		},
	})
	waitFor(t, "ticks", func() bool { return srv.TickStats().Tick > 3 })
	if err := h.stop(t); err != nil {
		t.Fatalf("Run = %v, want nil after cancellation", err)
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("AfterServerStop called %d times, want 1", n)
	}
	if info, err := os.Stat(path); err != nil || info.Size() == 0 {
		t.Fatalf("final snapshot not written: %v", err)
	}
}

func TestRunAfterServerStopErrorIsJoined(t *testing.T) {
	hookErr := errors.New("final snapshot failed")
	h := startRun(t, RunOptions{
		Server:          newTestServer(),
		AfterServerStop: func() error { return hookErr },
	})
	err := h.stop(t)
	if !errors.Is(err, hookErr) || !strings.Contains(err.Error(), "golem/host: after server stop") {
		t.Fatalf("Run = %v, want the joined AfterServerStop error", err)
	}
}

func TestRunAfterServerStopOnSidecarFailure(t *testing.T) {
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()

	srv := newTestServer()
	var calls atomic.Int32
	h := startRun(t, RunOptions{
		Server:     srv,
		HTTPServer: &http.Server{Addr: busy.Addr().String(), Handler: http.NotFoundHandler()},
		AfterServerStop: func() error {
			calls.Add(1)
			return stoppedServerProbe(t, srv)
		},
	})
	err = h.wait(t)
	if err == nil || !strings.HasPrefix(err.Error(), "golem/host: http: ") {
		t.Fatalf("Run = %v, want the sidecar listen error", err)
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("AfterServerStop called %d times, want 1", n)
	}
}

func TestRunWaitsForSidecarShutdown(t *testing.T) {
	addr := freeAddr(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	var handlerDone atomic.Bool
	sidecar := &http.Server{Addr: addr, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/slow" {
			close(entered)
			<-release
			handlerDone.Store(true)
		}
		_, _ = io.WriteString(w, "ok")
	})}
	var hookCalled atomic.Bool
	h := startRun(t, RunOptions{
		Server:          newTestServer(),
		HTTPServer:      sidecar,
		ShutdownTimeout: 5 * time.Second,
		AfterServerStop: func() error { hookCalled.Store(true); return nil },
	})
	waitFor(t, "sidecar", func() bool {
		resp, err := http.Get("http://" + addr + "/")
		if err == nil {
			resp.Body.Close()
		}
		return err == nil
	})
	slowErr := make(chan error, 1)
	go func() {
		resp, err := http.Get("http://" + addr + "/slow")
		if err == nil {
			resp.Body.Close()
		}
		slowErr <- err
	}()
	<-entered

	h.cancel()
	select {
	case err := <-h.done:
		t.Fatalf("Run returned (%v) while a sidecar request was in flight", err)
	case <-time.After(150 * time.Millisecond):
	}
	if !hookCalled.Load() {
		t.Fatal("AfterServerStop must not wait for the sidecar's shutdown")
	}
	close(release)
	if err := h.wait(t); err != nil {
		t.Fatalf("Run = %v", err)
	}
	if !handlerDone.Load() {
		t.Fatal("Run returned before the in-flight sidecar request finished")
	}
	if err := <-slowErr; err != nil {
		t.Fatalf("in-flight request failed: %v", err)
	}
}

func TestRunSidecarShutdownTimeoutIsReported(t *testing.T) {
	addr := freeAddr(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	sidecar := &http.Server{Addr: addr, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/hang" {
			close(entered)
			select {
			case <-release:
			case <-r.Context().Done():
			}
		}
	})}
	h := startRun(t, RunOptions{Server: newTestServer(), HTTPServer: sidecar, ShutdownTimeout: 50 * time.Millisecond})
	waitFor(t, "sidecar", func() bool {
		conn, err := net.Dial("tcp", addr)
		if err == nil {
			conn.Close()
		}
		return err == nil
	})
	go func() {
		resp, err := http.Get("http://" + addr + "/hang")
		if err == nil {
			resp.Body.Close()
		}
	}()
	<-entered
	err := h.stop(t)
	if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "golem/host: http shutdown") {
		t.Fatalf("Run = %v, want the sidecar shutdown timeout", err)
	}
}

func TestRunWithoutServerSkipsAfterServerStop(t *testing.T) {
	var called atomic.Bool
	h := startRun(t, RunOptions{
		HTTPServer:      &http.Server{Addr: freeAddr(t), Handler: http.NotFoundHandler()},
		AfterServerStop: func() error { called.Store(true); return nil },
	})
	time.Sleep(20 * time.Millisecond)
	if err := h.stop(t); err != nil {
		t.Fatalf("Run = %v", err)
	}
	if called.Load() {
		t.Fatal("AfterServerStop called without a Server")
	}
}

func TestRunValidationFailureDoesNotStartServer(t *testing.T) {
	srv := newTestServer()
	var called atomic.Bool
	err := Run(context.Background(), RunOptions{
		Server:          srv,
		HTTPServer:      &http.Server{Addr: freeAddr(t)},
		HTTPTLSCertFile: "cert.pem",
		AfterServerStop: func() error { called.Store(true); return nil },
	})
	if err == nil {
		t.Fatal("Run accepted an incomplete TLS pair")
	}
	if called.Load() {
		t.Fatal("AfterServerStop called although Server.Run never started")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := srv.Run(ctx); errors.Is(err, golem.ErrServerAlreadyRun) {
		t.Fatal("Run started the server before validating options")
	}
}

type sidecarTLSFixture struct {
	certPath, keyPath string
	mtime             time.Time
}

func newSidecarTLSFixture(t *testing.T, pair testCertPair) *sidecarTLSFixture {
	dir := t.TempDir()
	f := &sidecarTLSFixture{
		certPath: filepath.Join(dir, "cert.pem"),
		keyPath:  filepath.Join(dir, "key.pem"),
		mtime:    time.Now().Add(-time.Hour).Truncate(time.Second),
	}
	f.write(t, pair.certPEM, pair.keyPEM)
	return f
}

func (f *sidecarTLSFixture) write(t *testing.T, certPEM, keyPEM []byte) {
	f.mtime = f.mtime.Add(time.Minute)
	writeFileAt(t, f.certPath, certPEM, f.mtime)
	writeFileAt(t, f.keyPath, keyPEM, f.mtime)
}

func waitServedHash(t *testing.T, addr string, want [32]byte, label string) {
	t.Helper()
	var last [32]byte
	var lastErr error
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		last, lastErr = peerLeafHash(addr)
		if lastErr == nil && last == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("%s: served %x (err %v), want %x", label, last, lastErr, want)
}

func TestRunSidecarCertificatePollReloads(t *testing.T) {
	pairA, pairB := newTestCertPair(t), newTestCertPair(t)
	f := newSidecarTLSFixture(t, pairA)
	addr := freeAddr(t)
	interval := 10 * time.Millisecond
	startRun(t, RunOptions{
		HTTPServer:            &http.Server{Addr: addr, Handler: http.NotFoundHandler()},
		HTTPTLSCertFile:       f.certPath,
		HTTPTLSKeyFile:        f.keyPath,
		HTTPTLSReloadInterval: interval,
	})
	waitServedHash(t, addr, pairA.hash, "initial")

	f.write(t, pairB.certPEM, pairB.keyPEM)
	waitServedHash(t, addr, pairB.hash, "after poll")

	// A corrupt pair keeps B.
	f.write(t, []byte("not a certificate"), pairA.keyPEM)
	time.Sleep(10 * interval)
	got, err := peerLeafHash(addr)
	if err != nil || got != pairB.hash {
		t.Fatalf("after corrupt pair served %x (err %v), want B %x", got, err, pairB.hash)
	}
}

func TestRunSidecarKeepsCallerTLSConfig(t *testing.T) {
	pair := newTestCertPair(t)
	f := newSidecarTLSFixture(t, pair)
	addr := freeAddr(t)
	callerCfg := &tls.Config{MinVersion: tls.VersionTLS13}
	sidecar := &http.Server{Addr: addr, Handler: http.NotFoundHandler(), TLSConfig: callerCfg}
	startRun(t, RunOptions{HTTPServer: sidecar, HTTPTLSCertFile: f.certPath, HTTPTLSKeyFile: f.keyPath})
	waitServedHash(t, addr, pair.hash, "initial")
	if callerCfg.GetCertificate != nil {
		t.Fatal("Run modified the caller's tls.Config instead of a clone")
	}
	conn, err := tls.Dial("tcp", addr, &tls.Config{InsecureSkipVerify: true, MaxVersion: tls.VersionTLS12}) //nolint:gosec // test
	if err == nil {
		conn.Close()
		t.Fatal("caller MinVersion TLS 1.3 was not preserved")
	}
}

func TestRunSidecarCallerOwnedCertificates(t *testing.T) {
	filePair, callerPair := newTestCertPair(t), newTestCertPair(t)
	f := newSidecarTLSFixture(t, filePair)
	callerCert, err := tls.X509KeyPair(callerPair.certPEM, callerPair.keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	addr := freeAddr(t)
	sidecar := &http.Server{Addr: addr, Handler: http.NotFoundHandler(), TLSConfig: &tls.Config{
		GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) { return &callerCert, nil },
	}}
	h := startRun(t, RunOptions{
		Server:                newTestServer(),
		HTTPServer:            sidecar,
		HTTPTLSCertFile:       f.certPath,
		HTTPTLSKeyFile:        f.keyPath,
		HTTPTLSReloadInterval: 10 * time.Millisecond,
		Admin:                 &AdminOptions{Token: testToken},
	})
	// Unchanged net/http behaviour: ListenAndServeTLS puts the file pair in
	// Certificates, and crypto/tls consults the caller's GetCertificate only
	// when the client sends SNI.
	waitServedHash(t, addr, filePair.hash, "files without SNI")
	if got, err := peerLeafHashSNI(addr, "localhost"); err != nil || got != callerPair.hash {
		t.Fatalf("with SNI served %x (err %v), want the caller's certificate", got, err)
	}
	resp := adminDo(t, http.MethodPost, h.adminURL+"/reload-tls", testToken, nil)
	if resp.status != http.StatusOK || resp.body["http"] != "unsupported" {
		t.Fatalf("/reload-tls = %d %s, want http unsupported", resp.status, resp.raw)
	}
}

func TestRunSidecarRejectsUnloadableCertificateBeforeStarting(t *testing.T) {
	f := newSidecarTLSFixture(t, newTestCertPair(t))
	if err := os.WriteFile(f.certPath, []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv := newTestServer()
	err := Run(context.Background(), RunOptions{
		Server:          srv,
		HTTPServer:      &http.Server{Addr: freeAddr(t)},
		HTTPTLSCertFile: f.certPath,
		HTTPTLSKeyFile:  f.keyPath,
	})
	if err == nil || !strings.HasPrefix(err.Error(), "golem/host: http: ") {
		t.Fatalf("Run = %v, want a certificate load error", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := srv.Run(ctx); errors.Is(err, golem.ErrServerAlreadyRun) {
		t.Fatal("Run started the server before loading the sidecar certificate")
	}
}

func TestRunSidecarAdminReloadTLS(t *testing.T) {
	pairA, pairB := newTestCertPair(t), newTestCertPair(t)
	f := newSidecarTLSFixture(t, pairA)
	addr := freeAddr(t)
	h := startRun(t, RunOptions{
		Server:          newTestServer(),
		HTTPServer:      &http.Server{Addr: addr, Handler: http.NotFoundHandler()},
		HTTPTLSCertFile: f.certPath,
		HTTPTLSKeyFile:  f.keyPath,
		Admin:           &AdminOptions{Token: testToken},
	})
	waitServedHash(t, addr, pairA.hash, "initial")
	f.write(t, pairB.certPEM, pairB.keyPEM)
	resp := adminDo(t, http.MethodPost, h.adminURL+"/reload-tls", testToken, nil)
	if resp.status != http.StatusOK || resp.body["http"] != "reloaded" || resp.body["webtransport"] != "unsupported" || resp.body["ok"] != true {
		t.Fatalf("/reload-tls = %d %s", resp.status, resp.raw)
	}
	got, err := peerLeafHash(addr)
	if err != nil || got != pairB.hash {
		t.Fatalf("after /reload-tls served %x (err %v), want B", got, err)
	}

	f.write(t, bytes.Repeat([]byte("x"), 10), pairB.keyPEM)
	resp = adminDo(t, http.MethodPost, h.adminURL+"/reload-tls", testToken, nil)
	if resp.status != http.StatusInternalServerError || resp.body["http"] != "failed" || resp.body["ok"] != false {
		t.Fatalf("/reload-tls with a corrupt pair = %d %s", resp.status, resp.raw)
	}
	if got, err := peerLeafHash(addr); err != nil || got != pairB.hash {
		t.Fatalf("failed reload replaced B: %x (err %v)", got, err)
	}
}
