package host

import (
	"net"
	"net/url"
	"strconv"

	"github.com/demiurgos-hub/golem-engine/golem"
)

// LocalDevelopment prepares the server config and host options before constructing
// the server. It applies ServerConfigFromEnv, and fills in missing realtime URLs
// and enables the in-memory WebTransport certificate when the configured game
// and sidecar addresses are loopback and neither TLS file is set. A WebSocket
// primary advertises the sidecar if present. RunGame still applies environment
// overrides to the host options, including the advertised URLs and TLS pinning.
func LocalDevelopment(cfg golem.ServerConfig, opts GameOptions) (golem.ServerConfig, GameOptions) {
	cfg = golem.ServerConfigFromEnv(cfg)
	sidecarAddr := opts.sidecarAddr()
	gameLoopback, _ := isLoopbackAddr(cfg.Addr)
	sidecarLoopback, _ := isLoopbackAddr(sidecarAddr)
	webTransport := cfg.Transport == "" || cfg.Transport == golem.TransportWebTransport
	local := (cfg.Addr == "" || gameLoopback) && (sidecarAddr == "" || sidecarLoopback) &&
		cfg.TLSCertFile == "" && cfg.TLSKeyFile == ""
	cfg.DevSelfSignedCert = local && webTransport && gameLoopback
	if !local {
		return cfg, opts
	}

	switch {
	case webTransport && gameLoopback:
		path := cfg.Path
		if path == "" {
			path = "/wt"
		}
		if opts.Realtime.PublicURL == "" {
			opts.Realtime.PublicURL = localRealtimeURL("https", cfg.Addr, path)
		}
		if opts.Realtime.WebSocketFallbackURL == "" {
			opts.Realtime.WebSocketFallbackURL = localRealtimeURL("ws", sidecarAddr, opts.webSocketPath())
		}
	case cfg.Transport == golem.TransportWebSocket && opts.Realtime.PublicURL == "":
		if sidecarLoopback {
			opts.Realtime.PublicURL = localRealtimeURL("ws", sidecarAddr, opts.webSocketPath())
		} else if gameLoopback {
			path := cfg.Path
			if path == "" {
				path = "/ws"
			}
			opts.Realtime.PublicURL = localRealtimeURL("ws", cfg.Addr, path)
		}
	}
	return cfg, opts
}

// A listen port of zero is assigned at bind time and cannot be advertised here.
func localRealtimeURL(scheme, addr, path string) string {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return ""
	}
	n, err := strconv.ParseUint(port, 10, 16)
	if err != nil || n == 0 {
		return ""
	}
	return (&url.URL{Scheme: scheme, Host: addr, Path: path}).String()
}
