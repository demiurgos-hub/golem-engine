package golem

import (
	"encoding/hex"
	"encoding/json"
	"net/http"
)

// RealtimeConfigOptions configures the browser bootstrap JSON served by
// Server.RealtimeConfigHandler.
type RealtimeConfigOptions struct {
	PublicURL             string
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

type realtimeConfigResponse struct {
	Transport               string                    `json:"transport"`
	URL                     string                    `json:"url"`
	ServerCertificateHashes []realtimeCertificateHash `json:"serverCertificateHashes,omitempty"`
	EventualAckIntervalMs   *int                      `json:"eventualAckIntervalMs,omitempty"`
}

// RealtimeConfigHandler returns an HTTP handler that serves client connection
// settings for the server's integrated transport.
func (s *Server) RealtimeConfigHandler(opts RealtimeConfigOptions) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := realtimeConfigResponse{
			Transport:             string(s.config.Transport),
			URL:                   opts.PublicURL,
			EventualAckIntervalMs: opts.EventualAckIntervalMs,
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
