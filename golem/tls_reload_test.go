package golem

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

func TestServerReloadTLSCertificateUnsupported(t *testing.T) {
	tests := []struct {
		name string
		cfg  ServerConfig
		want error
	}{
		{name: "no integrated listener", cfg: ServerConfig{TLSCertFile: "c.pem", TLSKeyFile: "k.pem"}, want: ErrTLSCertificateReloadUnsupported},
		{name: "websocket transport", cfg: ServerConfig{Addr: "127.0.0.1:0", Transport: TransportWebSocket, StateUpdateLane: StateUpdateLaneStream, TLSCertFile: "c.pem", TLSKeyFile: "k.pem"}, want: ErrTLSCertificateReloadUnsupported},
		{name: "dev self-signed", cfg: ServerConfig{Addr: "127.0.0.1:0", DevSelfSignedCert: true}, want: ErrTLSCertificateReloadUnsupported},
		{name: "before run", cfg: ServerConfig{Addr: "127.0.0.1:0", TLSCertFile: "c.pem", TLSKeyFile: "k.pem"}, want: ErrTLSCertificateNotLoaded},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := NewServer(tt.cfg)
			if err := srv.ReloadTLSCertificate(); !errors.Is(err, tt.want) {
				t.Fatalf("ReloadTLSCertificate error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestServerReloadTLSCertificatePublishesNewHashes(t *testing.T) {
	certPath, keyPath := writeTestCertificateFiles(t)
	nextCertPath, nextKeyPath := writeTestCertificateFiles(t)
	srv := NewServer(ServerConfig{
		Addr:        "127.0.0.1:0",
		Transport:   TransportWebTransport,
		TLSCertFile: certPath,
		TLSKeyFile:  keyPath,
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runErr := make(chan error, 1)
	go func() { runErr <- srv.Run(ctx) }()
	defer func() {
		cancel()
		select {
		case <-runErr:
		case <-time.After(5 * time.Second):
			t.Error("Run did not return")
		}
	}()
	if err := srv.WaitReady(ctx); err != nil {
		t.Fatalf("WaitReady: %v", err)
	}

	pinned := true
	handler := srv.RealtimeConfigHandler(RealtimeConfigOptions{
		PublicURL:                      "https://example.com:4433/wt",
		IncludeServerCertificateHashes: &pinned,
	})
	if got, want := servedRealtimeHash(t, handler), pemLeafHashHex(t, certPath); got != want {
		t.Fatalf("initial published hash = %s, want %s", got, want)
	}

	copyFile(t, nextCertPath, certPath)
	copyFile(t, nextKeyPath, keyPath)
	if err := srv.ReloadTLSCertificate(); err != nil {
		t.Fatalf("ReloadTLSCertificate: %v", err)
	}
	if got, want := servedRealtimeHash(t, handler), pemLeafHashHex(t, nextCertPath); got != want {
		t.Fatalf("published hash after reload = %s, want %s", got, want)
	}
}

func servedRealtimeHash(t *testing.T, handler http.Handler) string {
	t.Helper()
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/realtime-config", nil))
	var body struct {
		ServerCertificateHashes []struct {
			Value string `json:"value"`
		} `json:"serverCertificateHashes"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(body.ServerCertificateHashes) != 1 {
		t.Fatalf("hash count = %d, want 1", len(body.ServerCertificateHashes))
	}
	return body.ServerCertificateHashes[0].Value
}

func pemLeafHashHex(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	block, _ := pem.Decode(data)
	if block == nil {
		t.Fatalf("no PEM block in %s", path)
	}
	sum := sha256.Sum256(block.Bytes)
	return hex.EncodeToString(sum[:])
}

func copyFile(t *testing.T, from string, to string) {
	t.Helper()
	data, err := os.ReadFile(from)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if err := os.WriteFile(to, data, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}
