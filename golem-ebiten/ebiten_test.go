package golemebiten

import (
	"bytes"
	"context"
	"errors"
	"log"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	golemclient "github.com/demiurgos-hub/golem-engine/golem-go-client"
)

type fakeClient struct {
	connects       int
	closed         bool
	onClose        func(golemclient.DisconnectInfo)
	connectOptions []golemclient.ConnectOptions
	connectErrs    []error
}

type fakePlanClient struct {
	fakeClient
	plans               []golemclient.ConnectionPlan
	planErrs            []error
	transport           golemclient.TransportKind
	disconnectOnConnect *golemclient.DisconnectInfo
}

func (f *fakePlanClient) ConnectPlan(_ context.Context, plan golemclient.ConnectionPlan) error {
	f.plans = append(f.plans, plan)
	if f.disconnectOnConnect != nil && f.onClose != nil {
		f.onClose(*f.disconnectOnConnect)
	}
	if len(f.planErrs) > 0 {
		err := f.planErrs[0]
		f.planErrs = f.planErrs[1:]
		return err
	}
	if f.transport == "" {
		f.transport = plan.Primary.Transport
	}
	return nil
}

func (f *fakePlanClient) ConnectedTransport() golemclient.TransportKind { return f.transport }

func (f *fakeClient) Connect(_ context.Context, options golemclient.ConnectOptions) error {
	f.connects++
	f.connectOptions = append(f.connectOptions, options)
	f.closed = false
	if len(f.connectErrs) > 0 {
		err := f.connectErrs[0]
		f.connectErrs = f.connectErrs[1:]
		return err
	}
	return nil
}

func (f *fakeClient) Disconnect() { f.closed = true }

func (f *fakeClient) OnDisconnect(fn func(golemclient.DisconnectInfo)) { f.onClose = fn }

type fakeView struct {
	updates int
	draws   int
}

func (f *fakeView) Update() error {
	f.updates++
	return nil
}

func (f *fakeView) Draw(Screen) { f.draws++ }

func TestGameConnectUpdateDrawAndDisconnect(t *testing.T) {
	client := &fakeClient{}
	connection := golemclient.ConnectOptions{Transport: golemclient.TransportWebSocket, URL: "ws://example/ws"}
	var connected int
	game := NewGame(GameConfig{
		Client:     client,
		Connection: connection,
		OnConnect:  func() { connected++ },
	})
	if err := game.Connect(context.Background()); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	view := &fakeView{}
	game.AddView(view)
	if err := game.Update(context.Background()); err != nil {
		t.Fatalf("Update: %v", err)
	}
	game.Draw(nil)
	game.Disconnect()
	if client.connects != 1 {
		t.Fatalf("connects = %d, want 1", client.connects)
	}
	if connected != 1 {
		t.Fatalf("connect callbacks = %d, want 1", connected)
	}
	if got := client.connectOptions[0]; got.Transport != connection.Transport || got.URL != connection.URL {
		t.Fatalf("connection options = %+v, want %+v", got, connection)
	}
	if view.updates != 1 || view.draws != 1 {
		t.Fatalf("view updates/draws = %d/%d, want 1/1", view.updates, view.draws)
	}
	if !client.closed {
		t.Fatal("client was not disconnected")
	}
}

func TestGameConnectionOptionsRefreshesForReconnect(t *testing.T) {
	client := &fakeClient{}
	var calls int
	var disconnects int
	game := NewGame(GameConfig{
		Client:             client,
		ReconnectBaseDelay: -time.Second,
		OnDisconnect:       func(golemclient.DisconnectInfo) { disconnects++ },
		ConnectionOptions: func(context.Context) (golemclient.ConnectOptions, error) {
			calls++
			return golemclient.ConnectOptions{
				Transport: golemclient.TransportWebTransport,
				URL:       "https://example/wt/" + strconv.Itoa(calls),
			}, nil
		},
	})
	if err := game.Connect(context.Background()); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if client.onClose == nil {
		t.Fatal("disconnect callback was not registered")
	}
	client.onClose(golemclient.DisconnectInfo{WasClean: false})
	if disconnects != 1 {
		t.Fatalf("disconnect callbacks = %d, want 1", disconnects)
	}
	if err := game.Update(context.Background()); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if calls != 2 {
		t.Fatalf("connection option calls = %d, want 2", calls)
	}
	if client.connects != 2 {
		t.Fatalf("connects = %d, want 2", client.connects)
	}
	first := client.connectOptions[0]
	second := client.connectOptions[1]
	if first.Transport != golemclient.TransportWebTransport || second.Transport != golemclient.TransportWebTransport {
		t.Fatalf("transports = %q/%q, want webtransport", first.Transport, second.Transport)
	}
	if first.URL == second.URL {
		t.Fatalf("reconnect reused URL %q", first.URL)
	}
}

func TestGameForwardsWebTransportOptions(t *testing.T) {
	client := &fakeClient{}
	connection := golemclient.ConnectOptions{
		Transport:               golemclient.TransportWebTransport,
		URL:                     "https://example/wt",
		ServerCertificateHashes: []golemclient.CertificateHash{{Algorithm: "sha-256", Value: []byte{1, 2, 3}}},
		EventualAckIntervalMs:   25,
	}
	game := NewGame(GameConfig{
		Client:            client,
		ConnectionOptions: func(context.Context) (golemclient.ConnectOptions, error) { return connection, nil },
	})
	if err := game.Connect(context.Background()); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	got := client.connectOptions[0]
	if got.Transport != connection.Transport || got.URL != connection.URL || got.EventualAckIntervalMs != connection.EventualAckIntervalMs {
		t.Fatalf("connection options = %+v, want %+v", got, connection)
	}
	if len(got.ServerCertificateHashes) != 1 || got.ServerCertificateHashes[0].Algorithm != "sha-256" || string(got.ServerCertificateHashes[0].Value) != string([]byte{1, 2, 3}) {
		t.Fatalf("certificate hashes = %+v, want %+v", got.ServerCertificateHashes, connection.ServerCertificateHashes)
	}
}

func TestGameReconnectFailureCallback(t *testing.T) {
	connectErr := errors.New("dial failed")
	client := &fakeClient{connectErrs: []error{connectErr, connectErr}}
	var failed int
	game := NewGame(GameConfig{
		Client:               client,
		MaxReconnectAttempts: 1,
		ReconnectBaseDelay:   -time.Second,
		OnReconnectFailed:    func() { failed++ },
	})
	if err := game.Connect(context.Background()); !errors.Is(err, connectErr) {
		t.Fatalf("Connect error = %v, want %v", err, connectErr)
	}
	if err := game.Update(context.Background()); !errors.Is(err, connectErr) {
		t.Fatalf("Update error = %v, want %v", err, connectErr)
	}
	if failed != 1 {
		t.Fatalf("reconnect failures = %d, want 1", failed)
	}
}

func TestGameConnectionPlanProviderPrecedesLegacyOptionsAndRefreshes(t *testing.T) {
	client := &fakePlanClient{transport: golemclient.TransportWebSocket}
	var planCalls int
	var optionsCalls int
	game := NewGame(GameConfig{
		Client:             client,
		ReconnectBaseDelay: -time.Second,
		ConnectionPlan: golemclient.ConnectionPlan{
			Primary: golemclient.ConnectOptions{Transport: golemclient.TransportWebTransport, URL: "https://static.invalid/wt"},
		},
		ConnectionPlanProvider: func(context.Context) (golemclient.ConnectionPlan, error) {
			planCalls++
			fallback := golemclient.ConnectOptions{
				Transport: golemclient.TransportWebSocket,
				URL:       "wss://example/ws/" + strconv.Itoa(planCalls),
			}
			return golemclient.ConnectionPlan{
				Primary:  golemclient.ConnectOptions{Transport: golemclient.TransportWebTransport, URL: "https://example/wt/" + strconv.Itoa(planCalls)},
				Fallback: &fallback,
			}, nil
		},
		ConnectionOptions: func(context.Context) (golemclient.ConnectOptions, error) {
			optionsCalls++
			return golemclient.ConnectOptions{Transport: golemclient.TransportWebSocket, URL: "wss://legacy.invalid/ws"}, nil
		},
	})
	if err := game.Connect(context.Background()); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if got := game.ConnectedTransport(); got != golemclient.TransportWebSocket {
		t.Fatalf("ConnectedTransport = %q, want websocket", got)
	}
	client.onClose(golemclient.DisconnectInfo{WasClean: false})
	if got := game.ConnectedTransport(); got != "" {
		t.Fatalf("ConnectedTransport after close = %q, want empty", got)
	}
	if err := game.Update(context.Background()); err != nil {
		t.Fatalf("Update reconnect: %v", err)
	}
	if planCalls != 2 || len(client.plans) != 2 {
		t.Fatalf("plan provider/client calls = %d/%d, want 2/2", planCalls, len(client.plans))
	}
	if optionsCalls != 0 || client.connects != 0 {
		t.Fatalf("legacy options/connect calls = %d/%d, want 0/0", optionsCalls, client.connects)
	}
	if client.plans[0].Primary.URL == client.plans[1].Primary.URL {
		t.Fatalf("plan provider did not refresh endpoint: %q", client.plans[0].Primary.URL)
	}
}

func TestGameStaticConnectionPlanPrecedesLegacyOptions(t *testing.T) {
	client := &fakePlanClient{transport: golemclient.TransportWebTransport}
	var optionsCalls int
	plan := golemclient.ConnectionPlan{
		Primary: golemclient.ConnectOptions{Transport: golemclient.TransportWebTransport, URL: "https://example/wt"},
	}
	game := NewGame(GameConfig{
		Client:         client,
		ConnectionPlan: plan,
		ConnectionOptions: func(context.Context) (golemclient.ConnectOptions, error) {
			optionsCalls++
			return golemclient.ConnectOptions{}, nil
		},
	})
	if err := game.Connect(context.Background()); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if len(client.plans) != 1 || optionsCalls != 0 {
		t.Fatalf("plan/legacy calls = %d/%d, want 1/0", len(client.plans), optionsCalls)
	}
}

func TestGameConnectionPlanRequiresPlanCapableClient(t *testing.T) {
	client := &fakeClient{}
	game := NewGame(GameConfig{
		Client: client,
		ConnectionPlan: golemclient.ConnectionPlan{
			Primary: golemclient.ConnectOptions{Transport: golemclient.TransportWebTransport, URL: "https://example/wt"},
		},
	})
	err := game.Connect(context.Background())
	if err == nil || !strings.Contains(err.Error(), "does not support connection plans") {
		t.Fatalf("Connect error = %v, want plan-capability error", err)
	}
}

func TestGameDisconnectDuringConnectPreservesScheduledReconnect(t *testing.T) {
	disconnect := golemclient.DisconnectInfo{WasClean: false, Err: errors.New("connection closed")}
	client := &fakePlanClient{
		transport:           golemclient.TransportWebSocket,
		disconnectOnConnect: &disconnect,
	}
	var connectCallbacks int
	game := NewGame(GameConfig{
		Client:             client,
		ReconnectBaseDelay: time.Hour,
		ConnectionPlan: golemclient.ConnectionPlan{
			Primary: golemclient.ConnectOptions{Transport: golemclient.TransportWebTransport, URL: "https://example/wt"},
		},
		OnConnect: func() { connectCallbacks++ },
	})

	if err := game.Connect(context.Background()); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if game.Connected() {
		t.Fatal("Connected = true after disconnect reported during ConnectPlan")
	}
	if got := game.ConnectedTransport(); got != "" {
		t.Fatalf("ConnectedTransport = %q, want empty", got)
	}
	if connectCallbacks != 0 {
		t.Fatalf("OnConnect callbacks = %d, want 0", connectCallbacks)
	}
	game.stateMu.RLock()
	pending, attempts := game.reconnectPending, game.attempts
	game.stateMu.RUnlock()
	if !pending || attempts != 1 {
		t.Fatalf("reconnect pending/attempts = %v/%d, want true/1", pending, attempts)
	}
}

func TestGameCleanReplacementDisconnectAllowsNewConnectCompletion(t *testing.T) {
	client := &fakePlanClient{transport: golemclient.TransportWebSocket}
	var connectCallbacks int
	game := NewGame(GameConfig{
		Client: client,
		ConnectionPlan: golemclient.ConnectionPlan{
			Primary: golemclient.ConnectOptions{Transport: golemclient.TransportWebTransport, URL: "https://example/wt"},
		},
		OnConnect: func() { connectCallbacks++ },
	})
	if err := game.Connect(context.Background()); err != nil {
		t.Fatalf("initial Connect: %v", err)
	}

	clean := golemclient.DisconnectInfo{WasClean: true}
	client.disconnectOnConnect = &clean
	if err := game.Connect(context.Background()); err != nil {
		t.Fatalf("replacement Connect: %v", err)
	}
	if !game.Connected() {
		t.Fatal("Connected = false after successful replacement")
	}
	if got := game.ConnectedTransport(); got != golemclient.TransportWebSocket {
		t.Fatalf("ConnectedTransport = %q, want websocket", got)
	}
	if connectCallbacks != 2 {
		t.Fatalf("OnConnect callbacks = %d, want 2", connectCallbacks)
	}
	game.stateMu.RLock()
	pending := game.reconnectPending
	game.stateMu.RUnlock()
	if pending {
		t.Fatal("reconnect remained pending after clean replacement")
	}
}

func TestGameProviderErrorsAreOpaqueBeforeReturnAndLogging(t *testing.T) {
	const secret = "provider-ticket-secret"
	tests := []struct {
		name string
		cfg  func(error) GameConfig
	}{
		{
			name: "plan",
			cfg: func(providerErr error) GameConfig {
				return GameConfig{
					Client: &fakePlanClient{},
					ConnectionPlanProvider: func(context.Context) (golemclient.ConnectionPlan, error) {
						return golemclient.ConnectionPlan{}, providerErr
					},
				}
			},
		},
		{
			name: "options",
			cfg: func(providerErr error) GameConfig {
				return GameConfig{
					Client: &fakeClient{},
					ConnectionOptions: func(context.Context) (golemclient.ConnectOptions, error) {
						return golemclient.ConnectOptions{}, providerErr
					},
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			providerErr := &url.Error{
				Op:  "fetch",
				URL: "https://example.test/realtime?ticket=" + secret,
				Err: errors.New("ticket request failed for " + secret),
			}
			var logs bytes.Buffer
			previousWriter := log.Writer()
			log.SetOutput(&logs)
			t.Cleanup(func() { log.SetOutput(previousWriter) })

			game := NewGame(tt.cfg(providerErr))
			err := game.Connect(context.Background())
			if !errors.Is(err, providerErr) {
				t.Fatalf("Connect error = %v, want wrapped provider error", err)
			}
			for label, text := range map[string]string{"error": err.Error(), "log": logs.String()} {
				if strings.Contains(text, secret) || strings.Contains(strings.ToLower(text), "ticket=") {
					t.Fatalf("%s leaked provider credentials: %q", label, text)
				}
			}
		})
	}
}
