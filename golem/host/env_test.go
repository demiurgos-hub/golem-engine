package host

import (
	"bytes"
	"log"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/demiurgos-hub/golem-engine/golem"
)

func clearHostEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{EnvAdminAddr, EnvAdminToken, EnvAdminProfiling, EnvHTTPAddr, golem.EnvTLSCertFile, golem.EnvTLSKeyFile, golem.EnvTLSReloadInterval} {
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

func TestAdminOptionsFromEnv(t *testing.T) {
	base := AdminOptions{Addr: "127.0.0.1:1", Token: "base-token-0123456789", Info: AdminInfo{Name: "game"}}
	tests := []struct {
		name    string
		env     map[string]string
		want    AdminOptions
		wantLog string
	}{
		{name: "unset keeps base", want: base},
		{name: "blank keeps base", env: map[string]string{EnvAdminAddr: "  ", EnvAdminToken: " "}, want: base},
		{name: "overrides", env: map[string]string{EnvAdminAddr: " :9090 ", EnvAdminToken: " env-token-0123456789 "},
			want: AdminOptions{Addr: ":9090", Token: "env-token-0123456789", Info: AdminInfo{Name: "game"}}},
		{name: "short token passes through for Run to reject", env: map[string]string{EnvAdminToken: "short"},
			want: AdminOptions{Addr: "127.0.0.1:1", Token: "short", Info: AdminInfo{Name: "game"}}},
		{name: "profiling on", env: map[string]string{EnvAdminProfiling: " ON "},
			want: AdminOptions{Addr: "127.0.0.1:1", Token: "base-token-0123456789", Info: AdminInfo{Name: "game"}, Profiling: true}},
		{name: "malformed profiling ignored", env: map[string]string{EnvAdminProfiling: "maybe"}, want: base, wantLog: EnvAdminProfiling},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearHostEnv(t)
			logs := captureLog(t)
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			got := AdminOptionsFromEnv(base)
			if got.Addr != tt.want.Addr || got.Token != tt.want.Token || got.Info.Name != tt.want.Info.Name || got.Profiling != tt.want.Profiling {
				t.Fatalf("AdminOptionsFromEnv = %+v, want %+v", got, tt.want)
			}
			if tt.wantLog != "" && !strings.Contains(logs.String(), tt.wantLog) {
				t.Fatalf("log %q does not name %s", logs.String(), tt.wantLog)
			}
			if strings.Contains(logs.String(), "token-0123456789") || strings.Contains(logs.String(), "short") {
				t.Fatalf("token logged: %q", logs.String())
			}
		})
	}
}

func TestAdminProfilingFromEnvTurnsOff(t *testing.T) {
	clearHostEnv(t)
	t.Setenv(EnvAdminProfiling, "false")
	if AdminOptionsFromEnv(AdminOptions{Profiling: true}).Profiling {
		t.Fatalf("%s=false kept profiling on", EnvAdminProfiling)
	}
}

func TestRunOptionsFromEnv(t *testing.T) {
	type result struct {
		httpAddr         string
		cert, key        string
		interval         time.Duration
		adminNil         bool
		adminAddr, token string
		profiling        bool
	}
	tests := []struct {
		name      string
		env       map[string]string
		noHTTP    bool
		baseAdmin *AdminOptions
		want      result
		wantLog   string
	}{
		{name: "unset keeps base", want: result{httpAddr: ":80", cert: "base.crt", key: "base.key", interval: time.Minute, adminNil: true}},
		{name: "http addr and shared TLS variables", env: map[string]string{
			EnvHTTPAddr: ":8080", golem.EnvTLSCertFile: "/tls/cert.pem", golem.EnvTLSKeyFile: "/tls/key.pem", golem.EnvTLSReloadInterval: "30s",
		}, want: result{httpAddr: ":8080", cert: "/tls/cert.pem", key: "/tls/key.pem", interval: 30 * time.Second, adminNil: true}},
		{name: "zero interval disables", env: map[string]string{golem.EnvTLSReloadInterval: "0"},
			want: result{httpAddr: ":80", cert: "base.crt", key: "base.key", adminNil: true}},
		{name: "malformed interval ignored", env: map[string]string{golem.EnvTLSReloadInterval: "soon"},
			want: result{httpAddr: ":80", cert: "base.crt", key: "base.key", interval: time.Minute, adminNil: true}, wantLog: golem.EnvTLSReloadInterval},
		{name: "negative interval ignored", env: map[string]string{golem.EnvTLSReloadInterval: "-1s"},
			want: result{httpAddr: ":80", cert: "base.crt", key: "base.key", interval: time.Minute, adminNil: true}, wantLog: golem.EnvTLSReloadInterval},
		{name: "http addr without HTTPServer ignored", noHTTP: true, env: map[string]string{EnvHTTPAddr: ":8080"},
			want: result{cert: "base.crt", key: "base.key", interval: time.Minute, adminNil: true}},
		{name: "admin addr creates admin", env: map[string]string{EnvAdminAddr: ":9090", EnvAdminToken: "env-token-0123456789"},
			want: result{httpAddr: ":80", cert: "base.crt", key: "base.key", interval: time.Minute, adminAddr: ":9090", token: "env-token-0123456789"}},
		{name: "token alone does not create admin", env: map[string]string{EnvAdminToken: "env-token-0123456789"},
			want: result{httpAddr: ":80", cert: "base.crt", key: "base.key", interval: time.Minute, adminNil: true}},
		{name: "token overlays base admin", baseAdmin: &AdminOptions{Addr: "127.0.0.1:9", Token: "base-token-0123456789"}, env: map[string]string{EnvAdminToken: "env-token-0123456789"},
			want: result{httpAddr: ":80", cert: "base.crt", key: "base.key", interval: time.Minute, adminAddr: "127.0.0.1:9", token: "env-token-0123456789"}},
		{name: "profiling overlays base admin", baseAdmin: &AdminOptions{Addr: "127.0.0.1:9"}, env: map[string]string{EnvAdminProfiling: "1"},
			want: result{httpAddr: ":80", cert: "base.crt", key: "base.key", interval: time.Minute, adminAddr: "127.0.0.1:9", profiling: true}},
		{name: "profiling alone does not create admin", env: map[string]string{EnvAdminProfiling: "1"},
			want: result{httpAddr: ":80", cert: "base.crt", key: "base.key", interval: time.Minute, adminNil: true}},
		{name: "base admin kept", baseAdmin: &AdminOptions{Addr: "127.0.0.1:9"},
			want: result{httpAddr: ":80", cert: "base.crt", key: "base.key", interval: time.Minute, adminAddr: "127.0.0.1:9"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearHostEnv(t)
			logs := captureLog(t)
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			base := RunOptions{HTTPTLSCertFile: "base.crt", HTTPTLSKeyFile: "base.key", HTTPTLSReloadInterval: time.Minute, Admin: tt.baseAdmin}
			if !tt.noHTTP {
				base.HTTPServer = &http.Server{Addr: ":80"}
			}
			var baseAdminCopy AdminOptions
			if tt.baseAdmin != nil {
				baseAdminCopy = *tt.baseAdmin
			}
			got := RunOptionsFromEnv(base)
			var httpAddr string
			if got.HTTPServer != nil {
				httpAddr = got.HTTPServer.Addr
			}
			r := result{httpAddr: httpAddr, cert: got.HTTPTLSCertFile, key: got.HTTPTLSKeyFile, interval: got.HTTPTLSReloadInterval, adminNil: got.Admin == nil}
			if got.Admin != nil {
				r.adminAddr, r.token, r.profiling = got.Admin.Addr, got.Admin.Token, got.Admin.Profiling
			}
			if r != tt.want {
				t.Fatalf("RunOptionsFromEnv = %+v, want %+v", r, tt.want)
			}
			if tt.baseAdmin != nil && (tt.baseAdmin.Addr != baseAdminCopy.Addr || tt.baseAdmin.Token != baseAdminCopy.Token || tt.baseAdmin.Profiling != baseAdminCopy.Profiling) {
				t.Fatal("RunOptionsFromEnv modified base.Admin")
			}
			if tt.wantLog != "" && !strings.Contains(logs.String(), tt.wantLog) {
				t.Fatalf("log %q does not name %s", logs.String(), tt.wantLog)
			}
			if strings.Contains(logs.String(), "token-0123456789") {
				t.Fatalf("token logged: %q", logs.String())
			}
		})
	}
}
