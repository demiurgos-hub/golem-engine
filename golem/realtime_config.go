package golem

import (
	"encoding/hex"
	"encoding/json"
	"net/http"
)

// RealtimeConfigOptions configures the browser bootstrap JSON served by
// Server.RealtimeConfigHandler.
type RealtimeConfigOptions struct {
	PublicURL string
	// WebSocketFallbackURL advertises an optional WebSocket endpoint backed by
	// the same Server. It is valid only when WebTransport is the primary
	// transport.
	WebSocketFallbackURL  string
	EventualAckIntervalMs *int
	// IncludeServerCertificateHashes overrides whether WebTransport certificate
	// hashes are returned. When nil, hashes are included only for generated
	// development self-signed certificates.
	IncludeServerCertificateHashes *bool
}

type realtimeCertificateHash struct {
	Algorithm string `json:"algorithm"`
	Value     string `json:"value"`
}

type realtimeEndpoint struct {
	Transport string `json:"transport"`
	URL       string `json:"url"`
}

type realtimeConfigResponse struct {
	Transport               string                    `json:"transport"`
	URL                     string                    `json:"url"`
	ServerCertificateHashes []realtimeCertificateHash `json:"serverCertificateHashes,omitempty"`
	EventualAckIntervalMs   *int                      `json:"eventualAckIntervalMs,omitempty"`
	Fallback                *realtimeEndpoint         `json:"fallback,omitempty"`
}

// RealtimeConfigHandler returns an HTTP handler that serves client connection
// settings for the server's integrated transport.
func (s *Server) RealtimeConfigHandler(opts RealtimeConfigOptions) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if opts.WebSocketFallbackURL != "" && s.config.Transport != TransportWebTransport {
			http.Error(w, "golem: WebSocket fallback requires WebTransport primary", http.StatusInternalServerError)
			return
		}
		response := realtimeConfigResponse{
			Transport:             string(s.config.Transport),
			URL:                   opts.PublicURL,
			EventualAckIntervalMs: opts.EventualAckIntervalMs,
		}
		if opts.WebSocketFallbackURL != "" {
			response.Fallback = &realtimeEndpoint{
				Transport: string(TransportWebSocket),
				URL:       opts.WebSocketFallbackURL,
			}
		}
		if s.shouldIncludeServerCertificateHashes(opts) {
			for _, hash := range s.WebTransportCertificateHashes() {
				response.ServerCertificateHashes = append(response.ServerCertificateHashes, realtimeCertificateHash{
					Algorithm: hash.Algorithm,
					Value:     hex.EncodeToString(hash.Value),
				})
			}
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
	})
}

func (s *Server) shouldIncludeServerCertificateHashes(opts RealtimeConfigOptions) bool {
	if opts.IncludeServerCertificateHashes != nil {
		return *opts.IncludeServerCertificateHashes
	}
	return s.config.DevSelfSignedCert && s.config.TLSCertFile == "" && s.config.TLSKeyFile == ""
}
