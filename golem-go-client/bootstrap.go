package golemclient

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const certificateHashSHA256 = "sha-256"

// RealtimeConfig is the client-side shape served by golem.Server.RealtimeConfigHandler.
type RealtimeConfig struct {
	Transport               TransportKind
	URL                     string
	ServerCertificateHashes []CertificateHash
	EventualAckIntervalMs   int
}

type realtimeConfigResponse struct {
	Transport               string `json:"transport"`
	URL                     string `json:"url"`
	ServerCertificateHashes []struct {
		Algorithm string `json:"algorithm"`
		Value     string `json:"value"`
	} `json:"serverCertificateHashes"`
	EventualAckIntervalMs *int `json:"eventualAckIntervalMs"`
}

// FetchRealtimeConfig loads and decodes a realtime config JSON endpoint.
func FetchRealtimeConfig(ctx context.Context, endpoint string, client *http.Client) (RealtimeConfig, error) {
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return RealtimeConfig{}, fmt.Errorf("golem-go-client: creating realtime config request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return RealtimeConfig{}, fmt.Errorf("golem-go-client: fetching realtime config: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		if len(snippet) > 0 {
			return RealtimeConfig{}, fmt.Errorf("golem-go-client: fetching realtime config: status %d body=%q", resp.StatusCode, string(snippet))
		}
		return RealtimeConfig{}, fmt.Errorf("golem-go-client: fetching realtime config: status %d", resp.StatusCode)
	}
	var body realtimeConfigResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return RealtimeConfig{}, fmt.Errorf("golem-go-client: decoding realtime config: %w", err)
	}
	cfg := RealtimeConfig{
		Transport: TransportKind(body.Transport),
		URL:       body.URL,
	}
	if body.EventualAckIntervalMs != nil {
		cfg.EventualAckIntervalMs = *body.EventualAckIntervalMs
	}
	for _, hash := range body.ServerCertificateHashes {
		decoded, err := decodeCertificateHash(hash.Algorithm, hash.Value)
		if err != nil {
			return RealtimeConfig{}, err
		}
		cfg.ServerCertificateHashes = append(cfg.ServerCertificateHashes, decoded)
	}
	return cfg, nil
}

// ConnectOption customizes ConnectOptions built from RealtimeConfig.
type ConnectOption func(*connectOptionsBuilder) error

type connectOptionsBuilder struct {
	options *ConnectOptions
	query   url.Values
}

// WithQueryParam appends one query parameter to the realtime transport URL.
func WithQueryParam(key, value string) ConnectOption {
	return func(b *connectOptionsBuilder) error {
		if key == "" {
			return fmt.Errorf("golem-go-client: query parameter key cannot be empty")
		}
		if b.query == nil {
			b.query = url.Values{}
		}
		b.query.Add(key, value)
		return nil
	}
}

// WithQuery appends query parameters to the realtime transport URL.
func WithQuery(values url.Values) ConnectOption {
	return func(b *connectOptionsBuilder) error {
		if b.query == nil {
			b.query = url.Values{}
		}
		for key, list := range values {
			if key == "" {
				return fmt.Errorf("golem-go-client: query parameter key cannot be empty")
			}
			for _, value := range list {
				b.query.Add(key, value)
			}
		}
		return nil
	}
}

// WithTLSClientConfig sets an explicit TLS config for the built ConnectOptions.
func WithTLSClientConfig(cfg *tls.Config) ConnectOption {
	return func(b *connectOptionsBuilder) error {
		b.options.TLSClientConfig = cfg
		return nil
	}
}

// ConnectOptionsFromRealtimeConfig converts realtime config JSON into ConnectOptions.
func ConnectOptionsFromRealtimeConfig(cfg RealtimeConfig, opts ...ConnectOption) (ConnectOptions, error) {
	switch cfg.Transport {
	case TransportWebSocket, TransportWebTransport:
	default:
		return ConnectOptions{}, fmt.Errorf("golem-go-client: unsupported transport %q", cfg.Transport)
	}
	options := ConnectOptions{
		Transport:               cfg.Transport,
		URL:                     cfg.URL,
		ServerCertificateHashes: append([]CertificateHash(nil), cfg.ServerCertificateHashes...),
		EventualAckIntervalMs:   cfg.EventualAckIntervalMs,
	}
	builder := &connectOptionsBuilder{options: &options}
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		if err := opt(builder); err != nil {
			return ConnectOptions{}, err
		}
	}
	if len(builder.query) > 0 {
		u, err := url.Parse(options.URL)
		if err != nil {
			return ConnectOptions{}, fmt.Errorf("golem-go-client: parsing realtime transport URL: %w", err)
		}
		query := u.Query()
		for key, list := range builder.query {
			for _, value := range list {
				query.Add(key, value)
			}
		}
		u.RawQuery = query.Encode()
		options.URL = u.String()
	}
	return options, nil
}

// TLSClientConfigFromCertificateHashes builds a TLS config pinned to certificate hashes.
func TLSClientConfigFromCertificateHashes(serverName string, hashes []CertificateHash) (*tls.Config, error) {
	if len(hashes) == 0 {
		return nil, nil
	}
	if err := validateCertificateHashes(hashes); err != nil {
		return nil, err
	}
	pinned := append([]CertificateHash(nil), hashes...)
	return &tls.Config{
		ServerName:         serverName,
		InsecureSkipVerify: true,
		VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			return verifyPinnedPeerCertificate(serverName, pinned, rawCerts)
		},
	}, nil
}

func decodeCertificateHash(algorithm, value string) (CertificateHash, error) {
	algorithm = normalizeCertificateHashAlgorithm(algorithm)
	if algorithm != certificateHashSHA256 {
		return CertificateHash{}, fmt.Errorf("golem-go-client: unsupported certificate hash algorithm %q", algorithm)
	}
	decoded, err := hex.DecodeString(value)
	if err != nil {
		return CertificateHash{}, fmt.Errorf("golem-go-client: decoding certificate hash: %w", err)
	}
	hash := CertificateHash{Algorithm: algorithm, Value: decoded}
	if err := validateCertificateHashes([]CertificateHash{hash}); err != nil {
		return CertificateHash{}, err
	}
	return hash, nil
}

func validateCertificateHashes(hashes []CertificateHash) error {
	for _, hash := range hashes {
		algorithm := normalizeCertificateHashAlgorithm(hash.Algorithm)
		switch algorithm {
		case certificateHashSHA256:
			if len(hash.Value) != sha256.Size {
				return fmt.Errorf("golem-go-client: %s certificate hash length %d, want %d", certificateHashSHA256, len(hash.Value), sha256.Size)
			}
		default:
			return fmt.Errorf("golem-go-client: unsupported certificate hash algorithm %q", hash.Algorithm)
		}
	}
	return nil
}

func verifyPinnedPeerCertificate(serverName string, hashes []CertificateHash, rawCerts [][]byte) error {
	if len(rawCerts) == 0 {
		return fmt.Errorf("golem-go-client: server sent no certificates")
	}
	leaf, err := x509.ParseCertificate(rawCerts[0])
	if err != nil {
		return fmt.Errorf("golem-go-client: parsing server certificate: %w", err)
	}
	if !matchesCertificateHash(rawCerts[0], hashes) {
		return fmt.Errorf("golem-go-client: server certificate did not match configured hashes")
	}

	intermediates := x509.NewCertPool()
	for _, raw := range rawCerts[1:] {
		cert, err := x509.ParseCertificate(raw)
		if err != nil {
			return fmt.Errorf("golem-go-client: parsing server certificate chain: %w", err)
		}
		intermediates.AddCert(cert)
	}
	verifyOptions := x509.VerifyOptions{
		DNSName:       serverName,
		Intermediates: intermediates,
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	if _, err := leaf.Verify(verifyOptions); err == nil {
		return nil
	}

	if serverName != "" {
		if err := leaf.VerifyHostname(serverName); err != nil {
			return fmt.Errorf("golem-go-client: pinned server certificate hostname mismatch: %w", err)
		}
	}
	now := time.Now()
	if now.Before(leaf.NotBefore) || now.After(leaf.NotAfter) {
		return fmt.Errorf("golem-go-client: pinned server certificate is not valid at %s", now.Format(time.RFC3339))
	}
	if !certificateAllowsServerAuth(leaf) {
		return fmt.Errorf("golem-go-client: pinned server certificate is not valid for server auth")
	}
	return nil
}

func matchesCertificateHash(der []byte, hashes []CertificateHash) bool {
	sum := sha256.Sum256(der)
	for _, hash := range hashes {
		if normalizeCertificateHashAlgorithm(hash.Algorithm) != certificateHashSHA256 {
			continue
		}
		if subtle.ConstantTimeCompare(sum[:], hash.Value) == 1 {
			return true
		}
	}
	return false
}

func certificateAllowsServerAuth(cert *x509.Certificate) bool {
	if len(cert.ExtKeyUsage) == 0 {
		return true
	}
	for _, usage := range cert.ExtKeyUsage {
		if usage == x509.ExtKeyUsageServerAuth || usage == x509.ExtKeyUsageAny {
			return true
		}
	}
	return false
}

func normalizeCertificateHashAlgorithm(algorithm string) string {
	return strings.ToLower(strings.TrimSpace(algorithm))
}

func serverNameFromEndpointURL(endpoint string) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", fmt.Errorf("golem-go-client: parsing WebTransport URL: %w", err)
	}
	if u.Hostname() == "" {
		return "", fmt.Errorf("golem-go-client: WebTransport URL %q has no host", endpoint)
	}
	return u.Hostname(), nil
}
