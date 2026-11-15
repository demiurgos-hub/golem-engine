package net

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/demiurgos-hub/golem-engine/golem/registry"
	"github.com/demiurgos-hub/golem-engine/internal/netwire"
)

func TestListenerEventDeliveryUsesLanesAndFallsBackToStream(t *testing.T) {
	listener := NewListener(registry.NewRegistry(), Config{Transport: TransportWebTransport})
	wtSession := newSession(1, &captureReliableChannel{}, &captureDatagramChannel{}, nil)
	wsSession := newSession(2, &captureReliableChannel{}, nil, nil)
	listener.addSession(wtSession)
	listener.addSession(wsSession)
	defer listener.removeSession(wtSession)
	defer listener.removeSession(wsSession)

	event, stream := []byte{0x0a, 0x02, 0x08, 0x01}, []byte("wrapped")
	record := netwire.AppendEventRecord(nil, event)
	for _, delivery := range []EventDelivery{EventStream, EventReliableOrdered, EventReliableUnordered, EventUnreliable} {
		if err := listener.BroadcastEvent(delivery, event, stream); err != nil {
			t.Fatalf("BroadcastEvent(%d): %v", delivery, err)
		}
	}
	if err := listener.SendEvent(wtSession.ID, EventReliableOrdered, event, stream); err != nil {
		t.Fatalf("SendEvent: %v", err)
	}

	if got := len(wtSession.streamSend); got != 1 {
		t.Fatalf("WebTransport stream batches = %d, want only the stream delivery", got)
	}
	if got := len(wtSession.protocol.pendingOrdered); got != 2 {
		t.Fatalf("WebTransport reliable ordered queue = %d, want 2", got)
	}
	if got := len(wtSession.protocol.pendingUnordered); got != 1 {
		t.Fatalf("WebTransport reliable unordered queue = %d, want 1", got)
	}
	for _, message := range append(wtSession.protocol.pendingOrdered, wtSession.protocol.pendingUnordered...) {
		if !bytes.Equal(message.payload, record) {
			t.Fatalf("reliable event payload = %x, want record %x", message.payload, record)
		}
	}
	if got := <-wtSession.unreliableSend; !bytes.Equal(got, record) {
		t.Fatalf("unreliable event payload = %x, want record %x", got, record)
	}
	if got := len(wsSession.streamSend); got != 4 {
		t.Fatalf("WebSocket stream batches = %d, want every delivery on the stream", got)
	}
	for len(wsSession.streamSend) > 0 {
		if batch := <-wsSession.streamSend; len(batch) != 1 || !bytes.Equal(batch[0], stream) {
			t.Fatalf("WebSocket fallback = %q, want the wrapped event", batch)
		}
	}
}

func TestListenerEventDeliveryRejectsOversizedDatagramEvents(t *testing.T) {
	listener := NewListener(registry.NewRegistry(), Config{Transport: TransportWebTransport})
	wsSession := newSession(1, &captureReliableChannel{}, nil, nil)
	listener.addSession(wsSession)
	defer listener.removeSession(wsSession)

	event := bytes.Repeat([]byte{1}, netwire.MaxDatagramBytes)
	for _, delivery := range []EventDelivery{EventReliableOrdered, EventReliableUnordered, EventUnreliable} {
		if err := listener.BroadcastEvent(delivery, event, []byte("wrapped")); err == nil || !strings.Contains(err.Error(), "exceeds max") {
			t.Fatalf("BroadcastEvent(%d) error = %v, want a payload budget error", delivery, err)
		}
	}
	if got := len(wsSession.streamSend); got != 0 {
		t.Fatalf("oversized event reached the stream fallback %d times", got)
	}
	if err := listener.SendEvent(9, EventStream, event, []byte("wrapped")); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("SendEvent to a missing session = %v, want ErrSessionNotFound", err)
	}
	if err := listener.BroadcastEvent(EventUnreliable+1, event, []byte("wrapped")); err == nil {
		t.Fatal("unknown event delivery accepted")
	}
}
