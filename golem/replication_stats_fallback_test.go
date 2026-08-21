package golem

import (
	"bytes"
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"
	golemnet "github.com/demiurgos-hub/golem-engine/golem/net"
)

type replicationStatsFallbackEntity struct {
	id    int64
	full  []byte
	delta []byte
}

func (e *replicationStatsFallbackEntity) EntityID() int64              { return e.id }
func (e *replicationStatsFallbackEntity) TypeName() string             { return "replication-stats-fallback" }
func (e *replicationStatsFallbackEntity) Position() (float32, float32) { return 0, 0 }
func (e *replicationStatsFallbackEntity) IsGlobal() bool               { return false }
func (e *replicationStatsFallbackEntity) FlushUpdate() ([]byte, error) { return e.delta, nil }
func (e *replicationStatsFallbackEntity) FullUpdate() ([]byte, error)  { return e.full, nil }

func TestEventualStateStreamFallbackCountsActualReliableWrites(t *testing.T) {
	srv := NewServer(ServerConfig{
		Transport:       golemnet.TransportWebTransport,
		StateUpdateLane: StateUpdateLaneDatagram,
	})
	httpServer := httptest.NewServer(srv.WebSocketHandler())
	defer httpServer.Close()
	client := mustDialGameClient(t, "ws"+httpServer.URL[4:])
	defer client.CloseNow()
	sessionID := waitForSessionIDs(t, srv, 1)[0]

	entities := []*replicationStatsFallbackEntity{
		{id: 1, full: bytes.Repeat([]byte("a"), 40*1024)},
		{id: 2, full: bytes.Repeat([]byte("b"), 40*1024)},
	}
	changes := make([]eventualStateChange, 0, len(entities))
	prepared := make([]eventualPreparedFrame, 0, len(entities))
	for _, entity := range entities {
		if err := srv.reg.Add(entity); err != nil {
			t.Fatalf("Add entity %d: %v", entity.id, err)
		}
		change := eventualStateChange{id: entity.id, full: true}
		changes = append(changes, change)
		frame, err := srv.eventualPreparedFrameForChange(change)
		if err != nil {
			t.Fatalf("eventualPreparedFrameForChange(%d): %v", entity.id, err)
		}
		prepared = append(prepared, frame)
	}

	tracker := newEventualStateTracker()
	stats, err := srv.sendEventualStateChanges(sessionID, tracker, nil, changes)
	if err != nil {
		t.Fatalf("sendEventualStateChanges: %v", err)
	}
	if stats.streamBatchedFrames != 2 || stats.streamWireMsgs != 1 {
		t.Fatalf("change stream stats = frames:%d messages:%d, want 2/1", stats.streamBatchedFrames, stats.streamWireMsgs)
	}
	if stats.datagramBatchedFrames != 0 || stats.datagramWirePayloads != 0 {
		t.Fatalf("change datagram stats = frames:%d payloads:%d, want 0/0", stats.datagramBatchedFrames, stats.datagramWirePayloads)
	}
	if got := mustReadWrappedMessages(t, client, 1)[0]; len(got) != 2 {
		t.Fatalf("change stream payload count = %d, want 2", len(got))
	}

	stats, err = srv.sendPreparedEventualStateFrames(sessionID, tracker, prepared)
	if err != nil {
		t.Fatalf("sendPreparedEventualStateFrames: %v", err)
	}
	if stats.streamBatchedFrames != 2 || stats.streamWireMsgs != 1 {
		t.Fatalf("prepared stream stats = frames:%d messages:%d, want 2/1", stats.streamBatchedFrames, stats.streamWireMsgs)
	}
	if stats.datagramBatchedFrames != 0 || stats.datagramWirePayloads != 0 {
		t.Fatalf("prepared datagram stats = frames:%d payloads:%d, want 0/0", stats.datagramBatchedFrames, stats.datagramWirePayloads)
	}
	if got := mustReadWrappedMessages(t, client, 1)[0]; len(got) != 2 {
		t.Fatalf("prepared stream payload count = %d, want 2", len(got))
	}
}

func TestBlindStreamReplicationWrapsOnceAndCountsSentFrames(t *testing.T) {
	srv := NewServer(ServerConfig{
		Transport:       golemnet.TransportWebSocket,
		StateUpdateLane: StateUpdateLaneStream,
	})
	entity := &replicationStatsFallbackEntity{id: 1, full: []byte("full")}
	if err := srv.CreateEntity(entity); err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}

	httpServer := httptest.NewServer(srv.Handler())
	defer httpServer.Close()
	clientA := mustDialGameClient(t, "ws"+httpServer.URL[4:])
	defer clientA.CloseNow()
	clientB := mustDialGameClient(t, "ws"+httpServer.URL[4:])
	defer clientB.CloseNow()
	_ = waitForSessionIDs(t, srv, 2)
	_ = mustReadWrappedMessages(t, clientA, 1)
	_ = mustReadWrappedMessages(t, clientB, 1)
	if _, err := srv.reg.FlushAll(); err != nil {
		t.Fatalf("clear initial spawn: %v", err)
	}

	var wrapperCalls int
	srv.listener.SetMessageWrapper(func(data []byte) []byte {
		wrapperCalls++
		wrapped := make([]byte, 1, len(data)+1)
		wrapped[0] = 0xa5
		return append(wrapped, data...)
	})
	entity.delta = []byte("stream-delta")
	if err := srv.runBroadcastTick(); err != nil {
		t.Fatalf("runBroadcastTick: %v", err)
	}
	if wrapperCalls != 1 {
		t.Fatalf("message wrapper calls = %d, want one call for one logical update", wrapperCalls)
	}

	want := append([]byte{0xa5}, entity.delta...)
	for name, client := range map[string]*websocket.Conn{
		"client A": clientA,
		"client B": clientB,
	} {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		_, got, err := client.Read(ctx)
		cancel()
		if err != nil {
			t.Fatalf("%s Read: %v", name, err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("%s update = %q, want %q", name, got, want)
		}
	}

	stats := srv.ReplicationStats()
	if stats.StreamBatchedFrames != 2 || stats.StreamWireMsgs != 2 {
		t.Fatalf("stream stats = frames:%d messages:%d, want 2/2", stats.StreamBatchedFrames, stats.StreamWireMsgs)
	}
	if stats.DatagramBatchedFrames != 0 || stats.DatagramWirePayloads != 0 {
		t.Fatalf("datagram stats = frames:%d payloads:%d, want 0/0", stats.DatagramBatchedFrames, stats.DatagramWirePayloads)
	}
}
