// Command unity-integration runs one bounded content/Unity/physics/navigation fixture.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"time"
)

type options struct {
	addr, content, evidence, resume string
}

func main() {
	var o options
	flag.StringVar(&o.addr, "addr", "127.0.0.1:18790", "loopback HTTP/WebSocket address")
	flag.StringVar(&o.content, "content", "generated/ContentDefinitions.json", "finalized content bundle")
	flag.StringVar(&o.evidence, "evidence", "", "new evidence directory; required")
	flag.StringVar(&o.resume, "resume", "", "paired checkpoint directory to restore before accepting clients")
	flag.Parse()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	if err := run(ctx, o); err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, o options) error {
	if o.evidence == "" {
		return errors.New("--evidence is required")
	}
	host, _, err := net.SplitHostPort(o.addr)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		return errors.New("--addr must be an explicit loopback IP and port")
	}
	if err := os.MkdirAll(o.evidence, 0700); err != nil {
		return err
	}
	app, err := newFixture(o)
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", o.addr)
	if err != nil {
		app.dispose()
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	server := &http.Server{Handler: app.handler(), ReadHeaderTimeout: 5 * time.Second}
	goErr := make(chan error, 1)
	httpErr := make(chan error, 1)
	go func() { goErr <- app.run(ctx); cancel() }()
	go func() { httpErr <- server.Serve(listener); cancel() }()
	fmt.Printf("integration ready: http://%s/integration/state\n", listener.Addr())
	<-ctx.Done()
	shutdown, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	_ = server.Shutdown(shutdown)
	runtimeErr, servingErr := <-goErr, <-httpErr
	if err := writeJSONExclusive(filepath.Join(o.evidence, "terminal-state.json"), app.snapshot()); err != nil {
		return err
	}
	if runtimeErr != nil && !errors.Is(runtimeErr, context.Canceled) {
		return runtimeErr
	}
	if servingErr != nil && !errors.Is(servingErr, http.ErrServerClosed) {
		return servingErr
	}
	return nil
}
