package net

import (
	"context"
	"crypto/tls"
	"errors"
	"log"
	"sync"
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

// ReloadableCertificate serves a TLS certificate loaded from a PEM
// certificate/key file pair and lets it be replaced while servers keep
// running. Plug GetCertificate into tls.Config.GetCertificate: only new TLS
// handshakes observe a replacement, established connections keep their
// negotiated certificate.
//
// Load, Reload and ReloadIfChanged read both files, validate that they form a
// matching pair and swap the certificate and its hashes as one unit; on any
// error the previous certificate stays in service. Watch polls the files.
// The integrated WebTransport listener uses the same machinery for
// Config.TLSCertFile/TLSKeyFile and Config.TLSReloadInterval.
//
// All methods are safe for concurrent use; reloads are serialized.
type ReloadableCertificate struct {
	name     string
	certFile string
	keyFile  string

	holder certificateHolder
	mu     sync.Mutex
	stamps certificateFileStamps
}

// NewReloadableCertificate returns a ReloadableCertificate for the given PEM
// files without loading them; call Load before serving. name labels the
// certificate in errors and log lines (for example "HTTP"); it defaults to
// "server". Both paths are required.
func NewReloadableCertificate(name string, certFile string, keyFile string) (*ReloadableCertificate, error) {
	if certFile == "" || keyFile == "" {
		return nil, errors.New("golem/net: ReloadableCertificate requires both a certificate file and a key file")
	}
	if name == "" {
		name = "server"
	}
	return &ReloadableCertificate{name: name, certFile: certFile, keyFile: keyFile}, nil
}

// Load reads and installs the certificate pair without logging. Use it for the
// initial load; it returns the read or validation error and leaves any
// previously loaded certificate in service on failure.
func (c *ReloadableCertificate) Load() error {
	return c.reloader().reload(false)
}

// Reload re-reads the certificate pair and swaps it in, logging the new
// certificate's hashes. On error the previous certificate keeps serving.
func (c *ReloadableCertificate) Reload() error {
	return c.reloader().reload(true)
}

// ReloadIfChanged reloads when the size or modification time of either file
// differs from the last successful load and reports whether a new certificate
// was installed. A failed load leaves the recorded file state untouched, so
// the next call retries.
func (c *ReloadableCertificate) ReloadIfChanged() (bool, error) {
	return c.reloader().reloadIfChanged()
}

// GetCertificate implements tls.Config.GetCertificate, returning the current
// certificate, or an error before the first successful Load.
func (c *ReloadableCertificate) GetCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	return c.holder.getCertificate(hello)
}

// Hashes returns a deep copy of the SHA-256 hashes of the current certificate
// chain, or nil before the first successful Load.
func (c *ReloadableCertificate) Hashes() []CertificateHash {
	return c.holder.hashes()
}

// Loaded reports whether a certificate has been loaded successfully.
func (c *ReloadableCertificate) Loaded() bool {
	return c.holder.load() != nil
}

// Files returns the certificate and key file paths.
func (c *ReloadableCertificate) Files() (certFile string, keyFile string) {
	return c.certFile, c.keyFile
}

// Watch polls the files every interval with ReloadIfChanged until ctx is done
// and then returns; it returns immediately when interval is not positive.
// Each distinct failure is logged once (a pair caught mid-write fails until
// both files are complete) and retried on the next tick while the previous
// certificate keeps serving.
func (c *ReloadableCertificate) Watch(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		return
	}
	runCertificateWatch(ctx, interval, c.name, c.reloader().reloadIfChanged)
}

func (c *ReloadableCertificate) reloader() certificateFileReloader {
	return certificateFileReloader{
		name:     c.name,
		certFile: c.certFile,
		keyFile:  c.keyFile,
		holder:   &c.holder,
		mu:       &c.mu,
		stamps:   &c.stamps,
	}
}

// certificateFileReloader is the reload logic shared by ReloadableCertificate
// and the integrated listener, which keeps its holder, mutex and file stamps
// as its own fields. mu serializes reloads and guards stamps, the file stamps
// observed for the last successful load.
type certificateFileReloader struct {
	name     string
	certFile string
	keyFile  string
	holder   *certificateHolder
	mu       *sync.Mutex
	stamps   *certificateFileStamps
}

func (r certificateFileReloader) reload(logSuccess bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.reloadLocked(logSuccess)
}

// reloadLocked loads the files and swaps them in. Callers hold mu.
func (r certificateFileReloader) reloadLocked(logSuccess bool) error {
	state, stamps, err := loadNamedCertificateFiles(r.name, r.certFile, r.keyFile)
	if err != nil {
		return err
	}
	r.holder.store(state)
	*r.stamps = stamps
	if logSuccess {
		log.Printf("golem/net: %s TLS certificate reloaded cert=%q key=%q hashes=%s", r.name, r.certFile, r.keyFile, formatCertificateHashes(state.hashes))
	}
	return nil
}

func (r certificateFileReloader) reloadIfChanged() (bool, error) {
	stamps, err := statCertificateFiles(r.certFile, r.keyFile)
	if err != nil {
		return false, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if stamps.equal(*r.stamps) {
		return false, nil
	}
	if err := r.reloadLocked(true); err != nil {
		return false, err
	}
	return true, nil
}

// runCertificateWatch calls reloadIfChanged every interval until ctx is done.
func runCertificateWatch(ctx context.Context, interval time.Duration, name string, reloadIfChanged func() (bool, error)) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	lastErr := ""
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		if _, err := reloadIfChanged(); err != nil {
			// A pair caught mid-write fails validation on every tick until both
			// files are complete; log each distinct failure once.
			if msg := err.Error(); msg != lastErr {
				lastErr = msg
				log.Printf("golem/net: %s TLS certificate reload failed; keeping previous certificate and retrying every %s: %v", name, interval, err)
			}
			continue
		}
		lastErr = ""
	}
}

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
	r := l.certificateReloader()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.holder.load() == nil {
		return ErrTLSCertificateNotLoaded
	}
	return r.reloadLocked(true)
}

func (l *Listener) certificateReloadable() bool {
	return l.config.Transport == TransportWebTransport && l.config.TLSCertFile != "" && l.config.TLSKeyFile != ""
}

// certificateReloader views the listener's certificate fields through the
// shared reload logic.
func (l *Listener) certificateReloader() certificateFileReloader {
	return certificateFileReloader{
		name:     webTransportCertificateName,
		certFile: l.config.TLSCertFile,
		keyFile:  l.config.TLSKeyFile,
		holder:   &l.tlsCertificate,
		mu:       &l.tlsReloadMu,
		stamps:   &l.tlsFileStamps,
	}
}

// installInitialCertificate publishes the certificate loaded at startup.
func (l *Listener) installInitialCertificate(state *servedCertificate, stamps certificateFileStamps) {
	l.tlsReloadMu.Lock()
	defer l.tlsReloadMu.Unlock()
	l.tlsCertificate.store(state)
	l.tlsFileStamps = stamps
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
		runCertificateWatch(pollCtx, interval, webTransportCertificateName, l.certificateReloader().reloadIfChanged)
	}()
	return func() {
		cancel()
		<-done
	}
}
