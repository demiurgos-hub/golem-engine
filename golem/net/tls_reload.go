package net

import (
	"context"
	"errors"
	"log"
	"time"
)

// ErrTLSCertificateReloadUnsupported reports that the listener does not serve
// a certificate loaded from TLSCertFile/TLSKeyFile through the integrated
// WebTransport listener, so there is nothing to reload. This includes
// WebSocket-primary listeners and the in-memory DevSelfSignedCert certificate.
var ErrTLSCertificateReloadUnsupported = errors.New("golem/net: TLS certificate reload requires an integrated WebTransport listener with TLSCertFile and TLSKeyFile")

// ErrTLSCertificateNotLoaded reports that the integrated WebTransport listener
// has not loaded its initial certificate yet: ListenAndServe has not started,
// has not reached TLS setup, or failed before serving. Callers that reload at
// startup should wait for WaitReady first.
var ErrTLSCertificateNotLoaded = errors.New("golem/net: TLS certificate not loaded; the WebTransport listener is not serving yet")

// ReloadCertificate re-reads Config.TLSCertFile and Config.TLSKeyFile,
// validates that they form a matching pair, and atomically switches the
// integrated WebTransport listener to the new certificate. New TLS handshakes
// get the new certificate and CertificateHashes reports its hashes; existing
// connections are unaffected. On any error the previous certificate stays in
// service.
//
// It returns ErrTLSCertificateReloadUnsupported when the listener is not a
// WebTransport listener with certificate files (including DevSelfSignedCert
// listeners), and ErrTLSCertificateNotLoaded before ListenAndServe has loaded
// the initial certificate (see WaitReady). Listeners mounted on a caller-owned
// HTTP/3 server own their TLS configuration and are not reloadable here.
// ReloadCertificate is safe for concurrent use, including with the
// Config.TLSReloadInterval poll; concurrent reloads are serialized.
func (l *Listener) ReloadCertificate() error {
	if !l.certificateReloadable() {
		return ErrTLSCertificateReloadUnsupported
	}
	l.tlsReloadMu.Lock()
	defer l.tlsReloadMu.Unlock()
	if l.tlsCertificate.load() == nil {
		return ErrTLSCertificateNotLoaded
	}
	return l.reloadCertificateLocked()
}

func (l *Listener) certificateReloadable() bool {
	return l.config.Transport == TransportWebTransport && l.config.TLSCertFile != "" && l.config.TLSKeyFile != ""
}

// installInitialCertificate publishes the certificate loaded at startup.
func (l *Listener) installInitialCertificate(state *servedCertificate, stamps certificateFileStamps) {
	l.tlsReloadMu.Lock()
	defer l.tlsReloadMu.Unlock()
	l.tlsCertificate.store(state)
	l.tlsFileStamps = stamps
}

// reloadCertificateLocked loads the configured files and swaps them in.
// Callers hold tlsReloadMu.
func (l *Listener) reloadCertificateLocked() error {
	state, stamps, err := loadServedCertificateFiles(l.config.TLSCertFile, l.config.TLSKeyFile)
	if err != nil {
		return err
	}
	l.tlsCertificate.store(state)
	l.tlsFileStamps = stamps
	log.Printf("golem/net: webtransport TLS certificate reloaded cert=%q key=%q hashes=%s", l.config.TLSCertFile, l.config.TLSKeyFile, formatCertificateHashes(state.hashes))
	return nil
}

// reloadCertificateIfChanged reloads when the certificate or key file's size
// or mtime differs from the last successful load. A failed load leaves the
// recorded stamps untouched, so the next call retries.
func (l *Listener) reloadCertificateIfChanged() error {
	stamps, err := statCertificateFiles(l.config.TLSCertFile, l.config.TLSKeyFile)
	if err != nil {
		return err
	}
	l.tlsReloadMu.Lock()
	defer l.tlsReloadMu.Unlock()
	if stamps.equal(l.tlsFileStamps) {
		return nil
	}
	return l.reloadCertificateLocked()
}

// startCertificateReloadPoll starts the Config.TLSReloadInterval poll when it
// is enabled and returns a function that stops it and waits for it to exit.
func (l *Listener) startCertificateReloadPoll(ctx context.Context) (stop func()) {
	interval := l.config.TLSReloadInterval
	if interval <= 0 || !l.certificateReloadable() {
		return func() {}
	}
	pollCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		l.runCertificateReloadPoll(pollCtx, interval)
	}()
	return func() {
		cancel()
		<-done
	}
}

func (l *Listener) runCertificateReloadPoll(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	lastErr := ""
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		if err := l.reloadCertificateIfChanged(); err != nil {
			// A pair caught mid-write fails validation on every tick until both
			// files are complete; log each distinct failure once.
			if msg := err.Error(); msg != lastErr {
				lastErr = msg
				log.Printf("golem/net: webtransport TLS certificate reload failed; keeping previous certificate and retrying every %s: %v", interval, err)
			}
			continue
		}
		lastErr = ""
	}
}
