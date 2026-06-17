package host

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"golem-engine/golem"
)

// RunOptions configures Run.
type RunOptions struct {
	Server          *golem.Server
	HTTPServer      *http.Server
	HTTPTLSCertFile string
	HTTPTLSKeyFile  string
	ShutdownTimeout time.Duration
	Signals         []os.Signal
}

type componentError struct {
	name string
	err  error
}

// Run starts the golem server, waits for transport readiness, starts an optional
// sidecar HTTP server, and shuts both down when ctx or a configured signal fires.
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
	if len(opts.Signals) > 0 {
		var stop context.CancelFunc
		ctx, stop = signal.NotifyContext(ctx, opts.Signals...)
		defer stop()
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	errCh := make(chan componentError, 2)
	components := 0

	if opts.Server != nil {
		components++
		go func() {
			errCh <- componentError{name: "golem", err: opts.Server.Run(ctx)}
		}()
		if err := opts.Server.WaitReady(ctx); err != nil {
			cancel()
			return fmt.Errorf("golem/host: golem ready: %w", err)
		}
	}

	if opts.HTTPServer != nil {
		if err := ValidateTLSFiles(opts.HTTPTLSCertFile, opts.HTTPTLSKeyFile); err != nil {
			cancel()
			return err
		}
		components++
		go shutdownHTTPOnCancel(ctx, opts.HTTPServer, opts.ShutdownTimeout)
		go func() {
			var err error
			if opts.HTTPTLSCertFile != "" {
				err = opts.HTTPServer.ListenAndServeTLS(opts.HTTPTLSCertFile, opts.HTTPTLSKeyFile)
			} else {
				err = opts.HTTPServer.ListenAndServe()
			}
			errCh <- componentError{name: "http", err: err}
		}()
	}

	if components == 0 {
		return nil
	}
	ce := <-errCh
	cancel()
	if ignoredRunError(ce.err) {
		return nil
	}
	return fmt.Errorf("golem/host: %s: %w", ce.name, ce.err)
}

// ValidateTLSFiles verifies that HTTP TLS cert and key paths are provided as a pair.
func ValidateTLSFiles(certFile, keyFile string) error {
	if (certFile == "") != (keyFile == "") {
		return errors.New("golem/host: set both HTTP TLS cert and key files, or leave both empty")
	}
	return nil
}

func shutdownHTTPOnCancel(ctx context.Context, srv *http.Server, timeout time.Duration) {
	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
}

func ignoredRunError(err error) bool {
	return err == nil || errors.Is(err, context.Canceled) || errors.Is(err, http.ErrServerClosed)
}
