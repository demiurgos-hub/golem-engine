package golemebiten

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	golemclient "golem-engine/golem-go-client"
)

type fakeClient struct {
	connects       int
	closed         bool
	onClose        func(golemclient.DisconnectInfo)
	connectOptions []golemclient.ConnectOptions
	connectErrs    []error
}

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
