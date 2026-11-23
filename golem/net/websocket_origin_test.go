package net

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/demiurgos-hub/golem-engine/golem/registry"
)

func websocketOriginRequest(host string, origin string) *http.Request {
	req, _ := http.NewRequest(http.MethodGet, "http://"+host+"/ws", nil)
	req.Host = host
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	return req
}

func TestWebSocketCheckOriginDefaultPolicy(t *testing.T) {
	allowList := []string{"https://play.example.com", "HTTPS://Admin.Example.com:8443", "not a url"}
	tests := []struct {
		name   string
		cfg    Config
		host   string
		origin string
		want   bool
	}{
		// No Origin header: native, non-browser clients.
		{name: "no origin", host: "game.example.com:8080", origin: "", want: true},

		// Same origin: Origin host:port equals Host, case-insensitively.
		{name: "same origin", host: "game.example.com:8080", origin: "https://game.example.com:8080", want: true},
		{name: "same origin http", host: "game.example.com:8080", origin: "http://game.example.com:8080", want: true},
		{name: "same origin default port", host: "game.example.com", origin: "https://game.example.com", want: true},
		{name: "same origin case-insensitive", host: "Game.Example.com:8080", origin: "https://game.EXAMPLE.com:8080", want: true},
		{name: "same hostname other port rejected", host: "game.example.com:8080", origin: "https://game.example.com:3000", want: false},

		// Exact allow-list entries.
		{name: "allow-list exact", cfg: Config{WebTransportAllowedOrigins: allowList}, host: "gs.example.com:8080", origin: "https://play.example.com", want: true},
		{name: "allow-list case-insensitive", cfg: Config{WebTransportAllowedOrigins: allowList}, host: "gs.example.com:8080", origin: "https://admin.example.COM:8443", want: true},
		{name: "allow-list wrong scheme", cfg: Config{WebTransportAllowedOrigins: allowList}, host: "gs.example.com:8080", origin: "http://play.example.com", want: false},
		{name: "allow-list wrong port", cfg: Config{WebTransportAllowedOrigins: allowList}, host: "gs.example.com:8080", origin: "https://admin.example.com", want: false},
		{name: "allow-list unlisted", cfg: Config{WebTransportAllowedOrigins: allowList}, host: "gs.example.com:8080", origin: "https://evil.example", want: false},

		// Same-host mode: any scheme, any port.
		{name: "same-host https other port", cfg: Config{WebTransportAllowSameHostOrigin: true}, host: "game.example.com:8080", origin: "https://game.example.com:3000", want: true},
		{name: "same-host http other port", cfg: Config{WebTransportAllowSameHostOrigin: true}, host: "game.example.com:8080", origin: "http://game.example.com", want: true},
		{name: "same-host different hostname", cfg: Config{WebTransportAllowSameHostOrigin: true}, host: "game.example.com:8080", origin: "https://evil.example.com:8080", want: false},
		{name: "same-host disabled", host: "game.example.com:8080", origin: "http://game.example.com", want: false},

		// Loopback on both sides: local dev servers on another port.
		{name: "loopback localhost", host: "localhost:8080", origin: "http://localhost:5173", want: true},
		{name: "loopback mixed names", host: "127.0.0.1:8080", origin: "http://localhost:5173", want: true},
		{name: "loopback ipv6", host: "[::1]:8080", origin: "http://[::1]:5173", want: true},
		{name: "loopback ipv6 no port", host: "[::1]", origin: "http://127.0.0.1:5173", want: true},
		{name: "loopback 127/8", host: "127.0.0.2:8080", origin: "http://127.0.0.1:5173", want: true},
		{name: "loopback origin to public host", host: "game.example.com:8080", origin: "http://localhost:5173", want: false},
		{name: "public origin to loopback host", host: "localhost:8080", origin: "https://evil.example", want: false},
		{name: "dev cert does not widen policy", cfg: Config{DevSelfSignedCert: true}, host: "192.168.1.10:8080", origin: "http://192.168.1.10:5173", want: false},

		// Everything else.
		{name: "cross site", host: "game.example.com:8080", origin: "https://evil.example", want: false},
		{name: "null origin", host: "game.example.com:8080", origin: "null", want: false},
		{name: "malformed origin", host: "game.example.com:8080", origin: "http://%zz", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checkOrigin := newWebSocketCheckOrigin(tt.cfg)
			if got := checkOrigin(websocketOriginRequest(tt.host, tt.origin)); got != tt.want {
				t.Fatalf("checkOrigin(host=%q, origin=%q) = %v, want %v", tt.host, tt.origin, got, tt.want)
			}
		})
	}
}

func TestSetWebSocketCheckOriginOverridesAndRestores(t *testing.T) {
	listener := NewListener(registry.NewRegistry(), Config{})
	req := websocketOriginRequest("game.example.com:8080", "https://evil.example")
	if listener.checkWebSocketOrigin(req) {
		t.Fatal("default policy accepted a cross-site origin")
	}
	listener.SetWebSocketCheckOrigin(func(*http.Request) bool { return true })
	if !listener.checkWebSocketOrigin(req) {
		t.Fatal("custom policy was not used")
	}
	listener.SetWebSocketCheckOrigin(nil)
	if listener.checkWebSocketOrigin(req) {
		t.Fatal("nil did not restore the config policy")
	}
}

func TestWebSocketUpgradeEnforcesOriginPolicy(t *testing.T) {
	listener := NewListener(registry.NewRegistry(), Config{
		WebTransportAllowedOrigins: []string{"https://play.example.com"},
	})
	var upgrades atomic.Int32
	listener.OnUpgrade(func(*http.Request) (any, error) {
		upgrades.Add(1)
		return nil, nil
	})
	httpSrv := httptest.NewServer(listener.WebSocketHandler())
	defer httpSrv.Close()
	defer listener.CloseSessions()
	wsURL := "ws" + strings.TrimPrefix(httpSrv.URL, "http")

	dial := func(origin string) (*websocket.Conn, *http.Response, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		opts := &websocket.DialOptions{HTTPHeader: http.Header{}}
		if origin != "" {
			opts.HTTPHeader.Set("Origin", origin)
		}
		return websocket.Dial(ctx, wsURL, opts)
	}

	conn, resp, err := dial("https://evil.example")
	if err == nil {
		conn.Close(websocket.StatusNormalClosure, "")
		t.Fatal("cross-site origin upgrade succeeded")
	}
	if resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-site origin response = %v, want 403 (err=%v)", resp, err)
	}
	if n := upgrades.Load(); n != 0 {
		t.Fatalf("OnUpgrade ran %d times for a rejected origin", n)
	}

	for _, origin := range []string{"https://play.example.com", ""} {
		conn, _, err = dial(origin)
		if err != nil {
			t.Fatalf("origin %q upgrade failed: %v", origin, err)
		}
		conn.Close(websocket.StatusNormalClosure, "")
	}
	if n := upgrades.Load(); n != 2 {
		t.Fatalf("OnUpgrade ran %d times, want 2", n)
	}

	// The test server listens on loopback, so a loopback page origin is
	// accepted without configuration.
	conn, _, err = dial("http://localhost:5173")
	if err != nil {
		t.Fatalf("loopback origin upgrade failed: %v", err)
	}
	conn.Close(websocket.StatusNormalClosure, "")
}
