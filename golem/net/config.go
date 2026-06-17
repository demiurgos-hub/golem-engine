package net

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
	// WebTransportAllowedOrigins lists exact browser origins allowed to connect
	// to WebTransport, e.g. "https://game.example.com:8080".
	WebTransportAllowedOrigins []string
	// WebTransportAllowSameHostOrigin allows HTTPS origins whose hostname
	// matches the WebTransport request host, regardless of port.
	WebTransportAllowSameHostOrigin bool
}
