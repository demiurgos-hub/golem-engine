package golemclient

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestConnectionPlanSkipsUnsupportedWebTransportBeforeResolving(t *testing.T) {
	var resolved []TransportKind
	var dialed []TransportKind
	client := NewGameClient(GameClientOptions{
		SupportsTransport: func(transport TransportKind) bool {
			return transport == TransportWebSocket
		},
		CreateChannel: func(_ context.Context, options ConnectOptions) (ReliableMessageChannel, error) {
			dialed = append(dialed, options.Transport)
			return &fakeChannel{connected: true}, nil
		},
	})
	connected := 0
	client.OnConnect(func() { connected++ })
	fallback := ConnectOptions{Transport: TransportWebSocket, URL: "wss://example.test/ws"}
	err := client.ConnectPlan(context.Background(), ConnectionPlan{
		Primary:  ConnectOptions{Transport: TransportWebTransport, URL: "https://example.test/wt"},
		Fallback: &fallback,
		ResolveOptions: func(_ context.Context, candidate ConnectOptions) (ConnectOptions, error) {
			resolved = append(resolved, candidate.Transport)
			return ApplyConnectOptions(candidate, WithQueryParam("ticket", "fresh"))
		},
	})
	if err != nil {
		t.Fatalf("ConnectPlan: %v", err)
	}
	if got, want := resolved, []TransportKind{TransportWebSocket}; !equalTransports(got, want) {
		t.Fatalf("resolved transports = %v, want %v", got, want)
	}
	if got, want := dialed, []TransportKind{TransportWebSocket}; !equalTransports(got, want) {
		t.Fatalf("dialed transports = %v, want %v", got, want)
	}
	if connected != 1 {
		t.Fatalf("OnConnect calls = %d, want 1", connected)
	}
	if got := client.ConnectedTransport(); got != TransportWebSocket {
		t.Fatalf("ConnectedTransport = %q, want websocket", got)
	}
}

func TestConnectionPlanFallsBackWithFreshResolvedOptions(t *testing.T) {
	var resolverCalls int
	var dialed []ConnectOptions
	client := NewGameClient(GameClientOptions{
		CreateChannel: func(_ context.Context, options ConnectOptions) (ReliableMessageChannel, error) {
			dialed = append(dialed, cloneConnectOptions(options))
			if options.Transport == TransportWebTransport {
				return nil, errors.New("webtransport handshake failed")
			}
			return &fakeChannel{connected: true}, nil
		},
	})
	fallback := ConnectOptions{Transport: TransportWebSocket, URL: "wss://example.test/ws"}
	err := client.ConnectPlan(context.Background(), ConnectionPlan{
		Primary:  ConnectOptions{Transport: TransportWebTransport, URL: "https://example.test/wt"},
		Fallback: &fallback,
		ResolveOptions: func(_ context.Context, candidate ConnectOptions) (ConnectOptions, error) {
			resolverCalls++
			return ApplyConnectOptions(candidate, WithQueryParam("ticket", string(rune('0'+resolverCalls))))
		},
	})
	if err != nil {
		t.Fatalf("ConnectPlan: %v", err)
	}
	if resolverCalls != 2 || len(dialed) != 2 {
		t.Fatalf("resolver/dial calls = %d/%d, want 2/2", resolverCalls, len(dialed))
	}
	first, _ := url.Parse(dialed[0].URL)
	second, _ := url.Parse(dialed[1].URL)
	if first.Query().Get("ticket") != "1" || second.Query().Get("ticket") != "2" {
		t.Fatalf("tickets = %q/%q, want distinct 1/2", first.Query().Get("ticket"), second.Query().Get("ticket"))
	}
}

func TestConnectionPlanResolverFailureAbortsWithoutFallbackAndRedacts(t *testing.T) {
	const secret = "resolver-ticket-secret"
	rawErr := errors.New("could not mint ticket " + secret)
	var dials int
	client := NewGameClient(GameClientOptions{
		CreateChannel: func(context.Context, ConnectOptions) (ReliableMessageChannel, error) {
			dials++
			return &fakeChannel{connected: true}, nil
		},
	})
	fallback := ConnectOptions{Transport: TransportWebSocket, URL: "wss://example.test/ws"}
	err := client.ConnectPlan(context.Background(), ConnectionPlan{
		Primary:        ConnectOptions{Transport: TransportWebTransport, URL: "https://example.test/wt"},
		Fallback:       &fallback,
		ResolveOptions: func(context.Context, ConnectOptions) (ConnectOptions, error) { return ConnectOptions{}, rawErr },
	})
	if err == nil || !errors.Is(err, rawErr) {
		t.Fatalf("ConnectPlan error = %v, want wrapped resolver error", err)
	}
	if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "ticket") {
		t.Fatalf("resolver error leaked credential: %v", err)
	}
	if dials != 0 {
		t.Fatalf("dial calls = %d, want 0", dials)
	}
}

func TestConnectionPlanRejectsResolverTransportMutation(t *testing.T) {
	var dials int
	client := NewGameClient(GameClientOptions{
		CreateChannel: func(context.Context, ConnectOptions) (ReliableMessageChannel, error) {
			dials++
			return &fakeChannel{connected: true}, nil
		},
	})
	fallback := ConnectOptions{Transport: TransportWebSocket, URL: "wss://example.test/ws"}
	err := client.ConnectPlan(context.Background(), ConnectionPlan{
		Primary:  ConnectOptions{Transport: TransportWebTransport, URL: "https://example.test/wt"},
		Fallback: &fallback,
		ResolveOptions: func(_ context.Context, candidate ConnectOptions) (ConnectOptions, error) {
			candidate.Transport = TransportWebSocket
			return candidate, nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "changed the candidate transport") {
		t.Fatalf("ConnectPlan error = %v, want transport-mutation error", err)
	}
	if dials != 0 {
		t.Fatalf("dial calls = %d, want 0", dials)
	}
}

func TestConnectionPlanRejectsResolverEndpointMetadataMutation(t *testing.T) {
	base := ConnectOptions{
		Transport:               TransportWebTransport,
		URL:                     "https://example.test/wt",
		ServerCertificateHashes: []CertificateHash{{Algorithm: "sha-256", Value: make([]byte, 32)}},
		EventualAckIntervalMs:   5,
	}
	for _, tc := range []struct {
		name   string
		mutate func(*ConnectOptions)
	}{
		{name: "endpoint", mutate: func(options *ConnectOptions) { options.URL = "https://other.test/wt?ticket=x" }},
		{name: "ack", mutate: func(options *ConnectOptions) { options.EventualAckIntervalMs++ }},
		{name: "hash", mutate: func(options *ConnectOptions) { options.ServerCertificateHashes[0].Value[0] = 1 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var dials int
			client := NewGameClient(GameClientOptions{
				CreateChannel: func(context.Context, ConnectOptions) (ReliableMessageChannel, error) {
					dials++
					return &fakeChannel{connected: true}, nil
				},
			})
			err := client.ConnectPlan(context.Background(), ConnectionPlan{
				Primary: base,
				ResolveOptions: func(_ context.Context, candidate ConnectOptions) (ConnectOptions, error) {
					tc.mutate(&candidate)
					return candidate, nil
				},
			})
			if err == nil {
				t.Fatal("ConnectPlan unexpectedly accepted resolver metadata mutation")
			}
			if dials != 0 {
				t.Fatalf("dial calls = %d, want 0", dials)
			}
		})
	}
}

func TestConnectionPlanDoesNotFallbackAfterTerminalHandshakeErrors(t *testing.T) {
	for _, terminalErr := range []error{ErrRealtimeRevisionMismatch, ErrRealtimeAuthorizationRejected} {
		t.Run(terminalErr.Error(), func(t *testing.T) {
			var dialed []TransportKind
			client := NewGameClient(GameClientOptions{
				CreateChannel: func(_ context.Context, options ConnectOptions) (ReliableMessageChannel, error) {
					dialed = append(dialed, options.Transport)
					return nil, fmt.Errorf("handshake rejected: %w", terminalErr)
				},
			})
			fallback := ConnectOptions{Transport: TransportWebSocket, URL: "wss://example.test/ws"}
			err := client.ConnectPlan(context.Background(), ConnectionPlan{
				Primary:  ConnectOptions{Transport: TransportWebTransport, URL: "https://example.test/wt"},
				Fallback: &fallback,
			})
			if err == nil || !errors.Is(err, terminalErr) {
				t.Fatalf("ConnectPlan error = %v, want %v", err, terminalErr)
			}
			if got, want := dialed, []TransportKind{TransportWebTransport}; !equalTransports(got, want) {
				t.Fatalf("dialed transports = %v, want %v", got, want)
			}
		})
	}
}

func TestConnectionPlanTimeoutClosesLateWebTransportAndFallsBack(t *testing.T) {
	webTransportStarted := make(chan struct{})
	webTransportCanceled := make(chan struct{})
	releaseWebTransport := make(chan struct{})
	closeStarted := make(chan struct{})
	releaseClose := make(chan struct{})
	fallbackResolved := make(chan struct{})
	fallbackDialed := make(chan struct{})
	late := newBlockingClosePlanChannel(closeStarted, releaseClose)
	var resolverCalls atomic.Int32
	var connectedCalls atomic.Int32
	client := NewGameClient(GameClientOptions{
		CreateChannel: func(ctx context.Context, options ConnectOptions) (ReliableMessageChannel, error) {
			if options.Transport == TransportWebTransport {
				close(webTransportStarted)
				<-ctx.Done()
				close(webTransportCanceled)
				<-releaseWebTransport // Deliberately return late after cancellation.
				return late, nil
			}
			close(fallbackDialed)
			return newTrackedPlanChannel(), nil
		},
	})
	client.OnConnect(func() { connectedCalls.Add(1) })
	fallback := ConnectOptions{Transport: TransportWebSocket, URL: "wss://example.test/ws"}
	result := make(chan error, 1)
	go func() {
		result <- client.ConnectPlan(context.Background(), ConnectionPlan{
			Primary:             ConnectOptions{Transport: TransportWebTransport, URL: "https://example.test/wt"},
			Fallback:            &fallback,
			WebTransportTimeout: 20 * time.Millisecond,
			ResolveOptions: func(_ context.Context, candidate ConnectOptions) (ConnectOptions, error) {
				resolverCalls.Add(1)
				if candidate.Transport == TransportWebSocket {
					close(fallbackResolved)
				}
				return candidate, nil
			},
		})
	}()

	waitForPlanSignal(t, webTransportStarted, "WebTransport factory start")
	waitForPlanSignal(t, webTransportCanceled, "WebTransport attempt cancellation")
	assertPlanSignalPending(t, fallbackResolved, "WebSocket resolver ran before the canceled WebTransport factory returned")
	assertPlanResultPending(t, result)

	close(releaseWebTransport)
	waitForPlanSignal(t, closeStarted, "WebTransport channel close")
	assertPlanSignalPending(t, fallbackResolved, "WebSocket resolver ran before WebTransport close completed")
	assertPlanSignalPending(t, fallbackDialed, "WebSocket dial ran before WebTransport close completed")
	assertPlanResultPending(t, result)

	close(releaseClose)
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("ConnectPlan: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("ConnectPlan did not finish after WebTransport close completed")
	}
	if resolverCalls.Load() != 2 {
		t.Fatalf("resolver calls = %d, want 2", resolverCalls.Load())
	}
	if connectedCalls.Load() != 1 {
		t.Fatalf("OnConnect calls = %d, want 1", connectedCalls.Load())
	}
	if late.closeCalls.Load() != 1 {
		t.Fatalf("late WebTransport close calls = %d, want 1", late.closeCalls.Load())
	}
	if got := client.ConnectedTransport(); got != TransportWebSocket {
		t.Fatalf("ConnectedTransport = %q, want websocket", got)
	}
}

func TestConnectionPlanRejectsChannelClosedBeforeCallbackInstallation(t *testing.T) {
	channel := newCloseBeforeCallbackPlanChannel()
	client := NewGameClient(GameClientOptions{
		CreateChannel: func(context.Context, ConnectOptions) (ReliableMessageChannel, error) {
			return channel, nil
		},
	})
	var connectedCalls atomic.Int32
	var disconnectedCalls atomic.Int32
	client.OnConnect(func() { connectedCalls.Add(1) })
	client.OnDisconnect(func(DisconnectInfo) { disconnectedCalls.Add(1) })

	err := client.ConnectPlan(context.Background(), ConnectionPlan{
		Primary: ConnectOptions{Transport: TransportWebTransport, URL: "https://example.test/wt"},
	})
	if err == nil || !strings.Contains(err.Error(), "closed before client callback installation") {
		t.Fatalf("ConnectPlan error = %v, want early-close error", err)
	}
	if got := client.ConnectedTransport(); got != "" {
		t.Fatalf("ConnectedTransport = %q, want empty after early close", got)
	}
	if connectedCalls.Load() != 0 {
		t.Fatalf("OnConnect calls = %d, want 0", connectedCalls.Load())
	}
	if disconnectedCalls.Load() != 0 {
		t.Fatalf("OnDisconnect calls = %d, want 0 for a pre-install close", disconnectedCalls.Load())
	}
	if channel.closeCalls.Load() != 1 {
		t.Fatalf("channel close calls = %d, want 1", channel.closeCalls.Load())
	}
}

func TestConnectionPlanKeepsSuccessfulWebSocketStickyUntilPlanChanges(t *testing.T) {
	var mu sync.Mutex
	var dialed []TransportKind
	var opened []*fakeChannel
	webTransportFailures := 1
	client := NewGameClient(GameClientOptions{
		CreateChannel: func(_ context.Context, options ConnectOptions) (ReliableMessageChannel, error) {
			mu.Lock()
			defer mu.Unlock()
			dialed = append(dialed, options.Transport)
			if options.Transport == TransportWebTransport && webTransportFailures > 0 {
				webTransportFailures--
				return nil, errors.New("blocked")
			}
			channel := &fakeChannel{connected: true}
			opened = append(opened, channel)
			return channel, nil
		},
	})
	fallback := ConnectOptions{Transport: TransportWebSocket, URL: "wss://example.test/ws"}
	plan := ConnectionPlan{
		Primary:  ConnectOptions{Transport: TransportWebTransport, URL: "https://example.test/wt"},
		Fallback: &fallback,
	}
	if err := client.ConnectPlan(context.Background(), plan); err != nil {
		t.Fatalf("first ConnectPlan: %v", err)
	}
	opened[0].callbacks.onClose(DisconnectInfo{WasClean: false, Err: errors.New("network lost")})
	if err := client.ConnectPlan(context.Background(), plan); err != nil {
		t.Fatalf("sticky ConnectPlan: %v", err)
	}
	if got, want := dialed, []TransportKind{TransportWebTransport, TransportWebSocket, TransportWebSocket}; !equalTransports(got, want) {
		t.Fatalf("dialed transports = %v, want %v", got, want)
	}

	opened[len(opened)-1].callbacks.onClose(DisconnectInfo{WasClean: false, Err: errors.New("network lost")})
	changedFallback := fallback
	changedFallback.URL = "wss://example.test/other-ws"
	changed := plan
	changed.Fallback = &changedFallback
	if err := client.ConnectPlan(context.Background(), changed); err != nil {
		t.Fatalf("changed ConnectPlan: %v", err)
	}
	if got := dialed[len(dialed)-1]; got != TransportWebTransport {
		t.Fatalf("first dial after plan change = %q, want webtransport", got)
	}
	if got := client.ConnectedTransport(); got != TransportWebTransport {
		t.Fatalf("ConnectedTransport = %q, want webtransport", got)
	}
}

func TestConnectionPlanWebSocketAffinitySurvivesReconnectFailure(t *testing.T) {
	var dialed []TransportKind
	var active *fakeChannel
	webSocketAttempts := 0
	client := NewGameClient(GameClientOptions{
		CreateChannel: func(_ context.Context, options ConnectOptions) (ReliableMessageChannel, error) {
			dialed = append(dialed, options.Transport)
			if options.Transport == TransportWebTransport {
				return nil, errors.New("webtransport unavailable")
			}
			webSocketAttempts++
			if webSocketAttempts == 2 {
				return nil, errors.New("temporary websocket failure")
			}
			active = &fakeChannel{connected: true}
			return active, nil
		},
	})
	fallback := ConnectOptions{Transport: TransportWebSocket, URL: "wss://example.test/ws"}
	plan := ConnectionPlan{
		Primary:  ConnectOptions{Transport: TransportWebTransport, URL: "https://example.test/wt"},
		Fallback: &fallback,
	}
	if err := client.ConnectPlan(context.Background(), plan); err != nil {
		t.Fatalf("initial ConnectPlan: %v", err)
	}
	active.callbacks.onClose(DisconnectInfo{WasClean: false, Err: errors.New("network lost")})
	if err := client.ConnectPlan(context.Background(), plan); err == nil {
		t.Fatal("sticky reconnect unexpectedly succeeded")
	}
	if err := client.ConnectPlan(context.Background(), plan); err != nil {
		t.Fatalf("second sticky reconnect: %v", err)
	}
	if got, want := dialed, []TransportKind{
		TransportWebTransport,
		TransportWebSocket,
		TransportWebSocket,
		TransportWebSocket,
	}; !equalTransports(got, want) {
		t.Fatalf("dialed transports = %v, want %v", got, want)
	}
}

func TestDirectConnectResetsWebSocketPlanAffinity(t *testing.T) {
	var dialed []TransportKind
	client := NewGameClient(GameClientOptions{
		CreateChannel: func(_ context.Context, options ConnectOptions) (ReliableMessageChannel, error) {
			dialed = append(dialed, options.Transport)
			if options.Transport == TransportWebTransport {
				return nil, errors.New("webtransport unavailable")
			}
			return &fakeChannel{connected: true}, nil
		},
	})
	fallback := ConnectOptions{Transport: TransportWebSocket, URL: "wss://example.test/ws"}
	plan := ConnectionPlan{
		Primary:  ConnectOptions{Transport: TransportWebTransport, URL: "https://example.test/wt"},
		Fallback: &fallback,
	}
	if err := client.ConnectPlan(context.Background(), plan); err != nil {
		t.Fatalf("initial ConnectPlan: %v", err)
	}
	if err := client.Connect(context.Background(), ConnectOptions{Transport: TransportWebSocket, URL: "wss://direct.example/ws"}); err != nil {
		t.Fatalf("direct Connect: %v", err)
	}
	client.Disconnect()
	if err := client.ConnectPlan(context.Background(), plan); err != nil {
		t.Fatalf("ConnectPlan after direct connect: %v", err)
	}
	if got, want := dialed, []TransportKind{
		TransportWebTransport,
		TransportWebSocket,
		TransportWebSocket,
		TransportWebTransport,
		TransportWebSocket,
	}; !equalTransports(got, want) {
		t.Fatalf("dialed transports = %v, want %v", got, want)
	}
}

func TestConnectionPlanFromRealtimeConfigClonesFallbackAndHashes(t *testing.T) {
	hashBytes := bytes.Repeat([]byte{7}, 32)
	fallback := &RealtimeEndpoint{Transport: TransportWebSocket, URL: "wss://example.test/ws"}
	cfg := RealtimeConfig{
		Transport:               TransportWebTransport,
		URL:                     "https://example.test/wt",
		ServerCertificateHashes: []CertificateHash{{Algorithm: certificateHashSHA256, Value: hashBytes}},
		EventualAckIntervalMs:   25,
		Fallback:                fallback,
	}
	plan, err := ConnectionPlanFromRealtimeConfig(cfg)
	if err != nil {
		t.Fatalf("ConnectionPlanFromRealtimeConfig: %v", err)
	}
	hashBytes[0] = 9
	fallback.URL = "wss://mutated.invalid/ws"
	if plan.Primary.ServerCertificateHashes[0].Value[0] != 7 {
		t.Fatal("primary certificate hashes were not cloned")
	}
	if plan.Fallback == nil || plan.Fallback.URL != "wss://example.test/ws" {
		t.Fatalf("fallback = %+v, want original endpoint", plan.Fallback)
	}
	if len(plan.Fallback.ServerCertificateHashes) != 0 || plan.Fallback.EventualAckIntervalMs != 0 {
		t.Fatalf("fallback inherited WebTransport-only metadata: %+v", plan.Fallback)
	}
}

func TestConnectionPlanWebSocketOnlyReconnectDoesNotCreateFallbackAffinity(t *testing.T) {
	var dialed []TransportKind
	client := NewGameClient(GameClientOptions{
		CreateChannel: func(_ context.Context, options ConnectOptions) (ReliableMessageChannel, error) {
			dialed = append(dialed, options.Transport)
			return &fakeChannel{connected: true}, nil
		},
	})
	plan := ConnectionPlan{
		Primary: ConnectOptions{Transport: TransportWebSocket, URL: "wss://example.test/ws"},
	}
	if err := client.ConnectPlan(context.Background(), plan); err != nil {
		t.Fatalf("first ConnectPlan: %v", err)
	}
	if err := client.ConnectPlan(context.Background(), plan); err != nil {
		t.Fatalf("second ConnectPlan: %v", err)
	}
	if got, want := dialed, []TransportKind{TransportWebSocket, TransportWebSocket}; !equalTransports(got, want) {
		t.Fatalf("dialed transports = %v, want %v", got, want)
	}
	if client.affinityKey != "" || client.affinityTransport != "" {
		t.Fatalf("WebSocket-only plan retained fallback affinity %q/%q", client.affinityKey, client.affinityTransport)
	}
}

func TestConnectionPlanRejectsInvalidEndpointURLsBeforeResolvingOrDialing(t *testing.T) {
	tests := []struct {
		name    string
		options ConnectOptions
	}{
		{name: "webtransport relative", options: ConnectOptions{Transport: TransportWebTransport, URL: "/api/wt"}},
		{name: "webtransport missing host", options: ConnectOptions{Transport: TransportWebTransport, URL: "https:///api/wt"}},
		{name: "webtransport wrong scheme", options: ConnectOptions{Transport: TransportWebTransport, URL: "http://example.test/api/wt"}},
		{name: "websocket missing host", options: ConnectOptions{Transport: TransportWebSocket, URL: "wss:///api/ws"}},
		{name: "websocket wrong scheme", options: ConnectOptions{Transport: TransportWebSocket, URL: "https://example.test/api/ws"}},
		{name: "userinfo", options: ConnectOptions{Transport: TransportWebSocket, URL: "wss://user:password@example.test/api/ws"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var resolverCalls int
			var dialCalls int
			client := NewGameClient(GameClientOptions{
				CreateChannel: func(context.Context, ConnectOptions) (ReliableMessageChannel, error) {
					dialCalls++
					return &fakeChannel{connected: true}, nil
				},
			})
			err := client.ConnectPlan(context.Background(), ConnectionPlan{
				Primary: tt.options,
				ResolveOptions: func(_ context.Context, candidate ConnectOptions) (ConnectOptions, error) {
					resolverCalls++
					return candidate, nil
				},
			})
			if err == nil {
				t.Fatal("ConnectPlan accepted invalid endpoint URL")
			}
			if resolverCalls != 0 || dialCalls != 0 {
				t.Fatalf("resolver/dial calls = %d/%d, want 0/0", resolverCalls, dialCalls)
			}
		})
	}

	t.Run("invalid fallback", func(t *testing.T) {
		var resolverCalls int
		var dialCalls int
		client := NewGameClient(GameClientOptions{
			CreateChannel: func(context.Context, ConnectOptions) (ReliableMessageChannel, error) {
				dialCalls++
				return &fakeChannel{connected: true}, nil
			},
		})
		fallback := ConnectOptions{Transport: TransportWebSocket, URL: "https://example.test/api/ws"}
		err := client.ConnectPlan(context.Background(), ConnectionPlan{
			Primary:  ConnectOptions{Transport: TransportWebTransport, URL: "https://example.test/api/wt"},
			Fallback: &fallback,
			ResolveOptions: func(_ context.Context, candidate ConnectOptions) (ConnectOptions, error) {
				resolverCalls++
				return candidate, nil
			},
		})
		if err == nil {
			t.Fatal("ConnectPlan accepted an invalid fallback endpoint URL")
		}
		if resolverCalls != 0 || dialCalls != 0 {
			t.Fatalf("resolver/dial calls = %d/%d, want 0/0", resolverCalls, dialCalls)
		}
	})
}

func TestGameClientDirectConnectRejectsInvalidEndpointBeforeDialing(t *testing.T) {
	var dialCalls int
	client := NewGameClient(GameClientOptions{
		CreateChannel: func(context.Context, ConnectOptions) (ReliableMessageChannel, error) {
			dialCalls++
			return &fakeChannel{connected: true}, nil
		},
	})
	err := client.Connect(context.Background(), ConnectOptions{
		Transport: TransportWebSocket,
		URL:       "https://example.test/api/ws",
	})
	if err == nil {
		t.Fatal("Connect accepted a WebSocket endpoint with an HTTPS scheme")
	}
	if dialCalls != 0 {
		t.Fatalf("dial calls = %d, want 0", dialCalls)
	}
}

func TestConnectionPlanBuiltInWebTransportDialErrorFallsBackWithoutIntermediateLogs(t *testing.T) {
	var logs bytes.Buffer
	previousWriter := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(previousWriter)

	var fallbackDialed bool
	client := NewGameClient(GameClientOptions{
		CreateChannel: func(ctx context.Context, options ConnectOptions) (ReliableMessageChannel, error) {
			if options.Transport == TransportWebTransport {
				return DialChannel(ctx, options)
			}
			fallbackDialed = true
			return &fakeChannel{connected: true}, nil
		},
	})
	fallback := ConnectOptions{Transport: TransportWebSocket, URL: "wss://example.test/ws"}
	err := client.ConnectPlan(context.Background(), ConnectionPlan{
		Primary:             ConnectOptions{Transport: TransportWebTransport, URL: "https://127.0.0.1:1/wt"},
		Fallback:            &fallback,
		WebTransportTimeout: 25 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("ConnectPlan: %v", err)
	}
	if !fallbackDialed {
		t.Fatal("WebSocket fallback was not dialed")
	}
	if got := logs.String(); strings.Contains(got, "dialing webtransport") ||
		strings.Contains(got, "webtransport dial failed") ||
		strings.Contains(got, "webtransport open stream failed") ||
		strings.Contains(got, "webtransport stream header write failed") {
		t.Fatalf("successful fallback logged intermediate WebTransport attempt: %q", got)
	}
}

func equalTransports(got, want []TransportKind) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

type trackedPlanChannel struct {
	connected   atomic.Bool
	closeCalls  atomic.Int32
	callbacksMu sync.Mutex
	callbacks   channelCallbacks
}

func newTrackedPlanChannel() *trackedPlanChannel {
	channel := &trackedPlanChannel{}
	channel.connected.Store(true)
	return channel
}

func (c *trackedPlanChannel) Connected() bool                  { return c.connected.Load() }
func (*trackedPlanChannel) MaxMessageBytes() int               { return maxReliableMessageBytes }
func (*trackedPlanChannel) MaxDatagramBytes() int              { return 0 }
func (*trackedPlanChannel) Send([]byte) error                  { return nil }
func (*trackedPlanChannel) SendUnreliable([]byte) error        { return nil }
func (*trackedPlanChannel) SendReliableUnordered([]byte) error { return nil }
func (*trackedPlanChannel) SendReliableOrdered([]byte) error   { return nil }
func (c *trackedPlanChannel) Close() error {
	c.connected.Store(false)
	c.closeCalls.Add(1)
	return nil
}
func (c *trackedPlanChannel) OnOpen(fn func()) {
	c.callbacksMu.Lock()
	c.callbacks.onOpen = fn
	c.callbacksMu.Unlock()
	if fn != nil && c.Connected() {
		fn()
	}
}
func (c *trackedPlanChannel) OnMessage(fn func([]byte)) {
	c.callbacksMu.Lock()
	c.callbacks.onMessage = fn
	c.callbacksMu.Unlock()
}
func (c *trackedPlanChannel) OnUnreliableStateMessage(fn func([]byte)) {
	c.callbacksMu.Lock()
	c.callbacks.onUnreliableStateMessage = fn
	c.callbacksMu.Unlock()
}
func (c *trackedPlanChannel) OnReliableOrderedMessage(fn func([]byte)) {
	c.callbacksMu.Lock()
	c.callbacks.onReliableOrderedMessage = fn
	c.callbacksMu.Unlock()
}
func (c *trackedPlanChannel) OnEventualStateMessage(fn func([]byte)) {
	c.callbacksMu.Lock()
	c.callbacks.onEventualStateMessage = fn
	c.callbacksMu.Unlock()
}
func (c *trackedPlanChannel) OnClose(fn func(DisconnectInfo)) {
	c.callbacksMu.Lock()
	c.callbacks.onClose = fn
	c.callbacksMu.Unlock()
}

type blockingClosePlanChannel struct {
	*trackedPlanChannel
	closeStarted chan struct{}
	releaseClose chan struct{}
	closeOnce    sync.Once
}

func newBlockingClosePlanChannel(closeStarted, releaseClose chan struct{}) *blockingClosePlanChannel {
	return &blockingClosePlanChannel{
		trackedPlanChannel: newTrackedPlanChannel(),
		closeStarted:       closeStarted,
		releaseClose:       releaseClose,
	}
}

func (c *blockingClosePlanChannel) Close() error {
	c.connected.Store(false)
	c.closeCalls.Add(1)
	c.closeOnce.Do(func() { close(c.closeStarted) })
	<-c.releaseClose
	return nil
}

type closeBeforeCallbackPlanChannel struct {
	*trackedPlanChannel
}

func newCloseBeforeCallbackPlanChannel() *closeBeforeCallbackPlanChannel {
	return &closeBeforeCallbackPlanChannel{trackedPlanChannel: newTrackedPlanChannel()}
}

func (c *closeBeforeCallbackPlanChannel) OnClose(fn func(DisconnectInfo)) {
	// Simulate a close in the gap between the caller's Connected check and the
	// transport storing its close callback. No callback can replay this event.
	c.connected.Store(false)
	c.trackedPlanChannel.OnClose(fn)
}

func waitForPlanSignal(t *testing.T, signal <-chan struct{}, description string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", description)
	}
}

func assertPlanSignalPending(t *testing.T, signal <-chan struct{}, failure string) {
	t.Helper()
	select {
	case <-signal:
		t.Fatal(failure)
	default:
	}
}

func assertPlanResultPending(t *testing.T, result <-chan error) {
	t.Helper()
	select {
	case err := <-result:
		t.Fatalf("ConnectPlan finished before WebTransport cleanup: %v", err)
	default:
	}
}
