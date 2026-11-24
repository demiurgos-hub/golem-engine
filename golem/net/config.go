package net

import "time"

// Config holds settings for the integrated transport listener and HTTP server.
type Config struct {
	Addr              string    // listen address, e.g. ":8080" or ":4433"
	Path              string    // transport endpoint path, e.g. "/ws" or "/wt"
	StaticDir         string    // directory of static files served over HTTP (optional)
	MapDir            string    // directory of map files served at /maps/ over HTTP (optional)
	Transport         Transport // integrated transport kind (default: websocket)
	TLSCertFile       string    // PEM certificate file for WebTransport / HTTP3
	TLSKeyFile        string    // PEM private key file for WebTransport / HTTP3
	DevSelfSignedCert bool      // generate an in-memory self-signed cert when files are not provided
	// TLSReloadInterval, when positive, makes the integrated WebTransport
	// listener check TLSCertFile and TLSKeyFile every interval and reload them
	// (see Listener.ReloadCertificate) when either file's size or modification
	// time changed since the last successful load. Failed reloads are logged and
	// retried on the next tick while the previous certificate keeps serving.
	// Zero (the default) or negative disables polling.
	TLSReloadInterval time.Duration
	// WebTransportAllowedOrigins lists exact browser origins allowed to connect,
	// e.g. "https://game.example.com:8080". Despite its name it applies to both
	// browser transports: the WebTransport endpoint and the WebSocket endpoint
	// (Handler or WebSocketHandler).
	WebTransportAllowedOrigins []string
	// WebTransportAllowSameHostOrigin allows origins whose hostname matches the
	// request host, regardless of port. WebTransport requires an HTTPS origin;
	// the WebSocket endpoint accepts any scheme because local pages may be
	// served over plain HTTP.
	WebTransportAllowSameHostOrigin bool
}
