package host

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/demiurgos-hub/golem-engine/golem"
)

const testToken = "0123456789abcdef-test-token"

type testCertPair struct {
	certPEM []byte
	keyPEM  []byte
	hash    [32]byte // SHA-256 of the leaf DER
}

func newTestCertPair(t *testing.T) testCertPair {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 64))
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "golem-host-test"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &priv.PublicKey, priv)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	return testCertPair{
		certPEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		keyPEM:  pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}),
		hash:    sha256.Sum256(der),
	}
}

// writeFileAt rewrites path in place (no rename over an open file, which
// Windows refuses) and pins an explicit mtime so coarse timestamps cannot
// hide the change from a poll.
func writeFileAt(t *testing.T, path string, data []byte, mtime time.Time) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

// freeAddr returns a loopback TCP address that was free a moment ago.
func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}

// newTestServer returns a headless server (no integrated listener).
func newTestServer() *golem.Server {
	return golem.NewServer(golem.ServerConfig{TickRate: 100})
}

type runHandle struct {
	cancel   context.CancelFunc
	done     chan error
	adminURL string
}

// startRun starts Run in the background and, when opts.Admin is set, waits for
// the admin listener's address.
func startRun(t *testing.T, opts RunOptions) *runHandle {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	h := &runHandle{cancel: cancel, done: make(chan error, 1)}
	addrCh := make(chan string, 1)
	if opts.Admin != nil {
		admin := *opts.Admin
		if admin.Addr == "" {
			admin.Addr = "127.0.0.1:0"
		}
		admin.listening = func(a net.Addr) { addrCh <- a.String() }
		opts.Admin = &admin
	}
	if opts.Signals == nil {
		opts.Signals = []os.Signal{}
	}
	go func() { h.done <- Run(ctx, opts) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-h.done:
		case <-time.After(10 * time.Second):
			t.Error("Run did not return during cleanup")
		}
	})
	if opts.Admin != nil {
		select {
		case addr := <-addrCh:
			h.adminURL = "http://" + addr
		case err := <-h.done:
			t.Fatalf("Run returned before the admin listener started: %v", err)
		case <-time.After(5 * time.Second):
			t.Fatal("admin listener did not start")
		}
	}
	return h
}

// stop cancels Run and returns its result.
func (h *runHandle) stop(t *testing.T) error {
	t.Helper()
	h.cancel()
	return h.wait(t)
}

func (h *runHandle) wait(t *testing.T) error {
	t.Helper()
	select {
	case err := <-h.done:
		h.done <- err // keep it for cleanup
		return err
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return")
		return nil
	}
}

type adminResponse struct {
	status int
	header http.Header
	body   map[string]any
	raw    string
}

func adminDo(t *testing.T, method, url, token string, body io.Reader) adminResponse {
	t.Helper()
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	out := adminResponse{status: resp.StatusCode, header: resp.Header, raw: string(raw)}
	if err := json.Unmarshal(raw, &out.body); err != nil {
		t.Fatalf("%s %s: body is not a JSON object: %q", method, url, raw)
	}
	return out
}

// waitFor polls cond until it holds or the deadline passes.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// peerLeafHash dials addr over TLS and returns the served leaf's SHA-256.
func peerLeafHash(addr string) ([32]byte, error) {
	return peerLeafHashSNI(addr, "")
}

// peerLeafHashSNI is peerLeafHash sending serverName as SNI when non-empty.
func peerLeafHashSNI(addr string, serverName string) ([32]byte, error) {
	dialer := &net.Dialer{Timeout: time.Second}
	conn, err := tls.DialWithDialer(dialer, "tcp", addr, &tls.Config{InsecureSkipVerify: true, ServerName: serverName}) //nolint:gosec // test inspects the served certificate
	if err != nil {
		return [32]byte{}, err
	}
	defer conn.Close()
	state := conn.ConnectionState()
	if len(state.PeerCertificates) == 0 {
		return [32]byte{}, errors.New("no peer certificate")
	}
	return sha256.Sum256(state.PeerCertificates[0].Raw), nil
}

// jsonUnmarshalObject decodes a JSON object.
func jsonUnmarshalObject(data []byte, out *map[string]any) error {
	if err := json.Unmarshal(data, out); err != nil {
		return err
	}
	if *out == nil {
		return errors.New("not a JSON object")
	}
	return nil
}
