package host

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"maps"
	"net"
	"net/http"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	"github.com/demiurgos-hub/golem-engine/golem"
	golemnet "github.com/demiurgos-hub/golem-engine/golem/net"
)

// MinAdminTokenLength is the minimum length of a non-empty AdminOptions.Token.
const MinAdminTokenLength = 16

// DefaultAdminSnapshotTimeout bounds POST /snapshot when
// AdminOptions.SnapshotTimeout is zero.
const DefaultAdminSnapshotTimeout = 60 * time.Second

// adminMaxBodyBytes caps POST bodies, which the admin endpoints ignore.
const adminMaxBodyBytes = 4 << 10

// AdminOptions configures the admin HTTP listener that Run starts when
// RunOptions.Admin is set with a non-empty Addr.
//
// The admin listener is a separate, small JSON API for the operator or a
// deploy tool (health checks, stats, pre-backup snapshots, certificate
// reloads); it is not part of the game's public sidecar and should be bound
// to a private interface or port. It serves no static files and no pprof.
//
// Security: every endpoint except GET /live requires
// "Authorization: Bearer <Token>", compared in constant time on SHA-256
// digests; failures get 401 with "WWW-Authenticate: Bearer". Run fails closed
// at startup when Token is empty and Addr is not loopback-only (an IP
// loopback address or "localhost"), and rejects a non-empty Token shorter
// than MinAdminTokenLength. With a loopback Addr an empty Token disables
// authentication for local development. The token is never logged or
// included in errors.
type AdminOptions struct {
	// Addr is the TCP listen address, e.g. "127.0.0.1:9090" or ":9090".
	Addr string
	// Token is the bearer token required by every endpoint except /live.
	Token string
	// Info carries the game-specific facts reported by GET /info.
	Info AdminInfo
	// Snapshot, when set, is run by POST /snapshot with a context that is
	// cancelled after SnapshotTimeout or when shutdown begins. At most one
	// runs at a time. It runs while the server is ready, so it should capture
	// on the tick goroutine, for example with Server.SaveSnapshotOnTick. Run
	// waits for a snapshot in progress to return before AfterServerStop.
	Snapshot func(ctx context.Context) error
	// SnapshotTimeout bounds POST /snapshot; default
	// DefaultAdminSnapshotTimeout. A snapshot that ignores its context keeps
	// running after the timeout response, and later requests get 409 until it
	// returns.
	SnapshotTimeout time.Duration

	// listening, when set (tests), receives the bound address.
	listening func(net.Addr)
}

// AdminInfo is what only the game knows, reported by GET /info alongside the
// engine-derived facts.
type AdminInfo struct {
	// Name identifies the game or server build.
	Name string
	// SchemaFingerprint is the generated SchemaFingerprint constant, which
	// snapshots and clients must match.
	SchemaFingerprint string
	// CertificateHashesPublished reports whether the game's realtime-config
	// endpoint publishes WebTransport certificate hashes for browser pinning
	// (golem.RealtimeConfigOptions.IncludeServerCertificateHashes).
	CertificateHashesPublished bool
	// Extra holds additional string facts; Run copies it at startup.
	Extra map[string]string
}

// adminConfig is validated AdminOptions.
type adminConfig struct {
	opts        AdminOptions
	tokenDigest [sha256.Size]byte
	requireAuth bool
}

func newAdminConfig(opts AdminOptions) (*adminConfig, error) {
	cfg := &adminConfig{opts: opts}
	if opts.Token != "" {
		if len(opts.Token) < MinAdminTokenLength {
			return nil, fmt.Errorf("golem/host: admin token must be at least %d characters", MinAdminTokenLength)
		}
		cfg.tokenDigest = sha256.Sum256([]byte(opts.Token))
		cfg.requireAuth = true
	} else {
		loopback, err := isLoopbackAddr(opts.Addr)
		if err != nil {
			return nil, fmt.Errorf("golem/host: admin address %q: %w", opts.Addr, err)
		}
		if !loopback {
			return nil, fmt.Errorf("golem/host: admin address %q is not loopback-only; set an admin token (%s) or bind to 127.0.0.1, ::1 or localhost", opts.Addr, EnvAdminToken)
		}
		log.Printf("golem/host: admin listener on %q has no token; authentication is disabled (loopback only)", opts.Addr)
	}
	if cfg.opts.SnapshotTimeout <= 0 {
		cfg.opts.SnapshotTimeout = DefaultAdminSnapshotTimeout
	}
	cfg.opts.Info.Extra = maps.Clone(opts.Info.Extra)
	return cfg, nil
}

// isLoopbackAddr reports whether a host:port listen address binds loopback
// only. An empty host binds every interface.
func isLoopbackAddr(addr string) (bool, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false, err
	}
	if strings.EqualFold(host, "localhost") {
		return true, nil
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback(), nil
}

// isLoopbackHostHeader reports whether a request Host header names loopback.
func isLoopbackHostHeader(hostport string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// adminServer serves the admin endpoints for one Run.
type adminServer struct {
	cfg         *adminConfig
	server      *golem.Server
	sidecarCert *golemnet.ReloadableCertificate
	// ctx is Run's shared context: once it is done the server is stopping.
	ctx     context.Context
	ready   atomic.Bool
	started time.Time
	// snapshotSem holds a token while a snapshot runs; shutdown takes it for
	// good (waitSnapshotIdle).
	snapshotSem chan struct{}
	http        *http.Server
}

func newAdminServer(ctx context.Context, cfg *adminConfig, server *golem.Server, sidecarCert *golemnet.ReloadableCertificate) *adminServer {
	a := &adminServer{
		cfg:         cfg,
		server:      server,
		sidecarCert: sidecarCert,
		ctx:         ctx,
		started:     time.Now(),
		snapshotSem: make(chan struct{}, 1),
	}
	a.http = &http.Server{
		Addr:              cfg.opts.Addr,
		Handler:           a,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      cfg.opts.SnapshotTimeout + 10*time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    8 << 10,
	}
	return a
}

// waitSnapshotIdle waits for a snapshot in progress and blocks later ones.
func (a *adminServer) waitSnapshotIdle() {
	a.snapshotSem <- struct{}{}
}

// notReadyReason returns "", "starting" or "stopping".
func (a *adminServer) notReadyReason() string {
	if a.ctx.Err() != nil {
		return "stopping"
	}
	if !a.ready.Load() {
		return "starting"
	}
	return ""
}

func (a *adminServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.URL.Path == "/live" {
		if requireMethod(w, r, http.MethodGet) {
			writeAdminJSON(w, http.StatusOK, map[string]bool{"live": true})
		}
		return
	}
	// The admin API is for operator tooling, never for web pages: refuse
	// anything a browser sends cross-site, and when authentication is disabled
	// (loopback only) also refuse a non-loopback Host so DNS rebinding cannot
	// reach it through a visitor's browser.
	if r.Header.Get("Origin") != "" || (!a.cfg.requireAuth && !isLoopbackHostHeader(r.Host)) {
		writeAdminError(w, http.StatusForbidden, "forbidden", "browser and cross-site requests are not allowed")
		return
	}
	if !a.authorized(r) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="golem-admin"`)
		writeAdminError(w, http.StatusUnauthorized, "unauthorized", "missing or invalid bearer token")
		return
	}
	switch r.URL.Path {
	case "/ready":
		if requireMethod(w, r, http.MethodGet) {
			a.handleReady(w)
		}
	case "/info":
		if requireMethod(w, r, http.MethodGet) {
			a.handleInfo(w)
		}
	case "/stats":
		if requireMethod(w, r, http.MethodGet) {
			a.handleStats(w)
		}
	case "/snapshot":
		if requireMethod(w, r, http.MethodPost) && discardBody(w, r) {
			a.handleSnapshot(w)
		}
	case "/reload-tls":
		if requireMethod(w, r, http.MethodPost) && discardBody(w, r) {
			a.handleReloadTLS(w)
		}
	default:
		writeAdminError(w, http.StatusNotFound, "not_found", "unknown admin endpoint")
	}
}

func (a *adminServer) authorized(r *http.Request) bool {
	if !a.cfg.requireAuth {
		return true
	}
	scheme, token, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return false
	}
	digest := sha256.Sum256([]byte(strings.TrimSpace(token)))
	return subtle.ConstantTimeCompare(digest[:], a.cfg.tokenDigest[:]) == 1
}

type adminReadyResponse struct {
	Ready  bool   `json:"ready"`
	Reason string `json:"reason,omitempty"`
}

func (a *adminServer) handleReady(w http.ResponseWriter) {
	if reason := a.notReadyReason(); reason != "" {
		writeAdminJSON(w, http.StatusServiceUnavailable, adminReadyResponse{Reason: reason})
		return
	}
	writeAdminJSON(w, http.StatusOK, adminReadyResponse{Ready: true})
}

type adminCertificateHash struct {
	Algorithm string `json:"algorithm"`
	Value     string `json:"value"`
}

type adminInfoResponse struct {
	Name                          string                 `json:"name"`
	SchemaFingerprint             string                 `json:"schema_fingerprint"`
	EngineVersion                 string                 `json:"engine_version"`
	GoVersion                     string                 `json:"go_version"`
	Transport                     string                 `json:"transport"`
	TickRate                      int                    `json:"tick_rate"`
	StartedAt                     string                 `json:"started_at"`
	CertificateHashesPublished    bool                   `json:"certificate_hashes_published"`
	WebTransportCertificateHashes []adminCertificateHash `json:"webtransport_certificate_hashes"`
	HTTPTLSReloadable             bool                   `json:"http_tls_reloadable"`
	HTTPCertificateHashes         []adminCertificateHash `json:"http_certificate_hashes"`
	Extra                         map[string]string      `json:"extra"`
}

func (a *adminServer) handleInfo(w http.ResponseWriter) {
	info := a.cfg.opts.Info
	resp := adminInfoResponse{
		Name:                          info.Name,
		SchemaFingerprint:             info.SchemaFingerprint,
		EngineVersion:                 golem.EngineVersion(),
		GoVersion:                     runtime.Version(),
		Transport:                     string(a.server.Transport()),
		TickRate:                      a.server.TickStats().TickRate,
		StartedAt:                     a.started.UTC().Format(time.RFC3339Nano),
		CertificateHashesPublished:    info.CertificateHashesPublished,
		WebTransportCertificateHashes: adminHashes(a.server.WebTransportCertificateHashes()),
		HTTPTLSReloadable:             a.sidecarCert != nil,
		HTTPCertificateHashes:         []adminCertificateHash{},
		Extra:                         info.Extra,
	}
	if a.sidecarCert != nil {
		resp.HTTPCertificateHashes = adminHashes(a.sidecarCert.Hashes())
	}
	if resp.Extra == nil {
		resp.Extra = map[string]string{}
	}
	writeAdminJSON(w, http.StatusOK, resp)
}

func adminHashes(hashes []golemnet.CertificateHash) []adminCertificateHash {
	out := make([]adminCertificateHash, 0, len(hashes))
	for _, hash := range hashes {
		out = append(out, adminCertificateHash{Algorithm: hash.Algorithm, Value: hex.EncodeToString(hash.Value)})
	}
	return out
}

type adminTickStats struct {
	Tick     uint64  `json:"tick"`
	Rate     int     `json:"rate"`
	BudgetMs float64 `json:"budget_ms"`
	LastMs   float64 `json:"last_ms"`
	MaxMs    float64 `json:"max_ms"`
	Overruns uint64  `json:"overruns"`
}

type adminReplicationStats struct {
	LastTick              uint64 `json:"last_tick"`
	DeltasFlushed         int    `json:"deltas_flushed"`
	StreamBatchedFrames   int    `json:"stream_batched_frames"`
	StreamWireMsgs        int    `json:"stream_wire_msgs"`
	DatagramBatchedFrames int    `json:"datagram_batched_frames"`
	DatagramWirePayloads  int    `json:"datagram_wire_payloads"`
}

type adminTaskStats struct {
	QueuedTasks             uint64 `json:"queued_tasks"`
	RunningTasks            uint64 `json:"running_tasks"`
	QueuedCompletions       uint64 `json:"queued_completions"`
	QueuedPosts             uint64 `json:"queued_posts"`
	TasksAccepted           uint64 `json:"tasks_accepted"`
	TasksFinished           uint64 `json:"tasks_finished"`
	TaskCompletionsExecuted uint64 `json:"task_completions_executed"`
	TasksRejectedFull       uint64 `json:"tasks_rejected_full"`
	TasksCancelled          uint64 `json:"tasks_cancelled"`
	TaskPanics              uint64 `json:"task_panics"`
	PostsAccepted           uint64 `json:"posts_accepted"`
	PostsExecuted           uint64 `json:"posts_executed"`
	PostsRejectedFull       uint64 `json:"posts_rejected_full"`
}

type adminStatsResponse struct {
	UptimeSeconds float64               `json:"uptime_seconds"`
	Sessions      int                   `json:"sessions"`
	Entities      int                   `json:"entities"`
	Tick          adminTickStats        `json:"tick"`
	Replication   adminReplicationStats `json:"replication"`
	Tasks         adminTaskStats        `json:"tasks"`
}

func (a *adminServer) handleStats(w http.ResponseWriter) {
	tick := a.server.TickStats()
	repl := a.server.ReplicationStats()
	tasks := a.server.TaskStats()
	writeAdminJSON(w, http.StatusOK, adminStatsResponse{
		UptimeSeconds: time.Since(a.started).Seconds(),
		Sessions:      a.server.SessionCount(),
		Entities:      a.server.Len(),
		Tick: adminTickStats{
			Tick:     tick.Tick,
			Rate:     tick.TickRate,
			BudgetMs: durationMs(tick.Budget),
			LastMs:   durationMs(tick.LastDuration),
			MaxMs:    durationMs(tick.MaxDuration),
			Overruns: tick.Overruns,
		},
		Replication: adminReplicationStats{
			LastTick:              repl.LastTick,
			DeltasFlushed:         repl.DeltasFlushed,
			StreamBatchedFrames:   repl.StreamBatchedFrames,
			StreamWireMsgs:        repl.StreamWireMsgs,
			DatagramBatchedFrames: repl.DatagramBatchedFrames,
			DatagramWirePayloads:  repl.DatagramWirePayloads,
		},
		Tasks: adminTaskStats{
			QueuedTasks:             tasks.QueuedTasks,
			RunningTasks:            tasks.RunningTasks,
			QueuedCompletions:       tasks.QueuedCompletions,
			QueuedPosts:             tasks.QueuedPosts,
			TasksAccepted:           tasks.TasksAccepted,
			TasksFinished:           tasks.TasksFinished,
			TaskCompletionsExecuted: tasks.TaskCompletionsExecuted,
			TasksRejectedFull:       tasks.TasksRejectedFull,
			TasksCancelled:          tasks.TasksCancelled,
			TaskPanics:              tasks.TaskPanics,
			PostsAccepted:           tasks.PostsAccepted,
			PostsExecuted:           tasks.PostsExecuted,
			PostsRejectedFull:       tasks.PostsRejectedFull,
		},
	})
}

func durationMs(d time.Duration) float64 {
	return float64(d) / float64(time.Millisecond)
}

type adminSnapshotResponse struct {
	OK         bool    `json:"ok"`
	DurationMs float64 `json:"duration_ms"`
}

func (a *adminServer) handleSnapshot(w http.ResponseWriter) {
	snapshot := a.cfg.opts.Snapshot
	if snapshot == nil {
		writeAdminError(w, http.StatusNotImplemented, "not_implemented", "snapshot is not configured")
		return
	}
	if reason := a.notReadyReason(); reason != "" {
		writeAdminNotReady(w, reason)
		return
	}
	select {
	case a.snapshotSem <- struct{}{}:
	default:
		writeAdminError(w, http.StatusConflict, "snapshot_in_progress", "a snapshot is already running")
		return
	}
	timeout := a.cfg.opts.SnapshotTimeout
	ctx, cancel := context.WithTimeout(a.ctx, timeout)
	start := time.Now()
	result := make(chan error, 1)
	go func() {
		defer func() { <-a.snapshotSem }()
		defer cancel()
		result <- runSnapshot(ctx, snapshot)
	}()

	var err error
	finished := false
	select {
	case err = <-result:
		finished = true
	case <-ctx.Done():
		err = ctx.Err()
	}
	elapsed := time.Since(start)
	switch {
	case finished && err == nil:
		writeAdminJSON(w, http.StatusOK, adminSnapshotResponse{OK: true, DurationMs: durationMs(elapsed)})
	case errors.Is(ctx.Err(), context.DeadlineExceeded) && a.ctx.Err() == nil:
		if finished {
			log.Printf("golem/host: admin snapshot failed after %s (timeout %s): %v", elapsed, timeout, err)
		} else {
			log.Printf("golem/host: admin snapshot timed out after %s; it keeps running until it returns", timeout)
		}
		writeAdminError(w, http.StatusGatewayTimeout, "snapshot_timeout", "snapshot did not finish in time")
	case a.ctx.Err() != nil:
		log.Printf("golem/host: admin snapshot interrupted by shutdown after %s: %v", elapsed, err)
		writeAdminNotReady(w, "stopping")
	default:
		log.Printf("golem/host: admin snapshot failed after %s: %v", elapsed, err)
		writeAdminError(w, http.StatusInternalServerError, "snapshot_failed", "snapshot failed; see server logs")
	}
}

// runSnapshot calls fn and turns a panic into an error so a faulty snapshot
// callback cannot crash the process from the handler's goroutine.
func runSnapshot(ctx context.Context, fn func(context.Context) error) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("snapshot panicked: %v", p)
		}
	}()
	return fn(ctx)
}

// Reload outcomes reported by POST /reload-tls.
const (
	adminReloadReloaded    = "reloaded"
	adminReloadUnsupported = "unsupported"
	adminReloadNotLoaded   = "not_loaded"
	adminReloadFailed      = "failed"
)

type adminReloadTLSResponse struct {
	OK           bool   `json:"ok"`
	WebTransport string `json:"webtransport"`
	HTTP         string `json:"http"`
}

func (a *adminServer) handleReloadTLS(w http.ResponseWriter) {
	resp := adminReloadTLSResponse{WebTransport: adminReloadUnsupported, HTTP: adminReloadUnsupported}
	switch err := a.server.ReloadTLSCertificate(); {
	case err == nil:
		resp.WebTransport = adminReloadReloaded
	case errors.Is(err, golem.ErrTLSCertificateReloadUnsupported):
	case errors.Is(err, golem.ErrTLSCertificateNotLoaded):
		resp.WebTransport = adminReloadNotLoaded
	default:
		resp.WebTransport = adminReloadFailed
		log.Printf("golem/host: admin WebTransport TLS certificate reload failed: %v", err)
	}
	if a.sidecarCert != nil {
		if err := a.sidecarCert.Reload(); err != nil {
			resp.HTTP = adminReloadFailed
			log.Printf("golem/host: admin HTTP TLS certificate reload failed: %v", err)
		} else {
			resp.HTTP = adminReloadReloaded
		}
	}
	status := http.StatusOK
	switch {
	case resp.WebTransport == adminReloadFailed || resp.HTTP == adminReloadFailed:
		status = http.StatusInternalServerError
	case resp.WebTransport == adminReloadNotLoaded:
		status = http.StatusServiceUnavailable
	}
	resp.OK = status == http.StatusOK
	writeAdminJSON(w, status, resp)
}

type adminErrorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message"`
	Reason  string `json:"reason,omitempty"`
}

func writeAdminNotReady(w http.ResponseWriter, reason string) {
	writeAdminJSON(w, http.StatusServiceUnavailable, adminErrorResponse{Error: "not_ready", Message: "server is " + reason, Reason: reason})
}

func writeAdminError(w http.ResponseWriter, status int, code string, message string) {
	writeAdminJSON(w, status, adminErrorResponse{Error: code, Message: message})
}

func writeAdminJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// requireMethod writes 405 and returns false unless r uses method.
func requireMethod(w http.ResponseWriter, r *http.Request, method string) bool {
	if r.Method == method {
		return true
	}
	w.Header().Set("Allow", method)
	writeAdminError(w, http.StatusMethodNotAllowed, "method_not_allowed", "use "+method)
	return false
}

// discardBody reads and drops a small POST body so the connection can be
// reused, writing 413 and returning false when it exceeds adminMaxBodyBytes.
func discardBody(w http.ResponseWriter, r *http.Request) bool {
	r.Body = http.MaxBytesReader(w, r.Body, adminMaxBodyBytes)
	if _, err := io.Copy(io.Discard, r.Body); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeAdminError(w, http.StatusRequestEntityTooLarge, "request_too_large", "request body is ignored and must be small")
		} else {
			writeAdminError(w, http.StatusBadRequest, "bad_request", "could not read request body")
		}
		return false
	}
	return true
}
