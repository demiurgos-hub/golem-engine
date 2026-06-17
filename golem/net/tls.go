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
	"time"

	"github.com/quic-go/quic-go/http3"
)

// prepareWebTransportTLS resolves a TLS certificate and certificate hashes for WebTransport.
func prepareWebTransportTLS(cfg Config) (certificate tls.Certificate, hashes []CertificateHash, cleanup func(), err error) {
	cleanup = func() {}
	if cfg.TLSCertFile != "" || cfg.TLSKeyFile != "" {
		if cfg.TLSCertFile == "" || cfg.TLSKeyFile == "" {
			return tls.Certificate{}, nil, cleanup, errors.New("golem/net: webtransport requires both TLSCertFile and TLSKeyFile")
		}
		certificate, err = loadWebTransportCertificateFiles(cfg.TLSCertFile, cfg.TLSKeyFile)
		if err != nil {
			return tls.Certificate{}, nil, cleanup, fmt.Errorf("golem/net: loading WebTransport TLS certificate cert=%q key=%q: %w", cfg.TLSCertFile, cfg.TLSKeyFile, err)
		}
		hashes, err = certificateHashesFromPEM(cfg.TLSCertFile)
		if err != nil {
			return tls.Certificate{}, nil, cleanup, fmt.Errorf("golem/net: hashing WebTransport TLS certificate %q: %w", cfg.TLSCertFile, err)
		}
		return certificate, hashes, cleanup, nil
	}
	if !cfg.DevSelfSignedCert {
		return tls.Certificate{}, nil, cleanup, errors.New("golem/net: webtransport requires TLS certificates or DevSelfSignedCert; set TLSCertFile/TLSKeyFile for production or DevSelfSignedCert for local development")
	}
	certPEM, keyPEM, hashes, err := generateSelfSignedCertificate(cfg.Addr)
	if err != nil {
		return tls.Certificate{}, nil, cleanup, fmt.Errorf("golem/net: generating WebTransport dev self-signed certificate: %w", err)
	}
	certificate, err = loadWebTransportCertificate(certPEM, keyPEM)
	if err != nil {
		return tls.Certificate{}, nil, cleanup, fmt.Errorf("golem/net: loading generated WebTransport dev certificate: %w", err)
	}
	return certificate, hashes, cleanup, nil
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

func certificateHashesFromPEM(path string) ([]CertificateHash, error) {
	pemBytes, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var hashes []CertificateHash
	for len(pemBytes) > 0 {
		var block *pem.Block
		block, pemBytes = pem.Decode(pemBytes)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		sum := sha256.Sum256(block.Bytes)
		hashes = append(hashes, CertificateHash{
			Algorithm: "sha-256",
			Value:     append([]byte(nil), sum[:]...),
		})
	}
	if len(hashes) == 0 {
		return nil, fmt.Errorf("golem/net: no certificates found in %q", path)
	}
	return hashes, nil
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
