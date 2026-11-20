// Command golem-inspector runs the Golem Inspector hub: game servers connect
// to it with the golem/inspector agent, and browsers log in to view any of
// them. Run "golem-inspector -h" for flags; every flag also reads a
// GOLEM_INSPECTOR_* environment variable. "golem-inspector hash-password"
// prints a bcrypt hash of a password read from stdin.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/demiurgos-hub/golem-engine/golem/inspector/hub"
	"golang.org/x/crypto/bcrypt"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "hash-password" {
		if err := hashPassword(os.Stdin, os.Stdout); err != nil {
			log.Fatalf("golem-inspector: %v", err)
		}
		return
	}
	opts, err := parseOptions(os.Args[1:], os.Getenv)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		log.Fatalf("golem-inspector: %v", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, opts); err != nil {
		log.Fatalf("golem-inspector: %v", err)
	}
}

type options struct {
	addr            string
	tlsCert         string
	tlsKey          string
	webDir          string
	shutdownTimeout time.Duration
	hub             hub.Config
}

// parseOptions reads flags, falling back to GOLEM_INSPECTOR_* variables for
// any flag not given on the command line.
func parseOptions(args []string, getenv func(string) string) (options, error) {
	fsFlags := flag.NewFlagSet("golem-inspector", flag.ContinueOnError)
	env := func(name, def string) string {
		if v := strings.TrimSpace(getenv("GOLEM_INSPECTOR_" + name)); v != "" {
			return v
		}
		return def
	}
	envBool := func(name string) bool {
		v, _ := strconv.ParseBool(env(name, "false"))
		return v
	}
	envDur := func(name string, def time.Duration) time.Duration {
		if d, err := time.ParseDuration(env(name, "")); err == nil {
			return d
		}
		return def
	}
	envInt := func(name string, def int) int {
		if n, err := strconv.Atoi(env(name, "")); err == nil {
			return n
		}
		return def
	}

	var o options
	var origins string
	var cookieSecure string
	fsFlags.StringVar(&o.addr, "addr", env("ADDR", "127.0.0.1:8787"), "listen address [GOLEM_INSPECTOR_ADDR]")
	fsFlags.StringVar(&o.hub.AgentToken, "agent-token", env("AGENT_TOKEN", ""), "token agents must present [GOLEM_INSPECTOR_AGENT_TOKEN]")
	fsFlags.StringVar(&o.hub.PasswordHash, "password-hash", env("PASSWORD_HASH", ""), "bcrypt hash of the UI password; empty disables login [GOLEM_INSPECTOR_PASSWORD_HASH]")
	fsFlags.StringVar(&origins, "public-origins", env("PUBLIC_ORIGINS", ""), "comma-separated browser origins allowed to log in [GOLEM_INSPECTOR_PUBLIC_ORIGINS]")
	fsFlags.StringVar(&o.tlsCert, "tls-cert", env("TLS_CERT", ""), "TLS certificate file [GOLEM_INSPECTOR_TLS_CERT]")
	fsFlags.StringVar(&o.tlsKey, "tls-key", env("TLS_KEY", ""), "TLS key file [GOLEM_INSPECTOR_TLS_KEY]")
	fsFlags.BoolVar(&o.hub.BehindProxy, "behind-proxy", envBool("BEHIND_PROXY"), "a TLS-terminating proxy fronts the hub [GOLEM_INSPECTOR_BEHIND_PROXY]")
	fsFlags.StringVar(&cookieSecure, "cookie-secure", env("COOKIE_SECURE", ""), "mark the session cookie Secure (default: with TLS or behind a proxy) [GOLEM_INSPECTOR_COOKIE_SECURE]")
	fsFlags.BoolVar(&o.hub.Production, "production", envBool("PRODUCTION"), "enforce production rules [GOLEM_INSPECTOR_PRODUCTION]")
	fsFlags.DurationVar(&o.hub.ObserveGrace, "observe-grace", envDur("OBSERVE_GRACE", 30*time.Second), "keep observing this long after the last viewer leaves [GOLEM_INSPECTOR_OBSERVE_GRACE]")
	fsFlags.DurationVar(&o.hub.SessionMaxAge, "session-max-age", envDur("SESSION_MAX_AGE", 10*time.Hour), "login session lifetime [GOLEM_INSPECTOR_SESSION_MAX_AGE]")
	fsFlags.StringVar(&o.hub.MetricsToken, "metrics-token", env("METRICS_TOKEN", ""), "enable /metrics behind this bearer token [GOLEM_INSPECTOR_METRICS_TOKEN]")
	fsFlags.StringVar(&o.webDir, "web-dir", env("WEB_DIR", ""), "serve the UI from this directory instead of the embedded build [GOLEM_INSPECTOR_WEB_DIR]")
	fsFlags.IntVar(&o.hub.MaxAgents, "max-agents", envInt("MAX_AGENTS", 256), "registered agent limit [GOLEM_INSPECTOR_MAX_AGENTS]")
	fsFlags.IntVar(&o.hub.MaxClients, "max-clients", envInt("MAX_CLIENTS", 128), "browser connection limit [GOLEM_INSPECTOR_MAX_CLIENTS]")
	fsFlags.DurationVar(&o.hub.AgentHeartbeatTimeout, "agent-heartbeat-timeout", envDur("AGENT_HEARTBEAT_TIMEOUT", 45*time.Second), "purge silent agents and offline targets after this long [GOLEM_INSPECTOR_AGENT_HEARTBEAT_TIMEOUT]")
	fsFlags.DurationVar(&o.shutdownTimeout, "shutdown-timeout", envDur("SHUTDOWN_TIMEOUT", 10*time.Second), "graceful shutdown bound [GOLEM_INSPECTOR_SHUTDOWN_TIMEOUT]")
	if err := fsFlags.Parse(args); err != nil {
		return o, err
	}
	if fsFlags.NArg() > 0 {
		return o, fmt.Errorf("unexpected arguments: %v", fsFlags.Args())
	}
	for _, origin := range strings.Split(origins, ",") {
		if origin = strings.TrimSpace(origin); origin != "" {
			o.hub.PublicOrigins = append(o.hub.PublicOrigins, strings.TrimSuffix(origin, "/"))
		}
	}
	o.hub.TLS = o.tlsCert != "" || o.tlsKey != ""
	o.hub.CookieSecure = o.hub.TLS || o.hub.BehindProxy
	if cookieSecure != "" {
		v, err := strconv.ParseBool(cookieSecure)
		if err != nil {
			return o, fmt.Errorf("cookie-secure: %w", err)
		}
		o.hub.CookieSecure = v
	}
	if o.hub.ObserveGrace == 0 {
		o.hub.ObserveGrace = -1 // explicit zero means stop immediately
	}
	return o, nil
}

func run(ctx context.Context, o options) error {
	webFS, err := uiFS(o.webDir)
	if err != nil {
		return err
	}
	o.hub.WebFS = webFS
	host := ""
	if len(o.hub.PublicOrigins) == 1 {
		if u, err := url.Parse(o.hub.PublicOrigins[0]); err == nil {
			host = u.Hostname()
		}
	}
	if err := hub.ValidateTLSFiles(o.tlsCert, o.tlsKey, host, time.Now()); err != nil {
		return err
	}
	h, err := hub.New(o.hub)
	if err != nil {
		return err
	}
	if o.hub.AgentToken == "" || o.hub.AgentToken == hub.DevAgentToken {
		log.Printf("golem-inspector: using the development agent token %q; set GOLEM_INSPECTOR_AGENT_TOKEN for real deployments", hub.DevAgentToken)
	}
	if o.hub.PasswordHash == "" {
		log.Printf("golem-inspector: login is disabled (no password hash)")
	}
	srv := &http.Server{Addr: o.addr, Handler: h.Handler(), ReadHeaderTimeout: 10 * time.Second}
	errc := make(chan error, 1)
	go func() {
		scheme := "http"
		var err error
		if o.hub.TLS {
			scheme = "https"
			log.Printf("golem-inspector: listening on %s://%s (agents: /ws/agent)", scheme, o.addr)
			err = srv.ListenAndServeTLS(o.tlsCert, o.tlsKey)
		} else {
			log.Printf("golem-inspector: listening on %s://%s (agents: /ws/agent)", scheme, o.addr)
			err = srv.ListenAndServe()
		}
		if !errors.Is(err, http.ErrServerClosed) {
			errc <- err
		}
		close(errc)
	}()
	select {
	case err := <-errc:
		if err != nil {
			return err
		}
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), o.shutdownTimeout)
	defer cancel()
	hubErr := h.Close(shutdownCtx)
	srvErr := srv.Shutdown(shutdownCtx)
	return errors.Join(hubErr, srvErr)
}

// uiFS returns the UI from dir, or the embedded build, or a placeholder page
// when the binary was built without running the UI build.
func uiFS(dir string) (fs.FS, error) {
	if dir != "" {
		if _, err := os.Stat(dir + "/index.html"); err != nil {
			return nil, fmt.Errorf("web dir: %w", err)
		}
		return os.DirFS(dir), nil
	}
	sub, err := fs.Sub(embeddedWeb, "web/dist")
	if err != nil {
		return nil, err
	}
	if _, err := fs.Stat(sub, "index.html"); err == nil {
		return sub, nil
	}
	return fs.Sub(placeholderWeb, "placeholder")
}

func hashPassword(in io.Reader, out io.Writer) error {
	fmt.Fprint(os.Stderr, "password: ")
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	password := strings.TrimRight(line, "\r\n")
	if len(password) < 12 {
		return errors.New("use a password of at least 12 characters")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), hub.MinBcryptCost)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, string(hash))
	return err
}
