package host

import (
	"log"
	"os"
	"strings"
	"time"

	"github.com/demiurgos-hub/golem-engine/golem"
)

// Environment variables read by AdminOptionsFromEnv and RunOptionsFromEnv.
// The sidecar's TLS pair and reload interval reuse golem.EnvTLSCertFile,
// golem.EnvTLSKeyFile and golem.EnvTLSReloadInterval, so one certificate pair
// serves both the WebTransport listener and the sidecar HTTPS server.
const (
	// EnvAdminAddr sets AdminOptions.Addr, e.g. "127.0.0.1:9090"; setting it
	// enables the admin listener in RunOptionsFromEnv.
	EnvAdminAddr = "GOLEM_ADMIN_ADDR"
	// EnvAdminToken sets AdminOptions.Token, the admin bearer token. Its value
	// is never logged.
	EnvAdminToken = "GOLEM_ADMIN_TOKEN"
	// EnvHTTPAddr sets the sidecar RunOptions.HTTPServer.Addr, e.g. ":8080".
	EnvHTTPAddr = "GOLEM_HTTP_ADDR"
)

// Environment variables read by SnapshotOptionsFromEnv.
const (
	// EnvSnapshotDir sets SnapshotOptions.Dir, the directory RunGame keeps
	// SnapshotFileName in; setting it enables entity snapshots.
	EnvSnapshotDir = "GOLEM_SNAPSHOT_DIR"
	// EnvSnapshotInterval sets SnapshotOptions.Interval, the autosave period in
	// Go duration syntax, e.g. "5m"; "0" disables autosave.
	EnvSnapshotInterval = "GOLEM_SNAPSHOT_INTERVAL"
)

// AdminOptionsFromEnv returns base with Addr and Token overridden by
// GOLEM_ADMIN_ADDR and GOLEM_ADMIN_TOKEN. A variable that is unset or blank
// after trimming leaves its field unchanged. Values are not validated here:
// Run applies the token length and fail-closed address rules at startup, so a
// too-short token makes Run fail instead of silently disabling authentication.
// It never logs the token and never fails.
func AdminOptionsFromEnv(base AdminOptions) AdminOptions {
	opts := base
	if v, ok := lookupEnvTrimmed(EnvAdminAddr); ok {
		opts.Addr = v
	}
	if v, ok := lookupEnvTrimmed(EnvAdminToken); ok {
		opts.Token = v
	}
	return opts
}

// RunOptionsFromEnv returns base overlaid from the environment:
//
//   - GOLEM_HTTP_ADDR sets base.HTTPServer.Addr when HTTPServer is non-nil.
//     The http.Server is shared with base and modified in place (it cannot be
//     copied).
//   - GOLEM_TLS_CERT_FILE and GOLEM_TLS_KEY_FILE set HTTPTLSCertFile and
//     HTTPTLSKeyFile, and GOLEM_TLS_RELOAD_INTERVAL sets HTTPTLSReloadInterval
//     (Go duration syntax; "0" disables polling), the same variables
//     golem.ServerConfigFromEnv reads for the WebTransport listener.
//   - GOLEM_ADMIN_ADDR and GOLEM_ADMIN_TOKEN overlay a copy of base.Admin as
//     AdminOptionsFromEnv does; when base.Admin is nil and GOLEM_ADMIN_ADDR is
//     set, a new AdminOptions is created. base.Admin itself is not modified.
//
// A variable that is unset or blank after trimming leaves its field unchanged.
// A malformed reload interval (invalid syntax or negative) is logged with its
// variable name and ignored. It never logs the admin token and never fails.
func RunOptionsFromEnv(base RunOptions) RunOptions {
	opts := base
	if v, ok := lookupEnvTrimmed(EnvHTTPAddr); ok && opts.HTTPServer != nil {
		opts.HTTPServer.Addr = v
	}
	if v, ok := lookupEnvTrimmed(golem.EnvTLSCertFile); ok {
		opts.HTTPTLSCertFile = v
	}
	if v, ok := lookupEnvTrimmed(golem.EnvTLSKeyFile); ok {
		opts.HTTPTLSKeyFile = v
	}
	if v, ok := lookupEnvTrimmed(golem.EnvTLSReloadInterval); ok {
		d, err := time.ParseDuration(v)
		switch {
		case err != nil:
			logIgnoredEnv(golem.EnvTLSReloadInterval, v, "want a Go duration such as 30s, or 0 to disable")
		case d < 0:
			logIgnoredEnv(golem.EnvTLSReloadInterval, v, "duration must not be negative")
		default:
			opts.HTTPTLSReloadInterval = d
		}
	}
	_, addrSet := lookupEnvTrimmed(EnvAdminAddr)
	_, tokenSet := lookupEnvTrimmed(EnvAdminToken)
	switch {
	case base.Admin != nil && (addrSet || tokenSet):
		admin := AdminOptionsFromEnv(*base.Admin)
		opts.Admin = &admin
	case base.Admin == nil && addrSet:
		admin := AdminOptionsFromEnv(AdminOptions{})
		opts.Admin = &admin
	}
	return opts
}

// SnapshotOptionsFromEnv returns base with Dir overridden by GOLEM_SNAPSHOT_DIR
// and Interval by GOLEM_SNAPSHOT_INTERVAL (Go duration syntax; "0" disables
// autosave). A variable that is unset or blank after trimming leaves its field
// unchanged. A malformed interval (invalid syntax or negative) is logged with
// its variable name and ignored. It never fails.
func SnapshotOptionsFromEnv(base SnapshotOptions) SnapshotOptions {
	opts := base
	if v, ok := lookupEnvTrimmed(EnvSnapshotDir); ok {
		opts.Dir = v
	}
	if v, ok := lookupEnvTrimmed(EnvSnapshotInterval); ok {
		d, err := time.ParseDuration(v)
		switch {
		case err != nil:
			logIgnoredEnv(EnvSnapshotInterval, v, "want a Go duration such as 5m, or 0 to disable autosave")
		case d < 0:
			logIgnoredEnv(EnvSnapshotInterval, v, "duration must not be negative")
		default:
			opts.Interval = d
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

func logIgnoredEnv(name string, value string, reason string) {
	log.Printf("golem/host: ignoring %s=%q: %s", name, value, reason)
}
