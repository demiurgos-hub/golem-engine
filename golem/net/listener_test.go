package net

import (
	"bytes"
	"context"
	"crypto/sha256"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/demiurgos-hub/golem-engine/golem/registry"
	"github.com/quic-go/quic-go/http3"
)

func TestPrepareWebTransportTLSWithDevSelfSignedCert(t *testing.T) {
	certificate, hashes, cleanup, err := prepareWebTransportTLS(Config{
		Addr:              "localhost:4433",
		DevSelfSignedCert: true,
	})
	if err != nil {
		t.Fatalf("prepareWebTransportTLS: %v", err)
	}
	defer cleanup()

	if len(certificate.Certificate) == 0 {
		t.Fatal("prepareWebTransportTLS returned no certificates")
	}
	if certificate.Leaf == nil {
		t.Fatal("prepareWebTransportTLS did not cache the leaf certificate")
	}
	if len(hashes) != 1 {
		t.Fatalf("hash count = %d, want 1", len(hashes))
	}

	sum := sha256.Sum256(certificate.Certificate[0])
	if !bytes.Equal(hashes[0].Value, sum[:]) {
		t.Fatalf("hash mismatch: got %x want %x", hashes[0].Value, sum)
	}
}

func TestPrepareWebTransportTLSWithCertificateFiles(t *testing.T) {
	certPEM, keyPEM, wantHashes, err := generateSelfSignedCertificate("localhost:4433")
	if err != nil {
		t.Fatalf("generateSelfSignedCertificate: %v", err)
	}

	dir := t.TempDir()
	certPath := filepath.Join(dir, "cert.pem")
	keyPath := filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certPath, certPEM, 0o600); err != nil {
		t.Fatalf("WriteFile cert: %v", err)
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		t.Fatalf("WriteFile key: %v", err)
	}

	certificate, hashes, cleanup, err := prepareWebTransportTLS(Config{
		TLSCertFile: certPath,
		TLSKeyFile:  keyPath,
	})
	if err != nil {
		t.Fatalf("prepareWebTransportTLS: %v", err)
	}
	defer cleanup()

	if len(certificate.Certificate) == 0 {
		t.Fatal("prepareWebTransportTLS returned no certificates")
	}
	if certificate.Leaf == nil {
		t.Fatal("prepareWebTransportTLS did not cache the leaf certificate")
	}
	if len(hashes) != len(wantHashes) {
		t.Fatalf("hash count = %d, want %d", len(hashes), len(wantHashes))
	}
	for i := range hashes {
		if hashes[i].Algorithm != wantHashes[i].Algorithm {
			t.Fatalf("hash %d algorithm = %q, want %q", i, hashes[i].Algorithm, wantHashes[i].Algorithm)
		}
		if !bytes.Equal(hashes[i].Value, wantHashes[i].Value) {
			t.Fatalf("hash %d value = %x, want %x", i, hashes[i].Value, wantHashes[i].Value)
		}
	}
}

func TestNewWebTransportServerTLSConfigIncludesH3ALPN(t *testing.T) {
	certificate, _, cleanup, err := prepareWebTransportTLS(Config{
		Addr:              "localhost:4433",
		DevSelfSignedCert: true,
	})
	if err != nil {
		t.Fatalf("prepareWebTransportTLS: %v", err)
	}
	defer cleanup()

	tlsConfig := newWebTransportServerTLSConfig(certificate)
	if tlsConfig == nil {
		t.Fatal("newWebTransportServerTLSConfig returned nil")
	}
	if len(tlsConfig.Certificates) != 1 {
		t.Fatalf("certificate count = %d, want 1", len(tlsConfig.Certificates))
	}

	foundH3 := false
	for _, proto := range tlsConfig.NextProtos {
		if proto == http3.NextProtoH3 {
			foundH3 = true
			break
		}
	}
	if !foundH3 {
		t.Fatalf("NextProtos = %v, want %q", tlsConfig.NextProtos, http3.NextProtoH3)
	}
}

func TestListenerWebTransportServerConfiguresProvidedHTTP3Server(t *testing.T) {
	listener := NewListener(registry.NewRegistry(), Config{Transport: TransportWebTransport})
	_ = listener.Handler()

	h3 := &http3.Server{}
	server := listener.WebTransportServer(h3)
	if server == nil {
		t.Fatal("WebTransportServer returned nil")
	}
	if server.H3 != h3 {
		t.Fatal("WebTransportServer did not reuse the provided http3.Server")
	}
	if !h3.EnableDatagrams {
		t.Fatal("WebTransportServer did not enable datagrams")
	}
	if h3.ConnContext == nil {
		t.Fatal("WebTransportServer did not configure ConnContext")
	}
	if len(h3.AdditionalSettings) == 0 {
		t.Fatal("WebTransportServer did not configure HTTP/3 settings")
	}
}

func TestListenerWebSocketHandlerAvailableForWebTransportPrimary(t *testing.T) {
	listener := NewListener(registry.NewRegistry(), Config{Transport: TransportWebTransport})
	connected := make(chan *Session, 1)
	listener.OnConnect(func(sess *Session) {
		connected <- sess
	})

	httpServer := httptest.NewServer(listener.WebSocketHandler())
	defer httpServer.Close()
	client, _, err := websocket.Dial(context.Background(), "ws"+httpServer.URL[4:], nil)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer client.CloseNow()

	select {
	case sess := <-connected:
		if sess.Transport != TransportWebSocket {
			t.Fatalf("session transport = %q, want %q", sess.Transport, TransportWebSocket)
		}
		if got, ok := listener.SessionTransport(sess.ID); !ok || got != TransportWebSocket {
			t.Fatalf("SessionTransport = %q, %v; want %q, true", got, ok, TransportWebSocket)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for WebSocket fallback session")
	}
}

func TestListenerWebSocketHandlerClearsHTTPServerDeadlines(t *testing.T) {
	listener := NewListener(registry.NewRegistry(), Config{Transport: TransportWebTransport})
	received := make(chan []byte, 1)
	listener.OnMessage(func(_ *Session, data []byte) {
		received <- append([]byte(nil), data...)
	})
	tcpListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	httpServer := &http.Server{
		Handler:      listener.WebSocketHandler(),
		ReadTimeout:  40 * time.Millisecond,
		WriteTimeout: 40 * time.Millisecond,
	}
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- httpServer.Serve(tcpListener)
	}()
	t.Cleanup(func() {
		_ = httpServer.Close()
		<-serveDone
	})

	client, _, err := websocket.Dial(
		context.Background(),
		"ws://"+tcpListener.Addr().String(),
		nil,
	)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer client.CloseNow()

	time.Sleep(120 * time.Millisecond)
	writeCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	want := []byte("still connected")
	if err := client.Write(writeCtx, websocket.MessageBinary, want); err != nil {
		t.Fatalf("Write after HTTP deadlines elapsed: %v", err)
	}
	select {
	case got := <-received:
		if !bytes.Equal(got, want) {
			t.Fatalf("received %q, want %q", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("server did not receive message after HTTP deadlines elapsed")
	}
}

func TestListenerCloseSessionsFencesPendingSnapshotAndFutureUpgrades(t *testing.T) {
	listener := NewListener(registry.NewRegistry(), Config{Transport: TransportWebTransport})
	snapshotStarted := make(chan struct{})
	releaseSnapshot := make(chan struct{})
	listener.SetEntitySnapshotFunc(func(int64) ([]EntitySnapshot, error) {
		close(snapshotStarted)
		<-releaseSnapshot
		return nil, nil
	})

	httpServer := httptest.NewServer(listener.WebSocketHandler())
	defer httpServer.Close()
	client, _, err := websocket.Dial(context.Background(), "ws"+httpServer.URL[4:], nil)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer client.CloseNow()
	select {
	case <-snapshotStarted:
	case <-time.After(time.Second):
		t.Fatal("snapshot did not start")
	}

	closed := make(chan struct{})
	go func() {
		listener.CloseSessions()
		close(closed)
	}()
	close(releaseSnapshot)
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("CloseSessions did not wait for pending handler cleanup")
	}
	if ids := listener.SessionIDs(); len(ids) != 0 {
		t.Fatalf("SessionIDs after shutdown = %v, want empty", ids)
	}

	postShutdown, response, err := websocket.Dial(context.Background(), "ws"+httpServer.URL[4:], nil)
	if postShutdown != nil {
		postShutdown.CloseNow()
	}
	if err == nil {
		t.Fatal("post-shutdown WebSocket upgrade unexpectedly succeeded")
	}
	if response == nil || response.StatusCode != http.StatusServiceUnavailable {
		status := 0
		if response != nil {
			status = response.StatusCode
		}
		t.Fatalf("post-shutdown status = %d, want %d", status, http.StatusServiceUnavailable)
	}
}

func TestListenerCloseSessionsWaitsForPreAcceptHandler(t *testing.T) {
	listener := NewListener(registry.NewRegistry(), Config{Transport: TransportWebTransport})
	authorizationStarted := make(chan struct{})
	releaseAuthorization := make(chan struct{})
	listener.OnUpgrade(func(*http.Request) (any, error) {
		close(authorizationStarted)
		<-releaseAuthorization
		return nil, nil
	})

	httpServer := httptest.NewServer(listener.WebSocketHandler())
	defer httpServer.Close()
	requestDone := make(chan struct{})
	go func() {
		defer close(requestDone)
		response, err := http.Get(httpServer.URL)
		if err == nil {
			_ = response.Body.Close()
		}
	}()
	select {
	case <-authorizationStarted:
	case <-time.After(time.Second):
		t.Fatal("authorization did not start")
	}

	closed := make(chan struct{})
	go func() {
		listener.CloseSessions()
		close(closed)
	}()
	select {
	case <-closed:
		t.Fatal("CloseSessions returned while a pre-accept handler was still running")
	case <-time.After(50 * time.Millisecond):
	}

	close(releaseAuthorization)
	select {
	case <-requestDone:
	case <-time.After(time.Second):
		t.Fatal("pre-accept request did not finish")
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("CloseSessions did not finish after the pre-accept handler returned")
	}
}

func TestListenerCertificateHashesReturnsDeepCopy(t *testing.T) {
	listener := NewListener(registry.NewRegistry(), Config{Transport: TransportWebTransport})
	listener.certificateHashes = []CertificateHash{{
		Algorithm: "sha-256",
		Value:     []byte{1, 2, 3},
	}}

	first := listener.CertificateHashes()
	first[0].Algorithm = "changed"
	first[0].Value[0] = 99
	second := listener.CertificateHashes()
	if second[0].Algorithm != "sha-256" || !bytes.Equal(second[0].Value, []byte{1, 2, 3}) {
		t.Fatalf("CertificateHashes internal value mutated: %+v", second)
	}
}

func TestDefaultWebTransportCheckOriginRejectsCrossPortWithoutDevSelfSignedCert(t *testing.T) {
	checkOrigin := newWebTransportCheckOrigin(Config{})
	req := httptestOriginRequest("aerhen.prod-gs.fracturedmmo.com:4433", "https://aerhen.prod-gs.fracturedmmo.com:8080")
	if checkOrigin(req) {
		t.Fatal("expected cross-port production origin to be rejected without explicit config")
	}
}

func TestWebTransportCheckOriginAllowsConfiguredExactOrigin(t *testing.T) {
	listener := NewListener(registry.NewRegistry(), Config{
		Transport:                  TransportWebTransport,
		WebTransportAllowedOrigins: []string{"https://aerhen.prod-gs.fracturedmmo.com:8080"},
	})
	server := listener.WebTransportServer(&http3.Server{})
	if server == nil {
		t.Fatal("WebTransportServer returned nil")
	}

	req := httptestOriginRequest("aerhen.prod-gs.fracturedmmo.com:4433", "https://aerhen.prod-gs.fracturedmmo.com:8080")
	if !server.CheckOrigin(req) {
		t.Fatal("expected configured exact origin to allow cross-port request")
	}
}

func TestWebTransportCheckOriginSameHostModeAllowsCrossPortHTTPSOrigin(t *testing.T) {
	checkOrigin := newWebTransportCheckOrigin(Config{WebTransportAllowSameHostOrigin: true})
	req := httptestOriginRequest("game.example:4433", "https://game.example:8080")
	if !checkOrigin(req) {
		t.Fatal("expected same-host mode to allow cross-port HTTPS origin")
	}
}

func TestWebTransportCheckOriginSameHostModeRejectsDifferentHostname(t *testing.T) {
	checkOrigin := newWebTransportCheckOrigin(Config{WebTransportAllowSameHostOrigin: true})
	req := httptestOriginRequest("game.example:4433", "https://app.example:8080")
	if checkOrigin(req) {
		t.Fatal("expected same-host mode to reject a different origin hostname")
	}
}

func TestDefaultWebTransportCheckOriginAllowsLoopbackCrossPortWithDevSelfSignedCert(t *testing.T) {
	checkOrigin := newWebTransportCheckOrigin(Config{DevSelfSignedCert: true})
	req := httptestOriginRequest("localhost:4433", "http://localhost:8080")
	if !checkOrigin(req) {
		t.Fatal("expected cross-port localhost origin to be allowed with DevSelfSignedCert")
	}
}

func TestDefaultWebTransportCheckOriginRejectsNonLoopbackCrossOrigin(t *testing.T) {
	checkOrigin := newWebTransportCheckOrigin(Config{DevSelfSignedCert: true})
	req := httptestOriginRequest("game.example:4433", "https://app.example:8080")
	if checkOrigin(req) {
		t.Fatal("expected non-loopback cross-origin request to be rejected")
	}
}

func TestListenerSetWebTransportCheckOriginOverridesDefault(t *testing.T) {
	listener := NewListener(registry.NewRegistry(), Config{
		Transport:                  TransportWebTransport,
		WebTransportAllowedOrigins: []string{"https://configured.example"},
	})
	called := false
	listener.SetWebTransportCheckOrigin(func(r *http.Request) bool {
		called = true
		return equalASCIIFold(r.Header.Get("Origin"), "https://custom.example")
	})

	server := listener.WebTransportServer(&http3.Server{})
	if server == nil {
		t.Fatal("WebTransportServer returned nil")
	}
	req := httptestOriginRequest("localhost:4433", "https://custom.example")
	if !server.CheckOrigin(req) {
		t.Fatal("expected custom origin checker to allow request")
	}
	if !called {
		t.Fatal("expected custom origin checker to run")
	}
}

func httptestOriginRequest(host string, origin string) *http.Request {
	req, _ := http.NewRequest(http.MethodConnect, "https://"+host+"/wt", nil)
	req.Host = host
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	return req
}
