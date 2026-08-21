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
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	golemnet "github.com/demiurgos-hub/golem-engine/golem/net"
	"github.com/demiurgos-hub/golem-engine/golem/registry"
	"github.com/quic-go/quic-go/http3"
	"github.com/quic-go/webtransport-go"
)

func TestFetchRealtimeConfigDecodesJSON(t *testing.T) {
	sum := sha256.Sum256([]byte("cert"))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"transport": "webtransport",
			"url": "https://example.com/wt",
			"serverCertificateHashes": [{"algorithm": "sha-256", "value": "` + hex.EncodeToString(sum[:]) + `"}],
			"eventualAckIntervalMs": 25,
			"fallback": {"transport": "websocket", "url": "wss://example.com/ws"}
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
	if cfg.Fallback == nil || cfg.Fallback.Transport != TransportWebSocket || cfg.Fallback.URL != "wss://example.com/ws" {
		t.Fatalf("fallback = %+v", cfg.Fallback)
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
		{name: "transport", status: http.StatusOK, body: `{"transport":"bad","url":"https://example.com/wt"}`},
		{name: "url", status: http.StatusOK, body: `{"transport":"websocket","url":""}`},
		{name: "relative url", status: http.StatusOK, body: `{"transport":"websocket","url":"/api/ws"}`},
		{name: "websocket scheme", status: http.StatusOK, body: `{"transport":"websocket","url":"https://example.com/ws"}`},
		{name: "webtransport host", status: http.StatusOK, body: `{"transport":"webtransport","url":"https:///api/wt"}`},
		{name: "fallback primary", status: http.StatusOK, body: `{"transport":"websocket","url":"wss://example.com/ws","fallback":{"transport":"websocket","url":"wss://example.com/ws2"}}`},
		{name: "fallback transport", status: http.StatusOK, body: `{"transport":"webtransport","url":"https://example.com/wt","fallback":{"transport":"webtransport","url":"https://example.com/wt2"}}`},
		{name: "fallback url", status: http.StatusOK, body: `{"transport":"webtransport","url":"https://example.com/wt","fallback":{"transport":"websocket","url":""}}`},
		{name: "fallback scheme", status: http.StatusOK, body: `{"transport":"webtransport","url":"https://example.com/wt","fallback":{"transport":"websocket","url":"https://example.com/ws"}}`},
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

func TestFetchRealtimeConfigDoesNotExposeErrorBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte("missing realtime config"))
	}))
	defer server.Close()

	_, err := FetchRealtimeConfig(context.Background(), server.URL, nil)
	if err == nil {
		t.Fatal("FetchRealtimeConfig returned nil error")
	}
	if !strings.Contains(err.Error(), "status 404") {
		t.Fatalf("error = %v", err)
	}
	if strings.Contains(err.Error(), "missing realtime config") || strings.Contains(err.Error(), "body=") {
		t.Fatalf("error exposed untrusted response body: %v", err)
	}
}

func TestFetchRealtimeConfigDoesNotExposeCredentialBearingErrorBody(t *testing.T) {
	const requestSecret = "request-ticket-secret"
	const bodySecret = "body-token-secret"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprintf(w, "request https://example.test/realtime?ticket=%s rejected token=%s path=%s", requestSecret, bodySecret, r.URL.String())
	}))
	defer server.Close()

	_, err := FetchRealtimeConfig(context.Background(), server.URL+"?ticket="+requestSecret, nil)
	if err == nil {
		t.Fatal("FetchRealtimeConfig returned nil error")
	}
	if !strings.Contains(err.Error(), "status 401") {
		t.Fatalf("error lost response status diagnostic: %v", err)
	}
	for _, secret := range []string{requestSecret, bodySecret} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("error body leaked credential %q: %v", secret, err)
		}
	}
	if strings.Contains(err.Error(), "ticket=") || strings.Contains(err.Error(), "token=") || strings.Contains(err.Error(), "body=") {
		t.Fatalf("error exposed credential-bearing response body: %v", err)
	}
}

func TestFetchRealtimeConfigDoesNotLeakEndpointTokenInURLErrors(t *testing.T) {
	secret := "super-secret-token"
	endpoint := "http://127.0.0.1:1/realtime-config?token=" + secret + "#frag"
	_, err := FetchRealtimeConfig(context.Background(), endpoint, &http.Client{Timeout: 50 * time.Millisecond})
	if err == nil {
		t.Fatal("FetchRealtimeConfig returned nil error")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("error leaked token: %v", err)
	}
	if strings.Contains(err.Error(), "token=") {
		t.Fatalf("error leaked query: %v", err)
	}
	var uerr *url.Error
	if !errors.As(err, &uerr) {
		t.Fatalf("expected url.Error in chain, got %v", err)
	}
	if strings.Contains(uerr.URL, secret) || strings.Contains(uerr.URL, "token=") {
		t.Fatalf("url.Error.URL leaked token: %q", uerr.URL)
	}
}

func TestTransportDialErrorsDoNotLeakCredentialURLs(t *testing.T) {
	const secret = "realtime-ticket-secret"
	tests := []struct {
		name      string
		transport TransportKind
		endpoint  string
		dial      func(context.Context, ConnectOptions) error
	}{
		{
			name:      "websocket",
			transport: TransportWebSocket,
			endpoint:  "wss://example.test/%zz?ticket=" + secret,
			dial: func(ctx context.Context, options ConnectOptions) error {
				_, err := DialWebSocket(ctx, options)
				return err
			},
		},
		{
			name:      "webtransport",
			transport: TransportWebTransport,
			endpoint:  "https://example.test/%zz?ticket=" + secret,
			dial: func(ctx context.Context, options ConnectOptions) error {
				_, err := DialWebTransport(ctx, options)
				return err
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var logs bytes.Buffer
			previousWriter := log.Writer()
			log.SetOutput(&logs)
			defer log.SetOutput(previousWriter)

			err := tc.dial(context.Background(), ConnectOptions{Transport: tc.transport, URL: tc.endpoint})
			if err == nil {
				t.Fatal("dial returned nil error")
			}
			for label, text := range map[string]string{
				"returned error": err.Error(),
				"log output":     logs.String(),
			} {
				if strings.Contains(text, secret) || strings.Contains(text, "ticket=") {
					t.Fatalf("%s leaked realtime credential: %q", label, text)
				}
			}
			var uerr *url.Error
			if !errors.As(err, &uerr) {
				t.Fatalf("dial error lost url.Error: %v", err)
			}
			if uerr.URL != "<invalid-url>" {
				t.Fatalf("sanitized url.Error URL = %q, want <invalid-url>", uerr.URL)
			}
		})
	}
}

func TestServerNameErrorDoesNotLeakCredentialURL(t *testing.T) {
	const secret = "realtime-ticket-secret"
	_, err := serverNameFromEndpointURL("?ticket=" + secret)
	if err == nil {
		t.Fatal("serverNameFromEndpointURL returned nil error")
	}
	if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "ticket=") {
		t.Fatalf("server name error leaked realtime credential: %v", err)
	}
}

func TestFetchRealtimeConfigRejectsInvalidAckIntervals(t *testing.T) {
	tests := []string{
		`{"transport":"websocket","url":"ws://example.com/ws","eventualAckIntervalMs":-1}`,
		`{"transport":"websocket","url":"ws://example.com/ws","eventualAckIntervalMs":1.5}`,
		`{"transport":"websocket","url":"ws://example.com/ws","eventualAckIntervalMs":2147483648}`,
	}
	for _, body := range tests {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Write([]byte(body))
		}))
		if _, err := FetchRealtimeConfig(context.Background(), server.URL, nil); err == nil {
			server.Close()
			t.Fatalf("expected error for body %s", body)
		}
		server.Close()
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

func TestConnectOptionsFromRealtimeConfigRejectsEmptyURL(t *testing.T) {
	if _, err := ConnectOptionsFromRealtimeConfig(RealtimeConfig{Transport: TransportWebSocket, URL: "   "}); err == nil {
		t.Fatal("ConnectOptionsFromRealtimeConfig returned nil error")
	}
}

func TestConnectOptionsFromRealtimeConfigDeepCopiesHashBytes(t *testing.T) {
	sum := sha256.Sum256([]byte("cert"))
	value := append([]byte(nil), sum[:]...)
	cfg := RealtimeConfig{
		Transport:               TransportWebTransport,
		URL:                     "https://example.com/wt",
		ServerCertificateHashes: []CertificateHash{{Algorithm: certificateHashSHA256, Value: value}},
		EventualAckIntervalMs:   25,
	}
	options, err := ConnectOptionsFromRealtimeConfig(cfg)
	if err != nil {
		t.Fatalf("ConnectOptionsFromRealtimeConfig: %v", err)
	}
	if options.EventualAckIntervalMs != 25 {
		t.Fatalf("ack = %d, want 25", options.EventualAckIntervalMs)
	}
	value[0] ^= 0xff
	if got := options.ServerCertificateHashes[0].Value; bytes.Equal(got, value) || !bytes.Equal(got, sum[:]) {
		t.Fatalf("hash bytes were not deep-copied: got %x want %x (mutated source %x)", got, sum, value)
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
