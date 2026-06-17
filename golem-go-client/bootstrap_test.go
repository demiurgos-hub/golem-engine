package golemclient

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/quic-go/quic-go/http3"
	"github.com/quic-go/webtransport-go"
	golemnet "golem-engine/golem/net"
	"golem-engine/golem/registry"
)

func TestFetchRealtimeConfigDecodesJSON(t *testing.T) {
	sum := sha256.Sum256([]byte("cert"))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"transport": "webtransport",
			"url": "https://example.com/wt",
			"serverCertificateHashes": [{"algorithm": "sha-256", "value": "` + hex.EncodeToString(sum[:]) + `"}],
			"eventualAckIntervalMs": 25
		}`))
	}))
	defer server.Close()

	cfg, err := FetchRealtimeConfig(context.Background(), server.URL, nil)
	if err != nil {
		t.Fatalf("FetchRealtimeConfig: %v", err)
	}
	if cfg.Transport != TransportWebTransport {
		t.Fatalf("transport = %q, want %q", cfg.Transport, TransportWebTransport)
	}
	if cfg.URL != "https://example.com/wt" {
		t.Fatalf("url = %q", cfg.URL)
	}
	if cfg.EventualAckIntervalMs != 25 {
		t.Fatalf("eventual ack interval = %d, want 25", cfg.EventualAckIntervalMs)
	}
	if len(cfg.ServerCertificateHashes) != 1 || cfg.ServerCertificateHashes[0].Algorithm != certificateHashSHA256 {
		t.Fatalf("hashes = %+v", cfg.ServerCertificateHashes)
	}
	if got := cfg.ServerCertificateHashes[0].Value; string(got) != string(sum[:]) {
		t.Fatalf("hash value = %x, want %x", got, sum)
	}
}

func TestFetchRealtimeConfigRejectsInvalidResponses(t *testing.T) {
	sum := sha256.Sum256([]byte("cert"))
	tests := []struct {
		name   string
		status int
		body   string
	}{
		{name: "status", status: http.StatusInternalServerError, body: `{}`},
		{name: "json", status: http.StatusOK, body: `{`},
		{name: "hex", status: http.StatusOK, body: `{"transport":"webtransport","url":"https://example.com/wt","serverCertificateHashes":[{"algorithm":"sha-256","value":"not-hex"}]}`},
		{name: "algorithm", status: http.StatusOK, body: `{"transport":"webtransport","url":"https://example.com/wt","serverCertificateHashes":[{"algorithm":"sha-512","value":"` + hex.EncodeToString(sum[:]) + `"}]}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				w.Write([]byte(tt.body))
			}))
			defer server.Close()
			if _, err := FetchRealtimeConfig(context.Background(), server.URL, nil); err == nil {
				t.Fatal("FetchRealtimeConfig returned nil error")
			}
		})
	}
}

func TestFetchRealtimeConfigIncludesErrorBodySnippet(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte("missing realtime config"))
	}))
	defer server.Close()

	_, err := FetchRealtimeConfig(context.Background(), server.URL, nil)
	if err == nil {
		t.Fatal("FetchRealtimeConfig returned nil error")
	}
	if !strings.Contains(err.Error(), "status 404") || !strings.Contains(err.Error(), "missing realtime config") {
		t.Fatalf("error = %v", err)
	}
}

func TestConnectOptionsFromRealtimeConfigAppliesOptions(t *testing.T) {
	sum := sha256.Sum256([]byte("cert"))
	tlsConfig := &tls.Config{ServerName: "override.example.com"}
	cfg := RealtimeConfig{
		Transport:               TransportWebTransport,
		URL:                     "https://example.com/wt?room=a",
		ServerCertificateHashes: []CertificateHash{{Algorithm: certificateHashSHA256, Value: sum[:]}},
		EventualAckIntervalMs:   50,
	}
	options, err := ConnectOptionsFromRealtimeConfig(
		cfg,
		WithQueryParam("token", "abc"),
		WithQuery(url.Values{"char_id": []string{"7"}, "token": []string{"extra"}}),
		WithTLSClientConfig(tlsConfig),
	)
	if err != nil {
		t.Fatalf("ConnectOptionsFromRealtimeConfig: %v", err)
	}
	if options.Transport != TransportWebTransport || options.EventualAckIntervalMs != 50 {
		t.Fatalf("options = %+v", options)
	}
	if options.TLSClientConfig != tlsConfig {
		t.Fatal("TLSClientConfig was not applied")
	}
	u, err := url.Parse(options.URL)
	if err != nil {
		t.Fatalf("Parse URL: %v", err)
	}
	query := u.Query()
	if query.Get("room") != "a" || query.Get("char_id") != "7" {
		t.Fatalf("query = %v", query)
	}
	if got := query["token"]; len(got) != 2 || got[0] != "abc" || got[1] != "extra" {
		t.Fatalf("token query = %v", got)
	}
	if len(options.ServerCertificateHashes) != 1 || string(options.ServerCertificateHashes[0].Value) != string(sum[:]) {
		t.Fatalf("hashes = %+v", options.ServerCertificateHashes)
	}
}

func TestConnectOptionsFromRealtimeConfigRejectsUnsupportedTransport(t *testing.T) {
	if _, err := ConnectOptionsFromRealtimeConfig(RealtimeConfig{Transport: "bad", URL: "https://example.com/wt"}); err == nil {
		t.Fatal("ConnectOptionsFromRealtimeConfig returned nil error")
	}
}

func TestTLSClientConfigFromCertificateHashesPinsLeafCertificate(t *testing.T) {
	certificate, hash := newTestCertificate(t, "localhost")
	cfg, err := TLSClientConfigFromCertificateHashes("localhost", []CertificateHash{hash})
	if err != nil {
		t.Fatalf("TLSClientConfigFromCertificateHashes: %v", err)
	}
	if cfg == nil {
		t.Fatal("TLSClientConfigFromCertificateHashes returned nil")
	}
	if err := cfg.VerifyPeerCertificate(certificate.Certificate, nil); err != nil {
		t.Fatalf("VerifyPeerCertificate: %v", err)
	}

	wrongHash := hash
	wrongHash.Value = append([]byte(nil), hash.Value...)
	wrongHash.Value[0] ^= 0xff
	wrongCfg, err := TLSClientConfigFromCertificateHashes("localhost", []CertificateHash{wrongHash})
	if err != nil {
		t.Fatalf("TLSClientConfigFromCertificateHashes wrong hash: %v", err)
	}
	if err := wrongCfg.VerifyPeerCertificate(certificate.Certificate, nil); err == nil {
		t.Fatal("VerifyPeerCertificate accepted wrong hash")
	}

	hostCfg, err := TLSClientConfigFromCertificateHashes("otherhost", []CertificateHash{hash})
	if err != nil {
		t.Fatalf("TLSClientConfigFromCertificateHashes host mismatch: %v", err)
	}
	if err := hostCfg.VerifyPeerCertificate(certificate.Certificate, nil); err == nil {
		t.Fatal("VerifyPeerCertificate accepted wrong hostname")
	}
}

func TestTLSClientConfigFromCertificateHashesRejectsInvalidHashes(t *testing.T) {
	if _, err := TLSClientConfigFromCertificateHashes("localhost", []CertificateHash{{Algorithm: "sha-512", Value: make([]byte, sha256.Size)}}); err == nil {
		t.Fatal("unsupported algorithm returned nil error")
	}
	if _, err := TLSClientConfigFromCertificateHashes("localhost", []CertificateHash{{Algorithm: certificateHashSHA256, Value: []byte{1}}}); err == nil {
		t.Fatal("invalid hash length returned nil error")
	}
}

func TestDialWebTransportUsesCertificateHashes(t *testing.T) {
	transportURL, hash := startPinnedWebTransportServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ch, err := DialWebTransport(ctx, ConnectOptions{
		Transport:               TransportWebTransport,
		URL:                     transportURL,
		ServerCertificateHashes: []CertificateHash{hash},
	})
	if err != nil {
		t.Fatalf("DialWebTransport with hash: %v", err)
	}
	ch.Close()

	wrongHash := hash
	wrongHash.Value = append([]byte(nil), hash.Value...)
	wrongHash.Value[0] ^= 0xff
	if _, err := DialWebTransport(ctx, ConnectOptions{
		Transport:               TransportWebTransport,
		URL:                     transportURL,
		ServerCertificateHashes: []CertificateHash{wrongHash},
	}); err == nil {
		t.Fatal("DialWebTransport accepted wrong certificate hash")
	}

	ch, err = DialWebTransport(ctx, ConnectOptions{
		Transport:               TransportWebTransport,
		URL:                     transportURL,
		ServerCertificateHashes: []CertificateHash{wrongHash},
		TLSClientConfig:         &tls.Config{InsecureSkipVerify: true},
	})
	if err != nil {
		t.Fatalf("DialWebTransport with explicit TLS config: %v", err)
	}
	ch.Close()
}

func TestDialWebTransportPrimesReliableStreamForListenerAccept(t *testing.T) {
	listener := golemnet.NewListener(registry.NewRegistry(), golemnet.Config{
		Transport: golemnet.TransportWebTransport,
		Path:      "/wt",
	})

	upgradeSeen := make(chan string, 1)
	listener.OnUpgrade(func(r *http.Request) (any, error) {
		charID := r.URL.Query().Get("char_id")
		upgradeSeen <- charID
		return charID, nil
	})

	onConnectErr := make(chan error, 1)
	listener.OnConnect(func(sess *golemnet.Session) {
		if got, want := sess.Data, "go-client"; got != want {
			onConnectErr <- fmt.Errorf("session data = %v, want %q", got, want)
			return
		}
		if ids := listener.SessionIDs(); len(ids) != 1 || ids[0] != sess.ID {
			onConnectErr <- fmt.Errorf("session IDs = %v, want [%d]", ids, sess.ID)
			return
		}
		onConnectErr <- listener.Send(sess.ID, []byte("connected"))
	})

	transportURL := startGolemNetWebTransportListener(t, listener) + "?char_id=go-client"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ch, err := DialWebTransport(ctx, ConnectOptions{
		Transport:       TransportWebTransport,
		URL:             transportURL,
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, // Test-only local self-signed certificate.
	})
	if err != nil {
		t.Fatalf("DialWebTransport: %v", err)
	}
	defer ch.Close()

	messageSeen := make(chan []byte, 1)
	ch.OnMessage(func(data []byte) {
		messageSeen <- append([]byte(nil), data...)
	})

	select {
	case got := <-upgradeSeen:
		if got != "go-client" {
			t.Fatalf("upgrade char_id = %q, want %q", got, "go-client")
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for OnUpgrade")
	}

	select {
	case err := <-onConnectErr:
		if err != nil {
			t.Fatalf("OnConnect: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for OnConnect")
	}

	select {
	case got := <-messageSeen:
		if !bytes.Equal(got, []byte("connected")) {
			t.Fatalf("first server message = %q, want %q", got, []byte("connected"))
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for server message")
	}
}

func newTestCertificate(t *testing.T, host string) (tls.Certificate, CertificateHash) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	serialLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		t.Fatalf("Int: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   "golem-client-test",
			Organization: []string{"Golem Engine"},
		},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	if ip := net.ParseIP(host); ip != nil {
		template.IPAddresses = []net.IP{ip}
	} else {
		template.DNSNames = []string{host}
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("CreateCertificate: %v", err)
	}
	keyBytes, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		t.Fatalf("MarshalECPrivateKey: %v", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyBytes})
	certificate, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatalf("X509KeyPair: %v", err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("ParseCertificate: %v", err)
	}
	certificate.Leaf = leaf
	sum := sha256.Sum256(der)
	return certificate, CertificateHash{
		Algorithm: certificateHashSHA256,
		Value:     append([]byte(nil), sum[:]...),
	}
}

func startPinnedWebTransportServer(t *testing.T) (string, CertificateHash) {
	t.Helper()
	packetConn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket: %v", err)
	}
	host, _, err := net.SplitHostPort(packetConn.LocalAddr().String())
	if err != nil {
		t.Fatalf("SplitHostPort: %v", err)
	}
	certificate, hash := newTestCertificate(t, host)
	h3 := &http3.Server{}
	h3.EnableDatagrams = true
	webtransport.ConfigureHTTP3Server(h3)
	wtServer := &webtransport.Server{H3: h3}
	mux := http.NewServeMux()
	mux.HandleFunc("/wt", func(w http.ResponseWriter, r *http.Request) {
		session, err := wtServer.Upgrade(w, r)
		if err != nil {
			return
		}
		go func() {
			ctx, cancel := context.WithTimeout(session.Context(), 5*time.Second)
			defer cancel()
			stream, err := session.AcceptStream(ctx)
			if err != nil {
				return
			}
			defer stream.Close()
			<-session.Context().Done()
		}()
	})
	h3.Handler = mux
	h3.TLSConfig = http3.ConfigureTLSConfig(&tls.Config{Certificates: []tls.Certificate{certificate}})
	serveErr := make(chan error, 1)
	go func() {
		serveErr <- wtServer.Serve(packetConn)
	}()
	t.Cleanup(func() {
		_ = wtServer.Close()
		_ = packetConn.Close()
		select {
		case err := <-serveErr:
			if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, net.ErrClosed) {
				t.Fatalf("Serve: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for WebTransport test server shutdown")
		}
	})
	return "https://" + packetConn.LocalAddr().String() + "/wt", hash
}

func startGolemNetWebTransportListener(t *testing.T, listener *golemnet.Listener) string {
	t.Helper()
	packetConn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket: %v", err)
	}
	host, _, err := net.SplitHostPort(packetConn.LocalAddr().String())
	if err != nil {
		t.Fatalf("SplitHostPort: %v", err)
	}
	certificate, _ := newTestCertificate(t, host)
	h3 := &http3.Server{Handler: listener.Handler()}
	server := listener.WebTransportServer(h3)
	h3.TLSConfig = http3.ConfigureTLSConfig(&tls.Config{Certificates: []tls.Certificate{certificate}})

	serveErr := make(chan error, 1)
	go func() {
		serveErr <- server.Serve(packetConn)
	}()
	t.Cleanup(func() {
		_ = server.Close()
		_ = packetConn.Close()
		select {
		case err := <-serveErr:
			if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, net.ErrClosed) {
				t.Fatalf("Serve: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for WebTransport listener shutdown")
		}
	})
	return "https://" + packetConn.LocalAddr().String() + "/wt"
}
