package net

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"sync/atomic"
	"time"

	"github.com/quic-go/quic-go/http3"
)

// prepareWebTransportTLS resolves a TLS certificate and certificate hashes for WebTransport.
func prepareWebTransportTLS(cfg Config) (certificate tls.Certificate, hashes []CertificateHash, cleanup func(), err error) {
	cleanup = func() {}
	state, _, err := prepareWebTransportCertificate(cfg)
	if err != nil {
		return tls.Certificate{}, nil, cleanup, err
	}
	return *state.certificate, cloneCertificateHashes(state.hashes), cleanup, nil
}

// prepareWebTransportCertificate resolves the initial served certificate.
// For file certificates it also returns the file stamps observed before the
// files were read, so the reload poll can detect a later replacement.
func prepareWebTransportCertificate(cfg Config) (*servedCertificate, certificateFileStamps, error) {
	if cfg.TLSCertFile != "" || cfg.TLSKeyFile != "" {
		if cfg.TLSCertFile == "" || cfg.TLSKeyFile == "" {
			return nil, certificateFileStamps{}, errors.New("golem/net: webtransport requires both TLSCertFile and TLSKeyFile")
		}
		return loadServedCertificateFiles(cfg.TLSCertFile, cfg.TLSKeyFile)
	}
	if !cfg.DevSelfSignedCert {
		return nil, certificateFileStamps{}, errors.New("golem/net: webtransport requires TLS certificates or DevSelfSignedCert; set TLSCertFile/TLSKeyFile for production or DevSelfSignedCert for local development")
	}
	certPEM, keyPEM, _, err := generateSelfSignedCertificate(cfg.Addr)
	if err != nil {
		return nil, certificateFileStamps{}, fmt.Errorf("golem/net: generating WebTransport dev self-signed certificate: %w", err)
	}
	certificate, err := loadWebTransportCertificate(certPEM, keyPEM)
	if err != nil {
		return nil, certificateFileStamps{}, fmt.Errorf("golem/net: loading generated WebTransport dev certificate: %w", err)
	}
	return newServedCertificate(certificate), certificateFileStamps{}, nil
}

// loadServedCertificateFiles stats, reads and validates a certificate/key PEM
// pair. The stamps are taken before reading: if the files change while being
// read, the next poll sees a different stamp and loads again, so a replacement
// is never missed (at worst it is loaded twice).
func loadServedCertificateFiles(certPath string, keyPath string) (*servedCertificate, certificateFileStamps, error) {
	return loadNamedCertificateFiles(webTransportCertificateName, certPath, keyPath)
}

// webTransportCertificateName labels the integrated listener's certificate in
// errors and logs.
const webTransportCertificateName = "WebTransport"

// loadNamedCertificateFiles is loadServedCertificateFiles with the label used
// in its errors; name distinguishes the listener's certificate from other
// ReloadableCertificate users.
func loadNamedCertificateFiles(name string, certPath string, keyPath string) (*servedCertificate, certificateFileStamps, error) {
	stamps, err := statCertificateFiles(certPath, keyPath)
	if err != nil {
		return nil, certificateFileStamps{}, fmt.Errorf("golem/net: loading %s TLS certificate cert=%q key=%q: %w", name, certPath, keyPath, err)
	}
	certificate, err := loadWebTransportCertificateFiles(certPath, keyPath)
	if err != nil {
		return nil, certificateFileStamps{}, fmt.Errorf("golem/net: loading %s TLS certificate cert=%q key=%q: %w", name, certPath, keyPath, err)
	}
	return newServedCertificate(certificate), stamps, nil
}

// loadWebTransportCertificateFiles loads a certificate pair from PEM files.
func loadWebTransportCertificateFiles(certPath string, keyPath string) (tls.Certificate, error) {
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		return tls.Certificate{}, err
	}
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		return tls.Certificate{}, err
	}
	return loadWebTransportCertificate(certPEM, keyPEM)
}

// loadWebTransportCertificate parses a PEM certificate pair and caches the leaf certificate.
func loadWebTransportCertificate(certPEM []byte, keyPEM []byte) (tls.Certificate, error) {
	certificate, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return tls.Certificate{}, err
	}
	if len(certificate.Certificate) > 0 {
		leaf, err := x509.ParseCertificate(certificate.Certificate[0])
		if err != nil {
			return tls.Certificate{}, err
		}
		certificate.Leaf = leaf
	}
	return certificate, nil
}

// newWebTransportServerTLSConfig prepares a TLS config suitable for HTTP/3 serving.
func newWebTransportServerTLSConfig(certificate tls.Certificate) *tls.Config {
	return http3.ConfigureTLSConfig(&tls.Config{
		Certificates: []tls.Certificate{certificate},
	})
}

// newReloadableWebTransportServerTLSConfig prepares an HTTP/3 TLS config that
// serves the holder's current certificate on every handshake.
// http3.ConfigureTLSConfig clones the config, which keeps
// GetCertificate, and sets NextProtos to h3; it only wraps GetConfigForClient,
// which is not used here. quic-go's own server setup also preserves
// GetCertificate (it only wraps it to populate ClientHelloInfo.Conn).
func newReloadableWebTransportServerTLSConfig(holder *certificateHolder) *tls.Config {
	return http3.ConfigureTLSConfig(&tls.Config{
		GetCertificate: holder.getCertificate,
	})
}

// servedCertificate is one immutable certificate together with the hashes of
// its chain. It is swapped as a unit so a reader never pairs a certificate
// with another certificate's hashes.
type servedCertificate struct {
	certificate *tls.Certificate
	hashes      []CertificateHash
}

func newServedCertificate(certificate tls.Certificate) *servedCertificate {
	return &servedCertificate{
		certificate: &certificate,
		hashes:      certificateHashesFromDER(certificate.Certificate),
	}
}

// certificateHolder serves the current certificate to TLS handshakes and is
// swapped atomically while the listener (or a ReloadableCertificate) is in
// use. The zero value holds nothing.
type certificateHolder struct {
	current atomic.Pointer[servedCertificate]
}

func (h *certificateHolder) load() *servedCertificate { return h.current.Load() }

func (h *certificateHolder) store(state *servedCertificate) { h.current.Store(state) }

// getCertificate implements tls.Config.GetCertificate. Only new handshakes
// observe a swap; established connections keep their negotiated certificate.
func (h *certificateHolder) getCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	state := h.current.Load()
	if state == nil {
		return nil, errors.New("golem/net: no TLS certificate loaded")
	}
	return state.certificate, nil
}

// hashes returns a deep copy of the current certificate's hashes, or nil.
func (h *certificateHolder) hashes() []CertificateHash {
	state := h.current.Load()
	if state == nil {
		return nil
	}
	return cloneCertificateHashes(state.hashes)
}

func cloneCertificateHashes(hashes []CertificateHash) []CertificateHash {
	if len(hashes) == 0 {
		return nil
	}
	out := make([]CertificateHash, len(hashes))
	for i, hash := range hashes {
		out[i] = CertificateHash{
			Algorithm: hash.Algorithm,
			Value:     append([]byte(nil), hash.Value...),
		}
	}
	return out
}

// certificateFileStamp identifies one observed version of a file. Size is
// compared as well as mtime because filesystem mtime granularity can be coarse.
type certificateFileStamp struct {
	modTime time.Time
	size    int64
}

type certificateFileStamps struct {
	cert certificateFileStamp
	key  certificateFileStamp
}

func (s certificateFileStamps) equal(o certificateFileStamps) bool {
	return s.cert.size == o.cert.size && s.cert.modTime.Equal(o.cert.modTime) &&
		s.key.size == o.key.size && s.key.modTime.Equal(o.key.modTime)
}

func statCertificateFiles(certPath string, keyPath string) (certificateFileStamps, error) {
	certInfo, err := os.Stat(certPath)
	if err != nil {
		return certificateFileStamps{}, err
	}
	keyInfo, err := os.Stat(keyPath)
	if err != nil {
		return certificateFileStamps{}, err
	}
	return certificateFileStamps{
		cert: certificateFileStamp{modTime: certInfo.ModTime(), size: certInfo.Size()},
		key:  certificateFileStamp{modTime: keyInfo.ModTime(), size: keyInfo.Size()},
	}, nil
}

// certificateHashesFromDER returns SHA-256 hashes of every certificate in a
// DER chain. tls.X509KeyPair keeps exactly the PEM CERTIFICATE blocks, so this
// matches hashing those blocks from the certificate file.
func certificateHashesFromDER(chain [][]byte) []CertificateHash {
	hashes := make([]CertificateHash, 0, len(chain))
	for _, der := range chain {
		sum := sha256.Sum256(der)
		hashes = append(hashes, CertificateHash{
			Algorithm: "sha-256",
			Value:     append([]byte(nil), sum[:]...),
		})
	}
	return hashes
}

func generateSelfSignedCertificate(addr string) (certPEM []byte, keyPEM []byte, hashes []CertificateHash, err error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, nil, err
	}
	serialLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		return nil, nil, nil, err
	}
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   "golem-webtransport-dev",
			Organization: []string{"Golem Engine"},
		},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(7 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
	}
	if host, _, splitErr := net.SplitHostPort(addr); splitErr == nil {
		if host != "" && host != "0.0.0.0" && host != "::" && host != "[::]" {
			if ip := net.ParseIP(host); ip != nil {
				template.IPAddresses = append(template.IPAddresses, ip)
			} else {
				template.DNSNames = append(template.DNSNames, host)
			}
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &priv.PublicKey, priv)
	if err != nil {
		return nil, nil, nil, err
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyBytes, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return nil, nil, nil, err
	}
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyBytes})
	sum := sha256.Sum256(der)
	hashes = []CertificateHash{{
		Algorithm: "sha-256",
		Value:     append([]byte(nil), sum[:]...),
	}}
	return certPEM, keyPEM, hashes, nil
}
