package host

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/demiurgos-hub/golem-engine/golem"
	golemnet "github.com/demiurgos-hub/golem-engine/golem/net"
)

// RunOptions configures Run.
type RunOptions struct {
	// Server is the golem server Run starts and stops. Optional when
	// HTTPServer is set; required when Admin is set.
	Server *golem.Server
	// HTTPServer is an optional caller-owned sidecar HTTP server (for example
	// the realtime-config endpoint and the browser client). Run starts it
	// after Server.WaitReady succeeds and shuts it down gracefully.
	HTTPServer *http.Server
	// HTTPTLSCertFile and HTTPTLSKeyFile, set together, make the sidecar serve
	// HTTPS. Unless HTTPServer.TLSConfig configures certificates itself
	// (Certificates, GetCertificate or GetConfigForClient), Run loads the pair
	// before starting anything, replaces HTTPServer.TLSConfig with a clone
	// whose GetCertificate serves the loaded certificate, and can then replace
	// it while running (HTTPTLSReloadInterval, the admin /reload-tls
	// endpoint). Otherwise the caller stays in control: the files are passed
	// to ListenAndServeTLS exactly as before (net/http then serves them unless
	// the client sends SNI and the caller set GetCertificate), and the
	// certificate is not reloadable.
	HTTPTLSCertFile string
	HTTPTLSKeyFile  string
	// HTTPTLSReloadInterval, when positive, polls the sidecar certificate
	// files and reloads them when either file's size or modification time
	// changes. A failed reload keeps the previous certificate, logs each
	// distinct failure once and retries on the next poll. The poll stops when
	// shutdown begins. Zero loads the files once.
	HTTPTLSReloadInterval time.Duration
	// ShutdownTimeout bounds the graceful shutdown of each HTTP server (the
	// sidecar and the admin listener); connections still open afterwards are
	// closed and the timeout is reported in Run's error. Default 5s. It does
	// not bound Server.Run or AfterServerStop.
	ShutdownTimeout time.Duration
	// Signals cancel Run when received. Nil means os.Interrupt and SIGTERM;
	// an empty non-nil slice installs no signal handler.
	Signals []os.Signal
	// AfterServerStop, when set, is called exactly once after Server.Run has
	// returned (the tick loop has stopped and sessions are closed) and before
	// Run returns, whatever started the shutdown: a signal, ctx cancellation
	// or a component failure. Use it for a final Server.SaveSnapshot, which is
	// legal while the server is not running (SaveSnapshotOnTick is not usable
	// once shutdown has begun). The admin listener, when configured, is still
	// serving (/ready already fails) and any admin snapshot in progress has
	// returned. Its error is joined into Run's result. It is not called when
	// Server is nil, when Run fails before starting Server.Run, or when
	// Server.Run reports golem.ErrServerAlreadyRun.
	AfterServerStop func() error
	// Admin, when non-nil with a non-empty Addr, starts the operator-only
	// admin HTTP listener (see AdminOptions); it requires Server.
	Admin *AdminOptions
}

// componentError is one component's exit, recorded in arrival order.
type componentError struct {
	name string
	err  error
}

// Run starts the configured components, blocks until one of them exits or ctx
// or a configured signal fires, then shuts everything down and waits for it.
//
// Startup: validate options (sidecar TLS pair and certificate, admin
// fail-closed rule) → bind and serve the admin listener (/live answers from
// here; /ready reports "starting") → start Server.Run → wait for
// Server.WaitReady → mark ready → start the sidecar HTTP server and its
// certificate poll.
//
// Shutdown, once the first component exits, ctx is cancelled or a signal
// arrives:
//
//  1. /ready starts failing with "stopping" and the shared context is
//     cancelled, which stops Server.Run, begins the sidecar's graceful
//     Shutdown (bounded by ShutdownTimeout, then Close) and stops the
//     certificate poll.
//  2. Wait for Server.Run to return.
//  3. Wait for an admin /snapshot in progress to return.
//  4. Call AfterServerStop.
//  5. Wait for the sidecar's Shutdown and serve goroutine and the certificate
//     poll to finish.
//  6. Shut down the admin listener (bounded by ShutdownTimeout, then Close)
//     and wait for it.
//
// The result is the first non-ignorable component error in arrival order,
// wrapped as "golem/host: <component>: <err>" (context.Canceled and
// http.ErrServerClosed are ignorable, so a signal or cancellation yields nil),
// joined with any shutdown timeout and the AfterServerStop error.
func Run(ctx context.Context, opts RunOptions) error {
	if opts.Server == nil && opts.HTTPServer == nil {
		return errors.New("golem/host: Run requires Server or HTTPServer")
	}
	if opts.ShutdownTimeout <= 0 {
		opts.ShutdownTimeout = 5 * time.Second
	}
	if opts.Signals == nil {
		opts.Signals = []os.Signal{os.Interrupt, syscall.SIGTERM}
	}

	var adminCfg *adminConfig
	if opts.Admin != nil && opts.Admin.Addr != "" {
		if opts.Server == nil {
			return errors.New("golem/host: Admin requires Server")
		}
		cfg, err := newAdminConfig(*opts.Admin)
		if err != nil {
			return err
		}
		adminCfg = cfg
	}
	side, err := prepareSidecar(opts)
	if err != nil {
		return err
	}

	if len(opts.Signals) > 0 {
		var stop context.CancelFunc
		ctx, stop = signal.NotifyContext(ctx, opts.Signals...)
		defer stop()
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	r := &runner{cancel: cancel}

	var admin *adminServer
	var adminDone chan struct{}
	if adminCfg != nil {
		ln, err := net.Listen("tcp", adminCfg.opts.Addr)
		if err != nil {
			return fmt.Errorf("golem/host: admin listen: %w", err)
		}
		admin = newAdminServer(ctx, adminCfg, opts.Server, side.reloadable())
		log.Printf("golem/host: admin listening addr=%q auth=%t", ln.Addr().String(), adminCfg.requireAuth)
		if adminCfg.opts.listening != nil {
			adminCfg.opts.listening(ln.Addr())
		}
		adminDone = r.start("admin", func() error { return admin.http.Serve(ln) })
	}

	var serverDone chan struct{}
	var serverRunErr error
	if opts.Server != nil {
		serverDone = make(chan struct{})
		go func() {
			defer close(serverDone)
			serverRunErr = opts.Server.Run(ctx)
			r.record("golem", serverRunErr)
		}()
		if err := opts.Server.WaitReady(ctx); err != nil {
			// A listener setup failure also ends Server.Run; report it as the
			// readiness failure that it is, whichever exit is recorded first.
			r.recordFirst("golem ready", err)
		}
	}

	var sideDone []chan struct{}
	if ctx.Err() == nil {
		if admin != nil {
			admin.ready.Store(true)
		}
		if side != nil {
			sideDone = side.start(ctx, r, opts)
		}
	}

	<-ctx.Done()
	cancel()

	if serverDone != nil {
		<-serverDone
	}
	var hookErr error
	if admin != nil {
		admin.waitSnapshotIdle()
	}
	if opts.AfterServerStop != nil && serverDone != nil && !errors.Is(serverRunErr, golem.ErrServerAlreadyRun) {
		if err := opts.AfterServerStop(); err != nil {
			hookErr = fmt.Errorf("golem/host: after server stop: %w", err)
		}
	}
	for _, done := range sideDone {
		<-done
	}
	if admin != nil {
		if err := shutdownHTTP(admin.http, opts.ShutdownTimeout); err != nil {
			r.addShutdownError(fmt.Errorf("golem/host: admin shutdown: %w", err))
		}
		<-adminDone
	}
	return r.result(hookErr)
}

// ValidateTLSFiles verifies that HTTP TLS cert and key paths are provided as a pair.
func ValidateTLSFiles(certFile, keyFile string) error {
	if (certFile == "") != (keyFile == "") {
		return errors.New("golem/host: set both HTTP TLS cert and key files, or leave both empty")
	}
	return nil
}

// runner records component exits and cancels the shared context on the first.
type runner struct {
	cancel context.CancelFunc

	mu             sync.Mutex
	exits          []componentError
	shutdownErrors []error
}

// record notes a component exit and starts shutdown.
func (r *runner) record(name string, err error) {
	r.mu.Lock()
	r.exits = append(r.exits, componentError{name: name, err: err})
	r.mu.Unlock()
	r.cancel()
}

// recordFirst is record for an exit that takes precedence over exits already
// recorded.
func (r *runner) recordFirst(name string, err error) {
	r.mu.Lock()
	r.exits = append([]componentError{{name: name, err: err}}, r.exits...)
	r.mu.Unlock()
	r.cancel()
}

func (r *runner) addShutdownError(err error) {
	r.mu.Lock()
	r.shutdownErrors = append(r.shutdownErrors, err)
	r.mu.Unlock()
}

// start runs fn as a component and returns a channel closed when it exits.
func (r *runner) start(name string, fn func() error) chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		r.record(name, fn())
	}()
	return done
}

func (r *runner) result(hookErr error) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	var primary error
	for _, ce := range r.exits {
		if !ignoredRunError(ce.err) {
			primary = fmt.Errorf("golem/host: %s: %w", ce.name, ce.err)
			break
		}
	}
	if len(r.shutdownErrors) == 0 && hookErr == nil {
		return primary
	}
	errs := append([]error{primary}, r.shutdownErrors...)
	return errors.Join(append(errs, hookErr)...)
}

// sidecar is the prepared sidecar HTTP server.
type sidecar struct {
	srv *http.Server
	tls bool
	// certFile/keyFile are passed to ListenAndServeTLS; empty when cert
	// serves the certificate through TLSConfig.GetCertificate.
	certFile string
	keyFile  string
	cert     *golemnet.ReloadableCertificate
}

// prepareSidecar validates the sidecar TLS options and loads a reloadable
// certificate unless the caller's TLSConfig owns its certificates.
func prepareSidecar(opts RunOptions) (*sidecar, error) {
	if opts.HTTPServer == nil {
		return nil, nil
	}
	if err := ValidateTLSFiles(opts.HTTPTLSCertFile, opts.HTTPTLSKeyFile); err != nil {
		return nil, err
	}
	side := &sidecar{srv: opts.HTTPServer}
	if opts.HTTPTLSCertFile == "" {
		return side, nil
	}
	side.tls = true
	if tlsConfigOwnsCertificates(opts.HTTPServer.TLSConfig) {
		side.certFile, side.keyFile = opts.HTTPTLSCertFile, opts.HTTPTLSKeyFile
		if opts.HTTPTLSReloadInterval > 0 {
			log.Printf("golem/host: HTTPServer.TLSConfig configures its own certificates; ignoring HTTPTLSReloadInterval=%s", opts.HTTPTLSReloadInterval)
		}
		return side, nil
	}
	cert, err := golemnet.NewReloadableCertificate("HTTP", opts.HTTPTLSCertFile, opts.HTTPTLSKeyFile)
	if err != nil {
		return nil, fmt.Errorf("golem/host: http: %w", err)
	}
	if err := cert.Load(); err != nil {
		return nil, fmt.Errorf("golem/host: http: %w", err)
	}
	cfg := &tls.Config{}
	if opts.HTTPServer.TLSConfig != nil {
		cfg = opts.HTTPServer.TLSConfig.Clone()
	}
	cfg.GetCertificate = cert.GetCertificate
	opts.HTTPServer.TLSConfig = cfg
	side.cert = cert
	return side, nil
}

func tlsConfigOwnsCertificates(cfg *tls.Config) bool {
	return cfg != nil && (len(cfg.Certificates) > 0 || cfg.GetCertificate != nil || cfg.GetConfigForClient != nil)
}

// reloadable returns the sidecar's reloadable certificate, or nil.
func (s *sidecar) reloadable() *golemnet.ReloadableCertificate {
	if s == nil {
		return nil
	}
	return s.cert
}

// start serves the sidecar, shuts it down when ctx is done and runs the
// certificate poll. The returned channels close when each part has finished.
func (s *sidecar) start(ctx context.Context, r *runner, opts RunOptions) []chan struct{} {
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		<-ctx.Done()
		if err := shutdownHTTP(s.srv, opts.ShutdownTimeout); err != nil {
			r.addShutdownError(fmt.Errorf("golem/host: http shutdown: %w", err))
		}
	}()
	serveDone := r.start("http", func() error {
		if s.tls {
			return s.srv.ListenAndServeTLS(s.certFile, s.keyFile)
		}
		return s.srv.ListenAndServe()
	})
	done := []chan struct{}{shutdownDone, serveDone}
	if s.cert != nil && opts.HTTPTLSReloadInterval > 0 {
		watchDone := make(chan struct{})
		go func() {
			defer close(watchDone)
			s.cert.Watch(ctx, opts.HTTPTLSReloadInterval)
		}()
		done = append(done, watchDone)
	}
	return done
}

// shutdownHTTP shuts srv down gracefully within timeout and force-closes it
// afterwards, returning the timeout error in that case.
func shutdownHTTP(srv *http.Server, timeout time.Duration) error {
	shutdownCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	err := srv.Shutdown(shutdownCtx)
	if err == nil {
		return nil
	}
	_ = srv.Close()
	return fmt.Errorf("%w; remaining connections closed", err)
}

func ignoredRunError(err error) bool {
	return err == nil || errors.Is(err, context.Canceled) || errors.Is(err, http.ErrServerClosed)
}
