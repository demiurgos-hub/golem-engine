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

func TestListenerEventTransitionTagsDatagramRecords(t *testing.T) {
	listener := NewListener(registry.NewRegistry(), Config{Transport: TransportWebTransport})
	wtSession := newSession(1, &captureReliableChannel{}, &captureDatagramChannel{}, nil)
	listener.addSession(wtSession)
	defer listener.removeSession(wtSession)

	event, stream := []byte{0x0a, 0x02, 0x08, 0x01}, []byte("wrapped")
	// Transition 0: the shared untagged record.
	if err := listener.SendEvent(1, EventUnreliable, event, stream); err != nil {
		t.Fatal(err)
	}
	if got := <-wtSession.unreliableSend; !bytes.Equal(got, netwire.AppendEventRecord(nil, event)) {
		t.Fatalf("untagged record = %x", got)
	}

	listener.SetSessionEventTransition(1, 5, true)
	if err := listener.SendEvent(1, EventUnreliable, event, stream); err != nil {
		t.Fatal(err)
	}
	if batch := <-wtSession.streamSend; len(batch) != 1 || !bytes.Equal(batch[0], stream) {
		t.Fatalf("stream-only event = %q", batch)
	}
	// Clearing stream-only keeps the transition tag.
	listener.SetSessionEventsStreamOnly(1, false)
	if err := listener.SendEvent(1, EventUnreliable, event, stream); err != nil {
		t.Fatal(err)
	}
	if got := <-wtSession.unreliableSend; !bytes.Equal(got, netwire.AppendEventRecordTransition(nil, event, 5)) {
		t.Fatalf("tagged unreliable record = %x", got)
	}
	if err := listener.BroadcastEvent(EventReliableOrdered, event, stream); err != nil {
		t.Fatal(err)
	}
	if got := wtSession.protocol.pendingOrdered; len(got) != 1 || !bytes.Equal(got[0].payload, netwire.AppendEventRecordTransition(nil, event, 5)) {
		t.Fatalf("tagged reliable record = %+v", got)
	}

	// An event that fits untagged but not tagged falls back to the stream.
	fits := bytes.Repeat([]byte{1}, maxUnreliableDatagramPayloadBytes-netwire.EventRecordBytes(nil))
	if err := listener.SendEvent(1, EventUnreliable, fits, stream); err != nil {
		t.Fatal(err)
	}
	if batch := <-wtSession.streamSend; len(batch) != 1 || !bytes.Equal(batch[0], stream) {
		t.Fatalf("oversized tagged event = %q, want the stream fallback", batch)
	}
	if len(wtSession.unreliableSend) != 0 {
		t.Fatal("oversized tagged event reached the datagram lane")
	}
}

func TestListenerHeldEventsFlushInOrderBehindConcurrentSends(t *testing.T) {
	listener := NewListener(registry.NewRegistry(), Config{Transport: TransportWebTransport})
	wtSession := newSession(1, &captureReliableChannel{}, &captureDatagramChannel{}, nil)
	listener.addSession(wtSession)
	defer listener.removeSession(wtSession)

	listener.HoldSessionEvents(1, 99)
	for _, name := range []string{"a", "b"} {
		if err := listener.SendEvent(1, EventStream, []byte(name), []byte("stream-"+name)); err != nil {
			t.Fatal(err)
		}
	}
	// The caller may reuse its event buffer once SendEvent returns.
	buf := []byte("u")
	if err := listener.SendEvent(1, EventUnreliable, buf, []byte("stream-u")); err != nil {
		t.Fatal(err)
	}
	buf[0] = 'x'
	if len(wtSession.streamSend) != 0 || len(wtSession.unreliableSend) != 0 {
		t.Fatal("held events were sent before the flush")
	}
	// A transition set while holding applies to the flushed datagram event.
	listener.SetSessionEventTransition(1, 3, false)
	listener.FlushSessionEvents(1, 99)
	if batch := <-wtSession.streamSend; len(batch) != 2 || string(batch[0]) != "stream-a" || string(batch[1]) != "stream-b" {
		t.Fatalf("flushed stream batch = %q", batch)
	}
	if got := <-wtSession.unreliableSend; !bytes.Equal(got, netwire.AppendEventRecordTransition(nil, []byte("u"), 3)) {
		t.Fatalf("flushed datagram event = %x", got)
	}
	// After the flush events go out directly; flushing again is a no-op.
	if err := listener.SendEvent(1, EventStream, []byte("c"), []byte("stream-c")); err != nil {
		t.Fatal(err)
	}
	listener.FlushSessionEvents(1)
	if batch := <-wtSession.streamSend; len(batch) != 1 || string(batch[0]) != "stream-c" {
		t.Fatalf("event after the flush = %q", batch)
	}
	if len(wtSession.streamSend) != 0 {
		t.Fatal("second flush sent something")
	}

	// Stream-only at flush time sends held datagram events on the stream.
	listener.HoldSessionEvents(1)
	if err := listener.SendEvent(1, EventReliableOrdered, []byte("o"), []byte("stream-o")); err != nil {
		t.Fatal(err)
	}
	listener.SetSessionEventsStreamOnly(1, true)
	listener.FlushSessionEvents(1)
	if batch := <-wtSession.streamSend; len(batch) != 1 || string(batch[0]) != "stream-o" {
		t.Fatalf("stream-only flush = %q", batch)
	}
	if len(wtSession.protocol.pendingOrdered) != 0 {
		t.Fatal("stream-only flush used the datagram lane")
	}
}

func TestListenerHeldEventsConcurrentSendersKeepStreamOrder(t *testing.T) {
	listener := NewListener(registry.NewRegistry(), Config{Transport: TransportWebTransport})
	sess := newSession(1, &captureReliableChannel{}, nil, nil)
	listener.addSession(sess)
	defer listener.removeSession(sess)

	listener.HoldSessionEvents(1)
	const held = 50
	for i := 0; i < held; i++ {
		if err := listener.SendEvent(1, EventStream, nil, []byte{byte(i)}); err != nil {
			t.Fatal(err)
		}
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := held; i < 2*held; i++ {
			if err := listener.SendEvent(1, EventStream, nil, []byte{byte(i)}); err != nil {
				t.Error(err)
			}
		}
	}()
	listener.FlushSessionEvents(1)
	<-done
	var got []byte
	for len(sess.streamSend) > 0 {
		for _, frame := range <-sess.streamSend {
			got = append(got, frame...)
		}
	}
	if len(got) != 2*held {
		t.Fatalf("received %d events, want %d", len(got), 2*held)
	}
	// Whether the concurrent sender was held too or ran after the flush, no
	// event overtakes an earlier one.
	for i := range got {
		if got[i] != byte(i) {
			t.Fatalf("events out of order: %v", got)
		}
	}
}
