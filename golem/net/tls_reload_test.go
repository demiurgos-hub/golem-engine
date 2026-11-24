package net

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/demiurgos-hub/golem-engine/golem/registry"
	"github.com/quic-go/webtransport-go"
)

type testCertificatePair struct {
	certPEM []byte
	keyPEM  []byte
	hash    []byte // SHA-256 of the leaf DER
}

func newTestCertificatePair(t *testing.T) testCertificatePair {
	t.Helper()
	certPEM, keyPEM, hashes, err := generateSelfSignedCertificate("127.0.0.1:0")
	if err != nil {
		t.Fatalf("generateSelfSignedCertificate: %v", err)
	}
	return testCertificatePair{certPEM: certPEM, keyPEM: keyPEM, hash: hashes[0].Value}
}

// writeTestFile rewrites path in place (no rename, which Windows refuses over
// open files) and pins an explicit mtime so coarse filesystem timestamps
// cannot hide the change from the poll.
func writeTestFile(t *testing.T, path string, data []byte, mtime time.Time) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("WriteFile %s: %v", path, err)
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatalf("Chtimes %s: %v", path, err)
	}
}

func TestCertificateHolderSwapsCertificateAndHashesTogether(t *testing.T) {
	var holder certificateHolder
	if _, err := holder.getCertificate(nil); err == nil {
		t.Fatal("empty holder served a certificate")
	}
	if holder.hashes() != nil {
		t.Fatal("empty holder returned hashes")
	}

	states := make([]*servedCertificate, 2)
	for i := range states {
		pair := newTestCertificatePair(t)
		certificate, err := loadWebTransportCertificate(pair.certPEM, pair.keyPEM)
		if err != nil {
			t.Fatalf("loadWebTransportCertificate: %v", err)
		}
		states[i] = newServedCertificate(certificate)
		if !bytes.Equal(states[i].hashes[0].Value, pair.hash) {
			t.Fatalf("state %d hash = %x, want %x", i, states[i].hashes[0].Value, pair.hash)
		}
	}
	holder.store(states[0])

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	var wg sync.WaitGroup
	var mismatches atomic.Int32
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ctx.Err() == nil {
				state := holder.load()
				sum := sha256.Sum256(state.certificate.Certificate[0])
				if !bytes.Equal(sum[:], state.hashes[0].Value) {
					mismatches.Add(1)
				}
				served, err := holder.getCertificate(nil)
				if err != nil || served == nil {
					mismatches.Add(1)
				}
			}
		}()
	}
	for i := 0; ctx.Err() == nil; i++ {
		holder.store(states[i%2])
	}
	wg.Wait()
	if n := mismatches.Load(); n != 0 {
		t.Fatalf("%d reads saw a certificate without its own hashes", n)
	}

	holder.store(states[1])
	served, _ := holder.getCertificate(nil)
	if served != states[1].certificate {
		t.Fatal("getCertificate did not serve the stored certificate")
	}
	hashes := holder.hashes()
	hashes[0].Value[0] ^= 0xff
	if !bytes.Equal(holder.hashes()[0].Value, states[1].hashes[0].Value) {
		t.Fatal("hashes() returned shared memory")
	}
}

func TestListenerReloadCertificateUnsupported(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
		want error
	}{
		{name: "websocket", cfg: Config{Transport: TransportWebSocket, TLSCertFile: "c.pem", TLSKeyFile: "k.pem"}, want: ErrTLSCertificateReloadUnsupported},
		{name: "dev self-signed", cfg: Config{Transport: TransportWebTransport, DevSelfSignedCert: true}, want: ErrTLSCertificateReloadUnsupported},
		{name: "no files", cfg: Config{Transport: TransportWebTransport}, want: ErrTLSCertificateReloadUnsupported},
		{name: "cert only", cfg: Config{Transport: TransportWebTransport, TLSCertFile: "c.pem"}, want: ErrTLSCertificateReloadUnsupported},
		{name: "not serving yet", cfg: Config{Transport: TransportWebTransport, TLSCertFile: "c.pem", TLSKeyFile: "k.pem"}, want: ErrTLSCertificateNotLoaded},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			listener := NewListener(registry.NewRegistry(), tt.cfg)
			if err := listener.ReloadCertificate(); !errors.Is(err, tt.want) {
				t.Fatalf("ReloadCertificate error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestListenerDevSelfSignedCertificateIsNotReloadable(t *testing.T) {
	listener := NewListener(registry.NewRegistry(), Config{
		Addr:              "127.0.0.1:0",
		Transport:         TransportWebTransport,
		DevSelfSignedCert: true,
		TLSReloadInterval: 10 * time.Millisecond,
	})
	startIntegratedWebTransportListener(t, listener)
	if len(listener.CertificateHashes()) != 1 {
		t.Fatalf("dev certificate hashes = %v, want one", listener.CertificateHashes())
	}
	if err := listener.ReloadCertificate(); !errors.Is(err, ErrTLSCertificateReloadUnsupported) {
		t.Fatalf("ReloadCertificate error = %v, want ErrTLSCertificateReloadUnsupported", err)
	}
}

// startIntegratedWebTransportListener runs ListenAndServe (the code path that
// owns the reloadable certificate and the poll) and returns the endpoint URL.
func startIntegratedWebTransportListener(t *testing.T, listener *Listener) string {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- listener.ListenAndServe(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("ListenAndServe: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("ListenAndServe did not return after cancellation")
		}
	})
	readyCtx, readyCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer readyCancel()
	if err := listener.WaitReady(readyCtx); err != nil {
		t.Fatalf("WaitReady: %v", err)
	}
	addr, _ := listener.boundAddr.Load().(string)
	if addr == "" {
		t.Fatal("listener did not record its bound address")
	}
	return "https://" + addr + listener.config.Path
}

// dialCapturingLeaf dials a fresh WebTransport connection (no session cache,
// so always a full handshake) and returns the SHA-256 of the served leaf.
func dialCapturingLeaf(t *testing.T, url string) (*webtransport.Session, []byte) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var mu sync.Mutex
	var leafHash []byte
	dialer := &webtransport.Dialer{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true, // Test-only self-signed certificates.
			VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
				sum := sha256.Sum256(rawCerts[0])
				mu.Lock()
				leafHash = sum[:]
				mu.Unlock()
				return nil
			},
		},
	}
	t.Cleanup(func() { _ = dialer.Close() })
	_, session, err := dialer.Dial(ctx, url, nil)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { _ = session.CloseWithError(0, "") })
	mu.Lock()
	defer mu.Unlock()
	return session, leafHash
}

// connectedTestSession dials, primes the reliable stream and waits for the
// listener's OnConnect, returning the client stream and server session ID.
type connectedTestSession struct {
	stream   *webtransport.Stream
	leafHash []byte
	id       int64
}

func connectTestSession(t *testing.T, url string, connected <-chan int64) connectedTestSession {
	t.Helper()
	session, leafHash := dialCapturingLeaf(t, url)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stream, err := session.OpenStreamSync(ctx)
	if err != nil {
		t.Fatalf("OpenStreamSync: %v", err)
	}
	if _, err := stream.Write(nil); err != nil {
		t.Fatalf("prime stream: %v", err)
	}
	select {
	case id := <-connected:
		return connectedTestSession{stream: stream, leafHash: leafHash, id: id}
	case <-ctx.Done():
		t.Fatal("OnConnect not called")
	}
	return connectedTestSession{}
}

func assertSessionReceives(t *testing.T, listener *Listener, sess connectedTestSession, payload string) {
	t.Helper()
	if err := listener.Send(sess.id, []byte(payload)); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if err := sess.stream.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	got, err := readReliableFrame(sess.stream)
	if err != nil {
		t.Fatalf("read frame on existing session: %v", err)
	}
	if string(got) != payload {
		t.Fatalf("existing session frame = %q, want %q", got, payload)
	}
}

func assertServedHash(t *testing.T, listener *Listener, url string, want []byte, label string) {
	t.Helper()
	hashes := listener.CertificateHashes()
	if len(hashes) != 1 || !bytes.Equal(hashes[0].Value, want) {
		t.Fatalf("%s: CertificateHashes = %v, want %x", label, hashes, want)
	}
	_, leaf := dialCapturingLeaf(t, url)
	if !bytes.Equal(leaf, want) {
		t.Fatalf("%s: new connection served %x, want %x", label, leaf, want)
	}
}

type reloadFixture struct {
	listener  *Listener
	url       string
	certPath  string
	keyPath   string
	connected chan int64
	mtime     time.Time
}

func newReloadFixture(t *testing.T, initial testCertificatePair, interval time.Duration) *reloadFixture {
	t.Helper()
	dir := t.TempDir()
	f := &reloadFixture{
		certPath:  filepath.Join(dir, "cert.pem"),
		keyPath:   filepath.Join(dir, "key.pem"),
		connected: make(chan int64, 8),
		mtime:     time.Now().Add(-time.Hour).Truncate(time.Second),
	}
	f.write(t, f.certPath, initial.certPEM)
	f.write(t, f.keyPath, initial.keyPEM)
	f.listener = NewListener(registry.NewRegistry(), Config{
		Addr:              "127.0.0.1:0",
		Transport:         TransportWebTransport,
		TLSCertFile:       f.certPath,
		TLSKeyFile:        f.keyPath,
		TLSReloadInterval: interval,
	})
	f.listener.OnConnect(func(s *Session) { f.connected <- s.ID })
	f.url = startIntegratedWebTransportListener(t, f.listener)
	return f
}

// write replaces a file with a strictly increasing explicit mtime.
func (f *reloadFixture) write(t *testing.T, path string, data []byte) {
	t.Helper()
	f.mtime = f.mtime.Add(time.Minute)
	writeTestFile(t, path, data, f.mtime)
}

func TestListenerReloadCertificateServesNewCertificate(t *testing.T) {
	pairA, pairB := newTestCertificatePair(t), newTestCertificatePair(t)
	f := newReloadFixture(t, pairA, 0)

	existing := connectTestSession(t, f.url, f.connected)
	if !bytes.Equal(existing.leafHash, pairA.hash) {
		t.Fatalf("initial connection served %x, want A %x", existing.leafHash, pairA.hash)
	}
	assertServedHash(t, f.listener, f.url, pairA.hash, "before reload")

	f.write(t, f.certPath, pairB.certPEM)
	f.write(t, f.keyPath, pairB.keyPEM)
	if err := f.listener.ReloadCertificate(); err != nil {
		t.Fatalf("ReloadCertificate: %v", err)
	}
	assertServedHash(t, f.listener, f.url, pairB.hash, "after reload")
	assertSessionReceives(t, f.listener, existing, "still-alive")

	fresh := connectTestSession(t, f.url, f.connected)
	if !bytes.Equal(fresh.leafHash, pairB.hash) {
		t.Fatalf("session after reload served %x, want B %x", fresh.leafHash, pairB.hash)
	}
	assertSessionReceives(t, f.listener, fresh, "fresh")
}

func TestListenerReloadCertificateRejectsBadPairAndKeepsServing(t *testing.T) {
	pairA, pairB := newTestCertificatePair(t), newTestCertificatePair(t)
	f := newReloadFixture(t, pairA, 0)
	existing := connectTestSession(t, f.url, f.connected)

	cases := []struct {
		name    string
		certPEM []byte
		keyPEM  []byte
	}{
		{name: "corrupt certificate", certPEM: []byte("-----BEGIN CERTIFICATE-----\nnot base64\n"), keyPEM: pairA.keyPEM},
		{name: "mismatched pair", certPEM: pairB.certPEM, keyPEM: pairA.keyPEM},
		{name: "empty key", certPEM: pairA.certPEM, keyPEM: nil},
	}
	for _, tc := range cases {
		f.write(t, f.certPath, tc.certPEM)
		f.write(t, f.keyPath, tc.keyPEM)
		if err := f.listener.ReloadCertificate(); err == nil {
			t.Fatalf("%s: ReloadCertificate succeeded", tc.name)
		}
		assertServedHash(t, f.listener, f.url, pairA.hash, tc.name)
	}

	if err := os.Remove(f.keyPath); err != nil {
		t.Fatalf("Remove key: %v", err)
	}
	if err := f.listener.ReloadCertificate(); err == nil {
		t.Fatal("missing key: ReloadCertificate succeeded")
	}
	assertServedHash(t, f.listener, f.url, pairA.hash, "missing key")
	assertSessionReceives(t, f.listener, existing, "after-failures")
}

func TestListenerCertificatePollReloadsChangedFiles(t *testing.T) {
	pairA, pairB := newTestCertificatePair(t), newTestCertificatePair(t)
	const interval = 20 * time.Millisecond
	f := newReloadFixture(t, pairA, interval)
	existing := connectTestSession(t, f.url, f.connected)

	// Half-written pair: new certificate, old key. The poll must keep A.
	f.write(t, f.certPath, pairB.certPEM)
	time.Sleep(10 * interval)
	assertServedHash(t, f.listener, f.url, pairA.hash, "half-written pair")

	f.write(t, f.keyPath, pairB.keyPEM)
	waitForCertificateHash(t, f.listener, pairB.hash)
	assertServedHash(t, f.listener, f.url, pairB.hash, "after poll")
	assertSessionReceives(t, f.listener, existing, "after-poll")

	// Same size, same content, only a newer mtime: reloading again is harmless
	// and still serves B.
	f.write(t, f.certPath, pairB.certPEM)
	time.Sleep(5 * interval)
	assertServedHash(t, f.listener, f.url, pairB.hash, "touched")
}

func TestListenerCertificatePollStopsWithListener(t *testing.T) {
	pairA, pairB := newTestCertificatePair(t), newTestCertificatePair(t)
	const interval = 10 * time.Millisecond
	dir := t.TempDir()
	certPath, keyPath := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	mtime := time.Now().Add(-time.Hour)
	writeTestFile(t, certPath, pairA.certPEM, mtime)
	writeTestFile(t, keyPath, pairA.keyPEM, mtime)
	listener := NewListener(registry.NewRegistry(), Config{
		Addr:              "127.0.0.1:0",
		Transport:         TransportWebTransport,
		TLSCertFile:       certPath,
		TLSKeyFile:        keyPath,
		TLSReloadInterval: interval,
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- listener.ListenAndServe(ctx) }()
	readyCtx, readyCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer readyCancel()
	if err := listener.WaitReady(readyCtx); err != nil {
		t.Fatalf("WaitReady: %v", err)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("ListenAndServe: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ListenAndServe did not return")
	}

	// ListenAndServe waits for the poll goroutine before returning, so a file
	// change afterwards must never be picked up.
	writeTestFile(t, certPath, pairB.certPEM, mtime.Add(time.Minute))
	writeTestFile(t, keyPath, pairB.keyPEM, mtime.Add(time.Minute))
	time.Sleep(10 * interval)
	if hashes := listener.CertificateHashes(); len(hashes) != 1 || !bytes.Equal(hashes[0].Value, pairA.hash) {
		t.Fatalf("certificate changed after shutdown: %v", hashes)
	}
}

func waitForCertificateHash(t *testing.T, listener *Listener, want []byte) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if hashes := listener.CertificateHashes(); len(hashes) == 1 && bytes.Equal(hashes[0].Value, want) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("CertificateHashes never became %x (got %v)", want, listener.CertificateHashes())
}
