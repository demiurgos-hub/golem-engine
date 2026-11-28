package host

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/demiurgos-hub/golem-engine/golem"
	golemclient "github.com/demiurgos-hub/golem-engine/golem-go-client"
)

func TestLocalDevelopmentConnectsOnBothTransports(t *testing.T) {
	clearGameEnv(t)
	t.Setenv(golem.EnvAddr, "")
	t.Setenv(EnvAdminAddr, "127.0.0.1:0")
	udp, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	gameAddr := udp.LocalAddr().String()
	udp.Close()
	sideAddr := freeAddr(t)
	cfg, opts := LocalDevelopment(golem.ServerConfig{Addr: gameAddr, Path: "/realtime"}, GameOptions{
		HTTPAddr: sideAddr, WebSocketPath: "/fallback",
	})
	srv := golem.NewServer(cfg)
	connected := make(chan golem.Transport, 2)
	srv.OnConnect(func(session *golem.Session) {
		session.Transport = "" // The legacy field does not determine the negotiated kind.
		connected <- session.TransportKind()
	})
	g := startGame(t, srv, GameBindings{}, opts)
	waitReady(t, g.adminURL)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	rt, err := golemclient.FetchRealtimeConfig(ctx, "http://"+sideAddr+DefaultRealtimeConfigPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rt.URL != "https://"+gameAddr+"/realtime" || len(rt.ServerCertificateHashes) != 1 ||
		rt.Fallback == nil || rt.Fallback.URL != "ws://"+sideAddr+"/fallback" {
		t.Fatalf("unexpected realtime config: %+v", rt)
	}

	for _, endpoint := range []golemclient.ConnectOptions{
		{Transport: rt.Transport, URL: rt.URL, ServerCertificateHashes: rt.ServerCertificateHashes},
		{Transport: rt.Fallback.Transport, URL: rt.Fallback.URL},
	} {
		client := golemclient.NewGameClient(golemclient.GameClientOptions{})
		defer client.Disconnect()
		if err := client.Connect(ctx, endpoint); err != nil {
			t.Fatal(err)
		}
		select {
		case kind := <-connected:
			if kind != golem.Transport(endpoint.Transport) {
				t.Fatalf("session transport = %q, want %q", kind, endpoint.Transport)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
}

func TestLocalDevelopmentEnvironmentOverrides(t *testing.T) {
	clearGameEnv(t)
	t.Setenv(golem.EnvAddr, "[::1]:9443")
	t.Setenv(EnvHTTPAddr, "localhost:9080")
	t.Setenv(golem.EnvPublicURL, "https://game.example/wt")
	t.Setenv(golem.EnvWebSocketFallbackURL, "wss://game.example/ws")
	t.Setenv(golem.EnvTLSPinned, "false")

	cfg, opts := LocalDevelopment(golem.ServerConfig{Addr: "127.0.0.1:8080"}, GameOptions{HTTPAddr: "127.0.0.1:8080"})
	if !cfg.DevSelfSignedCert || opts.Realtime.PublicURL != "https://[::1]:9443/wt" ||
		opts.Realtime.WebSocketFallbackURL != "ws://localhost:9080/api/ws" {
		t.Fatalf("unexpected loopback defaults: %+v, %+v", cfg, opts.Realtime)
	}
	srv := golem.NewServer(cfg)
	g, err := newGame(srv, GameBindings{}, opts)
	if err != nil {
		t.Fatal(err)
	}
	if g.side.Addr != "localhost:9080" || g.realtime.PublicURL != "https://game.example/wt" ||
		g.realtime.WebSocketFallbackURL != "wss://game.example/ws" || srv.IncludesServerCertificateHashes(g.realtime) {
		t.Fatalf("unexpected environment overrides: %s, %+v", g.side.Addr, g.realtime)
	}
}

func TestLocalDevelopmentDoesNotEnableTLSOrAdvertiseNonlocalEndpoints(t *testing.T) {
	for _, tc := range []struct {
		name, gameAddr, sideAddr, certFile, wantStartErr string
	}{
		{name: "nonloopback game", gameAddr: ":9443", sideAddr: "localhost:9080", wantStartErr: "requires TLS certificates"},
		{name: "nonloopback sidecar", gameAddr: "localhost:9443", sideAddr: "0.0.0.0:9080", wantStartErr: "requires TLS certificates"},
		{name: "TLS file", gameAddr: "localhost:9443", sideAddr: "localhost:9080", certFile: "cert.pem"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clearGameEnv(t)
			t.Setenv(golem.EnvAddr, tc.gameAddr)
			t.Setenv(EnvHTTPAddr, tc.sideAddr)
			t.Setenv(golem.EnvTLSCertFile, tc.certFile)
			cfg, opts := LocalDevelopment(golem.ServerConfig{Addr: "localhost:9443", DevSelfSignedCert: true}, GameOptions{})
			if cfg.DevSelfSignedCert || cfg.TLSCertFile != tc.certFile ||
				opts.Realtime.PublicURL != "" || opts.Realtime.WebSocketFallbackURL != "" {
				t.Fatalf("unexpected development defaults: %+v, %+v", cfg, opts.Realtime)
			}
			if tc.wantStartErr != "" {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if err := RunGame(ctx, golem.NewServer(cfg), GameBindings{}, opts); err == nil || !strings.Contains(err.Error(), tc.wantStartErr) {
					t.Fatalf("RunGame error = %v, want %q", err, tc.wantStartErr)
				}
			}
		})
	}
}

func TestLocalDevelopmentWebSocketPrimary(t *testing.T) {
	clearGameEnv(t)
	t.Setenv(golem.EnvAddr, "")
	cfg, opts := LocalDevelopment(golem.ServerConfig{Transport: golem.TransportWebSocket}, GameOptions{HTTPAddr: "localhost:9080"})
	if cfg.DevSelfSignedCert || opts.Realtime.PublicURL != "ws://localhost:9080/api/ws" || opts.Realtime.WebSocketFallbackURL != "" {
		t.Fatalf("unexpected WebSocket defaults: %+v, %+v", cfg, opts.Realtime)
	}
}
