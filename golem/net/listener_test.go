package net

import (
	"bytes"
	"crypto/sha256"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/quic-go/quic-go/http3"
	"golem-engine/golem/registry"
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
