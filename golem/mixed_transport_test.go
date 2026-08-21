package golem

import (
	"bytes"
	"context"
	"net/http/httptest"
	"testing"
	"time"

	golemnet "github.com/demiurgos-hub/golem-engine/golem/net"
	"github.com/demiurgos-hub/golem-engine/golem/pb"
)

type mixedTransportEntity struct {
	id       int64
	full     []byte
	delta    []byte
	compact  []byte
	revision uint64
}

func (e *mixedTransportEntity) EntityID() int64              { return e.id }
func (e *mixedTransportEntity) TypeName() string             { return "mixed-transport-test" }
func (e *mixedTransportEntity) Position() (float32, float32) { return 0, 0 }
func (e *mixedTransportEntity) IsGlobal() bool               { return false }
func (e *mixedTransportEntity) FlushUpdate() ([]byte, error) { return e.delta, nil }
func (e *mixedTransportEntity) FullUpdate() ([]byte, error)  { return e.full, nil }
func (e *mixedTransportEntity) LastFlushMask() uint64        { return 1 }
func (e *mixedTransportEntity) MarshalDeltaMask(uint64) ([]byte, error) {
	return append([]byte(nil), e.delta...), nil
}
func (e *mixedTransportEntity) MarshalCompactDeltaMask(uint64) ([]byte, error) {
	w := &pb.Writer{}
	w.Int64(e.id)
	w.Uint64(e.revision)
	w.Uint64(1)
	w.Raw(e.compact)
	return w.Finish(), nil
}

func TestRunBroadcastTickDatagramUsesStreamForWebSocketFallback(t *testing.T) {
	addr := freeUDPAddr(t)
	srv := NewServer(ServerConfig{
		Addr:              addr,
		Transport:         golemnet.TransportWebTransport,
		DevSelfSignedCert: true,
		StateUpdateLane:   StateUpdateLaneDatagram,
	})
	entity := &mixedTransportEntity{
		id:       1,
		full:     []byte("entity-full"),
		compact:  []byte("entity-compact-delta"),
		revision: 1,
	}
	entity2 := &mixedTransportEntity{
		id:       2,
		full:     []byte("entity-two-full"),
		compact:  []byte("entity-two-compact-delta"),
		revision: 1,
	}
	for _, e := range []*mixedTransportEntity{entity, entity2} {
		if err := srv.CreateEntity(e); err != nil {
			t.Fatalf("CreateEntity(%d): %v", e.id, err)
		}
	}
	if _, err := srv.reg.FlushAll(); err != nil {
		t.Fatalf("clear initial spawn: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	listenerErr := make(chan error, 1)
	go func() {
		listenerErr <- srv.listener.ListenAndServe(ctx)
	}()
	defer func() {
		cancel()
		select {
		case err := <-listenerErr:
			if err != nil && err != context.Canceled {
				t.Fatalf("ListenAndServe: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for listener shutdown")
		}
	}()
	if err := srv.WaitReady(ctx); err != nil {
		t.Fatalf("WaitReady: %v", err)
	}

	wsHTTP := httptest.NewServer(srv.WebSocketHandler())
	defer wsHTTP.Close()

	wtSession := dialWebTransportSession(t, "https://"+addr+"/wt")
	defer wtSession.CloseWithError(0, "")
	wtStream, err := wtSession.OpenStreamSync(context.Background())
	if err != nil {
		t.Fatalf("OpenStreamSync: %v", err)
	}
	if _, err := wtStream.Write(nil); err != nil {
		t.Fatalf("prime reliable stream: %v", err)
	}

	wsClient := mustDialGameClient(t, "ws"+wsHTTP.URL[4:])
	defer wsClient.CloseNow()
	wsClient.SetReadLimit(1 << 20)
	_ = waitForSessionIDs(t, srv, 2)

	for i := 0; i < 2; i++ {
		wtSnapshot := mustReadReliableFrame(t, wtStream)
		if got := decodeWrappedEntityBatch(t, wtSnapshot); len(got) != 1 {
			t.Fatalf("WebTransport snapshot %d = %q, want one entity", i, got)
		}
	}
	wsSnapshot := mustReadWrappedMessages(t, wsClient, 2)
	for i, got := range wsSnapshot {
		if len(got) != 1 {
			t.Fatalf("WebSocket snapshot %d = %q, want one entity", i, got)
		}
	}

	entity.delta = []byte("entity-stream-delta")
	if err := srv.runBroadcastTick(); err != nil {
		t.Fatalf("runBroadcastTick: %v", err)
	}

	wsDelta := mustReadWrappedMessages(t, wsClient, 1)
	if got := wsDelta[0]; len(got) != 1 || !bytes.Equal(got[0], entity.delta) {
		t.Fatalf("WebSocket delta = %q, want %q", got, entity.delta)
	}
	stats := srv.ReplicationStats()
	datagramCtx, datagramCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer datagramCancel()
	if data, err := wtSession.ReceiveDatagram(datagramCtx); err != nil {
		t.Fatalf("WebTransport ReceiveDatagram: %v", err)
	} else if len(data) == 0 {
		t.Fatal("WebTransport received an empty state datagram")
	}

	if stats.StreamBatchedFrames != 1 || stats.StreamWireMsgs != 1 {
		t.Fatalf("stream stats = frames:%d messages:%d, want 1/1 for WebSocket only", stats.StreamBatchedFrames, stats.StreamWireMsgs)
	}
	if stats.DatagramBatchedFrames != 1 || stats.DatagramWirePayloads != 1 {
		t.Fatalf("datagram stats = frames:%d payloads:%d, want 1/1 for WebTransport only", stats.DatagramBatchedFrames, stats.DatagramWirePayloads)
	}

	// Stream mode still uses each session's actual transport for batching and
	// wire-message statistics: WebSocket coalesces both frames into one message,
	// while WebTransport splits them at its 32 KiB write target.
	srv.config.StateUpdateLane = StateUpdateLaneStream
	entity.delta = bytes.Repeat([]byte("a"), 20_000)
	entity2.delta = bytes.Repeat([]byte("b"), 20_000)
	if err := srv.runBroadcastTick(); err != nil {
		t.Fatalf("runBroadcastTick stream: %v", err)
	}
	wsStream := mustReadWrappedMessages(t, wsClient, 1)
	if got := len(wsStream[0]); got != 2 {
		t.Fatalf("WebSocket stream payload count = %d, want 2", got)
	}
	for i := 0; i < 2; i++ {
		wtFrame := mustReadReliableFrame(t, wtStream)
		if got := decodeWrappedEntityBatch(t, wtFrame); len(got) != 1 {
			t.Fatalf("WebTransport stream frame %d = %q, want one entity", i, got)
		}
	}
	stats = srv.ReplicationStats()
	if stats.StreamBatchedFrames != 4 || stats.StreamWireMsgs != 3 {
		t.Fatalf("mixed stream stats = frames:%d messages:%d, want 4/3", stats.StreamBatchedFrames, stats.StreamWireMsgs)
	}
}

func TestServerRunShutdownClosesWebSocketFallbackSession(t *testing.T) {
	srv := NewServer(ServerConfig{Transport: golemnet.TransportWebTransport})
	ctx, cancel := context.WithCancel(context.Background())
	runErr := make(chan error, 1)
	go func() {
		runErr <- srv.Run(ctx)
	}()

	wsHTTP := httptest.NewServer(srv.WebSocketHandler())
	defer wsHTTP.Close()
	client := mustDialGameClient(t, "ws"+wsHTTP.URL[4:])
	defer client.CloseNow()
	_ = waitForSessionIDs(t, srv, 1)

	readErr := make(chan error, 1)
	go func() {
		_, _, err := client.Read(context.Background())
		readErr <- err
	}()
	cancel()

	select {
	case err := <-runErr:
		if err != context.Canceled {
			t.Fatalf("Run error = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Server.Run shutdown")
	}
	select {
	case err := <-readErr:
		if err == nil {
			t.Fatal("fallback client read returned nil after shutdown")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("fallback WebSocket session remained open after Server.Run shutdown")
	}
	waitForSessionIDs(t, srv, 0)
}

func TestServerRunAlreadyCanceledClosesExistingWebSocketFallbackSession(t *testing.T) {
	srv := NewServer(ServerConfig{Transport: golemnet.TransportWebTransport})
	wsHTTP := httptest.NewServer(srv.WebSocketHandler())
	defer wsHTTP.Close()
	client := mustDialGameClient(t, "ws"+wsHTTP.URL[4:])
	defer client.CloseNow()
	_ = waitForSessionIDs(t, srv, 1)

	readErr := make(chan error, 1)
	go func() {
		_, _, err := client.Read(context.Background())
		readErr <- err
	}()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := srv.Run(ctx); err != context.Canceled {
		t.Fatalf("Run error = %v, want context.Canceled", err)
	}
	select {
	case err := <-readErr:
		if err == nil {
			t.Fatal("fallback client read returned nil after already-canceled Run")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("fallback WebSocket session remained open after already-canceled Run")
	}
	waitForSessionIDs(t, srv, 0)
}
