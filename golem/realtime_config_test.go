package golem

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRealtimeConfigHandlerServesDevSelfSignedHashes(t *testing.T) {
	srv := NewServer(ServerConfig{
		Addr:              "127.0.0.1:0",
		Transport:         TransportWebTransport,
		DevSelfSignedCert: true,
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runErr := make(chan error, 1)
	go func() {
		runErr <- srv.Run(ctx)
	}()
	if err := srv.WaitReady(ctx); err != nil {
		t.Fatalf("WaitReady: %v", err)
	}
	ackInterval := 50

	req := httptest.NewRequest(http.MethodGet, "/api/realtime-config", nil)
	rec := httptest.NewRecorder()
	srv.RealtimeConfigHandler(RealtimeConfigOptions{
		PublicURL:             "https://localhost:4433/wt",
		EventualAckIntervalMs: &ackInterval,
	}).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("content type = %q, want application/json", got)
	}

	var body struct {
		Transport               string `json:"transport"`
		URL                     string `json:"url"`
		ServerCertificateHashes []struct {
			Algorithm string `json:"algorithm"`
			Value     string `json:"value"`
		} `json:"serverCertificateHashes"`
		EventualAckIntervalMs *int `json:"eventualAckIntervalMs"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if body.Transport != "webtransport" {
		t.Fatalf("transport = %q, want webtransport", body.Transport)
	}
	if body.URL != "https://localhost:4433/wt" {
		t.Fatalf("url = %q", body.URL)
	}
	if len(body.ServerCertificateHashes) != 1 {
		t.Fatalf("hash count = %d, want 1", len(body.ServerCertificateHashes))
	}
	if body.ServerCertificateHashes[0].Algorithm != "sha-256" || body.ServerCertificateHashes[0].Value == "" {
		t.Fatalf("hash = %+v", body.ServerCertificateHashes[0])
	}
	if body.EventualAckIntervalMs == nil || *body.EventualAckIntervalMs != ackInterval {
		t.Fatalf("eventualAckIntervalMs = %v, want %d", body.EventualAckIntervalMs, ackInterval)
	}
	cancel()
	<-runErr
}

func TestRealtimeConfigHandlerOmitsHashesForTLSCertificateFiles(t *testing.T) {
	certPath, keyPath := writeTestCertificateFiles(t)
	srv := NewServer(ServerConfig{
		Addr:        "127.0.0.1:0",
		Transport:   TransportWebTransport,
		TLSCertFile: certPath,
		TLSKeyFile:  keyPath,
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runErr := make(chan error, 1)
	go func() {
		runErr <- srv.Run(ctx)
	}()
	if err := srv.WaitReady(ctx); err != nil {
		t.Fatalf("WaitReady: %v", err)
	}
	if hashes := srv.WebTransportCertificateHashes(); len(hashes) == 0 {
		t.Fatal("expected listener to compute certificate hashes for TLS files")
	}

	req := httptest.NewRequest(http.MethodGet, "/api/realtime-config", nil)
	rec := httptest.NewRecorder()
	srv.RealtimeConfigHandler(RealtimeConfigOptions{
		PublicURL: "https://example.com:4433/wt",
	}).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var body map[string]json.RawMessage
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if _, ok := body["serverCertificateHashes"]; ok {
		t.Fatalf("serverCertificateHashes present for TLS certificate files: %s", body["serverCertificateHashes"])
	}
	cancel()
	<-runErr
}

func writeTestCertificateFiles(t *testing.T) (string, string) {
	t.Helper()

	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	serialLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		t.Fatalf("serial: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName: "localhost",
		},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{"localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("CreateCertificate: %v", err)
	}
	keyBytes, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		t.Fatalf("MarshalECPrivateKey: %v", err)
	}

	dir := t.TempDir()
	certPath := filepath.Join(dir, "cert.pem")
	keyPath := filepath.Join(dir, "key.pem")
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyBytes})
	if err := os.WriteFile(certPath, certPEM, 0o600); err != nil {
		t.Fatalf("WriteFile cert: %v", err)
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		t.Fatalf("WriteFile key: %v", err)
	}
	return certPath, keyPath
}
