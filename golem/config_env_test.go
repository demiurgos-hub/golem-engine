package golem

import (
	"bytes"
	"log"
	"reflect"
	"strings"
	"testing"
	"time"
)

var allConfigEnvNames = []string{
	EnvAddr, EnvTLSCertFile, EnvTLSKeyFile, EnvTLSReloadInterval,
	EnvWebTransportAllowedOrigins, EnvTickRate,
	EnvPublicURL, EnvWebSocketFallbackURL, EnvTLSPinned,
}

// clearConfigEnv blanks every variable so the host environment cannot leak
// into a case; blank values count as unset.
func clearConfigEnv(t *testing.T) {
	t.Helper()
	for _, name := range allConfigEnvNames {
		t.Setenv(name, "")
	}
}

func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prevOut, prevFlags := log.Writer(), log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(prevOut)
		log.SetFlags(prevFlags)
	})
	return &buf
}

func TestServerConfigFromEnv(t *testing.T) {
	base := ServerConfig{
		TickRate:                   30,
		Addr:                       ":9000",
		TLSCertFile:                "base.crt",
		TLSKeyFile:                 "base.key",
		TLSReloadInterval:          time.Minute,
		WebTransportAllowedOrigins: []string{"https://base.example"},
		StaticDir:                  "static",
	}
	tests := []struct {
		name    string
		env     map[string]string
		want    func(ServerConfig) ServerConfig
		wantLog string
	}{
		{
			name: "unset leaves base",
			want: func(c ServerConfig) ServerConfig { return c },
		},
		{
			name: "blank values leave base",
			env: map[string]string{
				EnvAddr: "   ", EnvTLSCertFile: "\t", EnvTLSReloadInterval: " ",
				EnvWebTransportAllowedOrigins: " ", EnvTickRate: "  ",
			},
			want: func(c ServerConfig) ServerConfig { return c },
		},
		{
			name: "all fields overridden and trimmed",
			env: map[string]string{
				EnvAddr:                       " :4433 ",
				EnvTLSCertFile:                " /certs/fullchain.pem",
				EnvTLSKeyFile:                 "/certs/privkey.pem ",
				EnvTLSReloadInterval:          " 30s ",
				EnvWebTransportAllowedOrigins: " https://a.example , ,https://b.example:8443,",
				EnvTickRate:                   " 60 ",
			},
			want: func(c ServerConfig) ServerConfig {
				c.Addr = ":4433"
				c.TLSCertFile = "/certs/fullchain.pem"
				c.TLSKeyFile = "/certs/privkey.pem"
				c.TLSReloadInterval = 30 * time.Second
				c.WebTransportAllowedOrigins = []string{"https://a.example", "https://b.example:8443"}
				c.TickRate = 60
				return c
			},
		},
		{
			name: "zero reload interval disables",
			env:  map[string]string{EnvTLSReloadInterval: "0"},
			want: func(c ServerConfig) ServerConfig { c.TLSReloadInterval = 0; return c },
		},
		{
			name:    "malformed reload interval ignored",
			env:     map[string]string{EnvTLSReloadInterval: "30"},
			want:    func(c ServerConfig) ServerConfig { return c },
			wantLog: EnvTLSReloadInterval,
		},
		{
			name:    "negative reload interval ignored",
			env:     map[string]string{EnvTLSReloadInterval: "-5s"},
			want:    func(c ServerConfig) ServerConfig { return c },
			wantLog: EnvTLSReloadInterval,
		},
		{
			name:    "non-numeric tick rate ignored",
			env:     map[string]string{EnvTickRate: "fast"},
			want:    func(c ServerConfig) ServerConfig { return c },
			wantLog: EnvTickRate,
		},
		{
			name:    "zero tick rate ignored",
			env:     map[string]string{EnvTickRate: "0"},
			want:    func(c ServerConfig) ServerConfig { return c },
			wantLog: EnvTickRate,
		},
		{
			name:    "negative tick rate ignored",
			env:     map[string]string{EnvTickRate: "-20"},
			want:    func(c ServerConfig) ServerConfig { return c },
			wantLog: EnvTickRate,
		},
		{
			name: "origins list of separators replaces with empty",
			env:  map[string]string{EnvWebTransportAllowedOrigins: " , ,"},
			want: func(c ServerConfig) ServerConfig { c.WebTransportAllowedOrigins = nil; return c },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearConfigEnv(t)
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			logs := captureLog(t)
			baseCopy := base
			baseCopy.WebTransportAllowedOrigins = append([]string(nil), base.WebTransportAllowedOrigins...)

			got := ServerConfigFromEnv(baseCopy)
			want := tt.want(base)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("ServerConfigFromEnv =\n%+v\nwant\n%+v", got, want)
			}
			if !reflect.DeepEqual(baseCopy.WebTransportAllowedOrigins, base.WebTransportAllowedOrigins) {
				t.Fatalf("base origins mutated: %v", baseCopy.WebTransportAllowedOrigins)
			}
			if tt.wantLog == "" && logs.Len() != 0 {
				t.Fatalf("unexpected log output: %q", logs.String())
			}
			if tt.wantLog != "" && !strings.Contains(logs.String(), tt.wantLog) {
				t.Fatalf("log output %q does not name %s", logs.String(), tt.wantLog)
			}
		})
	}
}

func TestRealtimeConfigOptionsFromEnv(t *testing.T) {
	yes, no := true, false
	ack := 50
	base := RealtimeConfigOptions{
		PublicURL:             "https://base.example/wt",
		WebSocketFallbackURL:  "wss://base.example/ws",
		EventualAckIntervalMs: &ack,
	}
	tests := []struct {
		name    string
		base    RealtimeConfigOptions
		env     map[string]string
		want    RealtimeConfigOptions
		wantLog string
	}{
		{name: "unset leaves base", base: base, want: base},
		{
			name: "urls overridden and trimmed",
			base: base,
			env: map[string]string{
				EnvPublicURL:            " https://game.example:4433/wt ",
				EnvWebSocketFallbackURL: " wss://game.example/ws",
			},
			want: RealtimeConfigOptions{
				PublicURL:             "https://game.example:4433/wt",
				WebSocketFallbackURL:  "wss://game.example/ws",
				EventualAckIntervalMs: &ack,
			},
		},
		{name: "pinned 1", base: base, env: map[string]string{EnvTLSPinned: "1"}, want: withPinned(base, &yes)},
		{name: "pinned TRUE", base: base, env: map[string]string{EnvTLSPinned: "TRUE"}, want: withPinned(base, &yes)},
		{name: "pinned Yes", base: base, env: map[string]string{EnvTLSPinned: " Yes "}, want: withPinned(base, &yes)},
		{name: "pinned on", base: base, env: map[string]string{EnvTLSPinned: "on"}, want: withPinned(base, &yes)},
		{name: "unpinned 0", base: withPinned(base, &yes), env: map[string]string{EnvTLSPinned: "0"}, want: withPinned(base, &no)},
		{name: "unpinned False", base: base, env: map[string]string{EnvTLSPinned: "False"}, want: withPinned(base, &no)},
		{name: "unpinned no", base: base, env: map[string]string{EnvTLSPinned: "no"}, want: withPinned(base, &no)},
		{name: "unpinned OFF", base: base, env: map[string]string{EnvTLSPinned: "OFF"}, want: withPinned(base, &no)},
		{name: "pinned blank unchanged", base: withPinned(base, &yes), env: map[string]string{EnvTLSPinned: "  "}, want: withPinned(base, &yes)},
		{
			name:    "pinned malformed ignored",
			base:    withPinned(base, &no),
			env:     map[string]string{EnvTLSPinned: "maybe"},
			want:    withPinned(base, &no),
			wantLog: EnvTLSPinned,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearConfigEnv(t)
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			logs := captureLog(t)
			got := RealtimeConfigOptionsFromEnv(tt.base)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("RealtimeConfigOptionsFromEnv = %+v (pinned=%v), want %+v (pinned=%v)",
					got, derefBool(got.IncludeServerCertificateHashes), tt.want, derefBool(tt.want.IncludeServerCertificateHashes))
			}
			if tt.wantLog == "" && logs.Len() != 0 {
				t.Fatalf("unexpected log output: %q", logs.String())
			}
			if tt.wantLog != "" && !strings.Contains(logs.String(), tt.wantLog) {
				t.Fatalf("log output %q does not name %s", logs.String(), tt.wantLog)
			}
		})
	}
}

func withPinned(opts RealtimeConfigOptions, pinned *bool) RealtimeConfigOptions {
	opts.IncludeServerCertificateHashes = pinned
	return opts
}

func derefBool(b *bool) any {
	if b == nil {
		return nil
	}
	return *b
}
