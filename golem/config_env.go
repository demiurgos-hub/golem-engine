package golem

import (
	"log"
	"os"
	"strconv"
	"strings"
	"time"
)

// Environment variables read by ServerConfigFromEnv.
const (
	// EnvAddr overrides ServerConfig.Addr, e.g. ":4433".
	EnvAddr = "GOLEM_ADDR"
	// EnvTLSCertFile overrides ServerConfig.TLSCertFile.
	EnvTLSCertFile = "GOLEM_TLS_CERT_FILE"
	// EnvTLSKeyFile overrides ServerConfig.TLSKeyFile.
	EnvTLSKeyFile = "GOLEM_TLS_KEY_FILE"
	// EnvTLSReloadInterval overrides ServerConfig.TLSReloadInterval using Go
	// duration syntax, e.g. "30s"; "0" disables polling.
	EnvTLSReloadInterval = "GOLEM_TLS_RELOAD_INTERVAL"
	// EnvWebTransportAllowedOrigins replaces
	// ServerConfig.WebTransportAllowedOrigins with a comma-separated list.
	EnvWebTransportAllowedOrigins = "GOLEM_WT_ALLOWED_ORIGINS"
	// EnvTickRate overrides ServerConfig.TickRate with a positive integer.
	EnvTickRate = "GOLEM_TICK_RATE"
)

// Environment variables read by RealtimeConfigOptionsFromEnv.
const (
	// EnvPublicURL overrides RealtimeConfigOptions.PublicURL.
	EnvPublicURL = "GOLEM_PUBLIC_URL"
	// EnvWebSocketFallbackURL overrides RealtimeConfigOptions.WebSocketFallbackURL.
	EnvWebSocketFallbackURL = "GOLEM_WS_FALLBACK_URL"
	// EnvTLSPinned overrides RealtimeConfigOptions.IncludeServerCertificateHashes:
	// 1/true/yes/on publishes the served certificate's hashes for
	// serverCertificateHashes pinning, 0/false/no/off suppresses them.
	EnvTLSPinned = "GOLEM_TLS_PINNED"
)

// ServerConfigFromEnv returns base with fields overridden by the GOLEM_ADDR,
// GOLEM_TLS_CERT_FILE, GOLEM_TLS_KEY_FILE, GOLEM_TLS_RELOAD_INTERVAL,
// GOLEM_WT_ALLOWED_ORIGINS and GOLEM_TICK_RATE environment variables. A
// variable that is unset or blank after trimming leaves its field unchanged.
// GOLEM_WT_ALLOWED_ORIGINS is comma-separated (entries trimmed, empty entries
// dropped) and replaces the base list. A malformed value (a duration that is
// not valid Go syntax or is negative, or a tick rate that is not a positive
// integer) is logged with its variable name and ignored. It never fails.
func ServerConfigFromEnv(base ServerConfig) ServerConfig {
	cfg := base
	if v, ok := lookupEnvTrimmed(EnvAddr); ok {
		cfg.Addr = v
	}
	if v, ok := lookupEnvTrimmed(EnvTLSCertFile); ok {
		cfg.TLSCertFile = v
	}
	if v, ok := lookupEnvTrimmed(EnvTLSKeyFile); ok {
		cfg.TLSKeyFile = v
	}
	if v, ok := lookupEnvTrimmed(EnvTLSReloadInterval); ok {
		d, err := time.ParseDuration(v)
		switch {
		case err != nil:
			logIgnoredEnv(EnvTLSReloadInterval, v, "want a Go duration such as 30s, or 0 to disable")
		case d < 0:
			logIgnoredEnv(EnvTLSReloadInterval, v, "duration must not be negative")
		default:
			cfg.TLSReloadInterval = d
		}
	}
	if v, ok := lookupEnvTrimmed(EnvWebTransportAllowedOrigins); ok {
		cfg.WebTransportAllowedOrigins = splitEnvList(v)
	}
	if v, ok := lookupEnvTrimmed(EnvTickRate); ok {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.TickRate = n
		} else {
			logIgnoredEnv(EnvTickRate, v, "want a positive integer")
		}
	}
	return cfg
}

// RealtimeConfigOptionsFromEnv returns base with fields overridden by the
// GOLEM_PUBLIC_URL, GOLEM_WS_FALLBACK_URL and GOLEM_TLS_PINNED environment
// variables. A variable that is unset or blank after trimming leaves its field
// unchanged. GOLEM_TLS_PINNED accepts 1/true/yes/on (publish certificate
// hashes) or 0/false/no/off (omit them), case-insensitively; it lets a deploy
// tool serve a short-lived self-signed certificate from files and still have
// browsers pin it through serverCertificateHashes. Other values are logged and
// ignored. It never fails.
func RealtimeConfigOptionsFromEnv(base RealtimeConfigOptions) RealtimeConfigOptions {
	opts := base
	if v, ok := lookupEnvTrimmed(EnvPublicURL); ok {
		opts.PublicURL = v
	}
	if v, ok := lookupEnvTrimmed(EnvWebSocketFallbackURL); ok {
		opts.WebSocketFallbackURL = v
	}
	if v, ok := lookupEnvTrimmed(EnvTLSPinned); ok {
		switch strings.ToLower(v) {
		case "1", "true", "yes", "on":
			pinned := true
			opts.IncludeServerCertificateHashes = &pinned
		case "0", "false", "no", "off":
			pinned := false
			opts.IncludeServerCertificateHashes = &pinned
		default:
			logIgnoredEnv(EnvTLSPinned, v, "want 1/true/yes/on or 0/false/no/off")
		}
	}
	return opts
}

// lookupEnvTrimmed returns the trimmed value of name and whether it is
// non-empty.
func lookupEnvTrimmed(name string) (string, bool) {
	v := strings.TrimSpace(os.Getenv(name))
	return v, v != ""
}

func splitEnvList(v string) []string {
	var out []string
	for _, part := range strings.Split(v, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

func logIgnoredEnv(name string, value string, reason string) {
	log.Printf("golem: ignoring %s=%q: %s", name, value, reason)
}
