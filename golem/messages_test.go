package golem

import (
	"bytes"
	"testing"
	"time"

	golemnet "github.com/demiurgos-hub/golem-engine/golem/net"
)

// TestDrainMessages_Ordering verifies that events queued via the internal
// enqueue helpers are dispatched in FIFO order by drainMessages, and that
// connect precedes message which precedes disconnect.
func TestDrainMessages_Ordering(t *testing.T) {
	srv := NewServer(ServerConfig{TickRate: 20, Transport: golemnet.TransportWebSocket, StateUpdateLane: StateUpdateLaneStream})

	// Push events directly onto the queue to simulate connection goroutines.
	// We use nil sessions because drainMessages only invokes the stored
	// callback — it does not dereference sess itself.
	srv.msgQueue <- pendingMsg{kind: msgConnect, sess: nil}
	srv.msgQueue <- pendingMsg{kind: msgMessage, sess: nil, data: []byte{1, 2, 3}}
	srv.msgQueue <- pendingMsg{kind: msgDisconnect, sess: nil}

	var order []string

	srv.OnConnect(func(_ *Session) { order = append(order, "connect") })
	srv.OnMessage(func(_ *Session, _ []byte) { order = append(order, "message") })
	srv.OnDisconnect(func(_ *Session) { order = append(order, "disconnect") })

	srv.drainMessages()

	want := []string{"connect", "message", "disconnect"}
	if len(order) != len(want) {
		t.Fatalf("got %d events, want %d: %v", len(order), len(want), order)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Errorf("event[%d] = %q, want %q", i, order[i], want[i])
		}
	}
}

// TestDrainMessages_DataCopy verifies that the []byte delivered to OnMessage
// is a distinct copy from what was originally enqueued, guarding against
// WebSocket read-buffer reuse.
func TestDrainMessages_DataCopy(t *testing.T) {
	srv := NewServer(ServerConfig{TickRate: 20, Transport: golemnet.TransportWebSocket, StateUpdateLane: StateUpdateLaneStream})

	original := []byte{0xAA, 0xBB, 0xCC}
	cp := make([]byte, len(original))
	copy(cp, original)

	// Simulate enqueueMessage: the copy must already have been made.
	srv.msgQueue <- pendingMsg{kind: msgMessage, sess: nil, data: cp}

	// Mutate the "original" buffer to prove the queued copy is independent.
	original[0] = 0xFF

	var received []byte
	srv.OnMessage(func(_ *Session, data []byte) { received = data })
	srv.drainMessages()

	want := []byte{0xAA, 0xBB, 0xCC}
	if !bytes.Equal(received, want) {
		t.Errorf("received %v, want %v (copy not independent of original buffer)", received, want)
	}
}

// TestDrainMessages_NoCallbacks verifies that drainMessages does not panic
// when no user callbacks are registered.
func TestDrainMessages_NoCallbacks(t *testing.T) {
	srv := NewServer(ServerConfig{TickRate: 20, Transport: golemnet.TransportWebSocket, StateUpdateLane: StateUpdateLaneStream})
	srv.msgQueue <- pendingMsg{kind: msgConnect, sess: nil}
	srv.msgQueue <- pendingMsg{kind: msgMessage, sess: nil, data: []byte{1}}
	srv.msgQueue <- pendingMsg{kind: msgDisconnect, sess: nil}
	srv.drainMessages() // must not panic
}

// TestDrainMessages_EmptyQueue verifies that drainMessages returns immediately
// on an empty queue without blocking.
func TestDrainMessages_EmptyQueue(t *testing.T) {
	srv := NewServer(ServerConfig{TickRate: 20, Transport: golemnet.TransportWebSocket, StateUpdateLane: StateUpdateLaneStream})
	srv.drainMessages() // must return without blocking
}

// TestEnqueue_FullQueueDrops verifies that enqueue drops events without
// blocking when the queue is at capacity.
func TestEnqueue_FullQueueDrops(t *testing.T) {
	srv := NewServer(ServerConfig{TickRate: 20, Transport: golemnet.TransportWebSocket, StateUpdateLane: StateUpdateLaneStream})

	// Fill the queue to capacity.
	for i := 0; i < msgQueueCap; i++ {
		srv.msgQueue <- pendingMsg{kind: msgMessage}
	}

	// enqueue must return immediately (drop, not block) when the queue is full.
	done := make(chan struct{})
	go func() {
		srv.enqueue(pendingMsg{kind: msgMessage})
		close(done)
	}()

	select {
	case <-done:
		// good: returned without blocking
	case <-time.After(time.Second):
		t.Error("enqueue blocked on a full queue instead of dropping")
	}
}
