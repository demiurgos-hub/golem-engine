package net

import (
	"bytes"
	"testing"
	"time"

	"github.com/demiurgos-hub/golem-engine/golem/registry"
)

// markSent marks every queued reliable message as sent in its own packet,
// numbered from first, as the scheduler would.
func markSent(p *datagramProtocolState, first uint16) {
	seq := first
	for _, queue := range [][]*reliableDatagramMessage{p.pendingOrdered, p.pendingUnordered} {
		for _, msg := range queue {
			msg.inFlight, msg.lastPacket, msg.attempts = true, seq, 1
			seq++
		}
	}
}

func ackPacket(t *testing.T, p *datagramProtocolState, seq uint16) {
	t.Helper()
	if _, err := p.applyPeerAcks(seq, ackMask128{}, time.Now()); err != nil {
		t.Fatalf("applyPeerAcks(%d): %v", seq, err)
	}
}

func TestListenerEventDrainWaitsForEarlierReliableDatagrams(t *testing.T) {
	listener := NewListener(registry.NewRegistry(), Config{Transport: TransportWebTransport})
	wt := newSession(1, &captureReliableChannel{}, &captureDatagramChannel{}, nil)
	listener.addSession(wt)
	defer listener.removeSession(wt)

	event, stream := []byte{0x0a, 0x02, 0x08, 0x01}, []byte("wrapped")
	if err := listener.SendEvent(1, EventReliableOrdered, event, stream); err != nil {
		t.Fatal(err)
	}
	if err := listener.SendEvent(1, EventReliableUnordered, event, stream); err != nil {
		t.Fatal(err)
	}
	// Raw reliable sends count as well as event records.
	if err := wt.SendReliableOrdered([]byte("raw")); err != nil {
		t.Fatal(err)
	}

	mark, reliable, ok := listener.BeginEventDrain(1)
	if !ok || !reliable || mark != 3 {
		t.Fatalf("BeginEventDrain = (%d, %v, %v), want (3, true, true)", mark, reliable, ok)
	}
	// From the drain on, datagram-lane events take the stream.
	if err := listener.SendEvent(1, EventReliableOrdered, event, stream); err != nil {
		t.Fatal(err)
	}
	if batch := <-wt.streamSend; len(batch) != 1 || !bytes.Equal(batch[0], stream) {
		t.Fatalf("event after BeginEventDrain = %q, want the stream frame", batch)
	}
	if got := len(wt.protocol.pendingOrdered) + len(wt.protocol.pendingUnordered); got != 3 {
		t.Fatalf("reliable queue = %d, want only the three earlier messages", got)
	}

	if settled, ok := listener.ReliableSettled(1, mark); !ok || settled {
		t.Fatalf("ReliableSettled before any send = (%v, %v), want (false, true)", settled, ok)
	}
	// Ordered messages 0 and 2 go out in packets 10 and 11, unordered 1 in 12.
	markSent(wt.protocol, 10)
	ackPacket(t, wt.protocol, 10)
	ackPacket(t, wt.protocol, 11)
	if settled, _ := listener.ReliableSettled(1, mark); settled {
		t.Fatal("settled while the unordered message is in flight")
	}
	// A loss report keeps the message queued for a resend.
	ackPacket(t, wt.protocol, 13)
	if len(wt.protocol.pendingUnordered) != 1 || wt.protocol.pendingUnordered[0].inFlight {
		t.Fatalf("unordered queue after a loss report = %+v, want one message waiting for a resend", wt.protocol.pendingUnordered)
	}
	if settled, _ := listener.ReliableSettled(1, mark); settled {
		t.Fatal("settled while the lost message waits for its resend")
	}

	// A reliable datagram queued after the watermark does not hold it back.
	if err := wt.SendReliableUnordered([]byte("later")); err != nil {
		t.Fatal(err)
	}
	msg := wt.protocol.pendingUnordered[0]
	msg.inFlight, msg.lastPacket = true, 14
	ackPacket(t, wt.protocol, 14)
	if settled, ok := listener.ReliableSettled(1, mark); !ok || !settled {
		t.Fatalf("ReliableSettled after every earlier message was acknowledged = (%v, %v), want (true, true)", settled, ok)
	}
	if got := wt.protocol.pendingUnordered; len(got) != 1 || !bytes.Equal(got[0].payload, []byte("later")) {
		t.Fatalf("pending after settling = %+v, want only the later message", got)
	}
}

func TestListenerEventDrainWatermarkWrapsAround(t *testing.T) {
	listener := NewListener(registry.NewRegistry(), Config{Transport: TransportWebTransport})
	wt := newSession(1, &captureReliableChannel{}, &captureDatagramChannel{}, nil)
	listener.addSession(wt)
	defer listener.removeSession(wt)

	wt.protocol.nextMessageID = 65534
	for _, payload := range []string{"a", "b"} {
		if err := wt.SendReliableOrdered([]byte(payload)); err != nil {
			t.Fatal(err)
		}
	}
	mark, _, _ := listener.BeginEventDrain(1)
	if mark != 0 {
		t.Fatalf("watermark = %d, want the wrapped ID 0", mark)
	}
	if err := wt.SendReliableOrdered([]byte("after")); err != nil {
		t.Fatal(err)
	}
	if settled, _ := listener.ReliableSettled(1, mark); settled {
		t.Fatal("settled while IDs 65534 and 65535 are queued")
	}
	markSent(wt.protocol, 100)
	ackPacket(t, wt.protocol, 100)
	ackPacket(t, wt.protocol, 101)
	if got := len(wt.protocol.pendingOrdered); got != 1 {
		t.Fatalf("pending = %d, want the message queued after the watermark", got)
	}
	if settled, _ := listener.ReliableSettled(1, mark); !settled {
		t.Fatal("not settled once the messages before a wrapped watermark were acknowledged")
	}
}

func TestListenerEventDrainWithoutReliableLanes(t *testing.T) {
	listener := NewListener(registry.NewRegistry(), Config{Transport: TransportWebTransport})
	ws := newSession(2, &captureReliableChannel{}, nil, nil)
	listener.addSession(ws)
	defer listener.removeSession(ws)

	if _, reliable, ok := listener.BeginEventDrain(2); !ok || reliable {
		t.Fatalf("BeginEventDrain(WebSocket) = (reliable %v, ok %v), want (false, true)", reliable, ok)
	}
	if settled, ok := listener.ReliableSettled(2, 0); !ok || !settled {
		t.Fatalf("ReliableSettled(WebSocket) = (%v, %v), want (true, true)", settled, ok)
	}
	if _, _, ok := listener.BeginEventDrain(9); ok {
		t.Fatal("BeginEventDrain accepted an unknown session")
	}
	if _, ok := listener.ReliableSettled(9, 0); ok {
		t.Fatal("ReliableSettled accepted an unknown session")
	}
}
