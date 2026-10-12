package net

import (
	"errors"
	"testing"
	"time"
)

func TestHalfRangeSequenceIsAmbiguous(t *testing.T) {
	for _, pair := range [][2]uint16{{0, 32768}, {1, 32769}, {32767, 65535}} {
		for _, v := range [][2]uint16{pair, {pair[1], pair[0]}} {
			if seqGreater(v[0], v[1]) {
				t.Fatalf("half range %v is newer", v)
			}
			var window sequenceWindow
			window.Accept(v[0])
			if window.Accept(v[1]) {
				t.Fatalf("window advanced half range %v", v)
			}
			if got := packetAckState(v[0], v[1], ackMask128{}); got != packetAckPending {
				t.Fatalf("half range ACK=%v", got)
			}
		}
	}
}

func TestServerACKDeadlineSurvivesContinuousArrivals(t *testing.T) {
	p := newDatagramProtocolState()
	start := time.Unix(1000, 0)
	for seq := uint16(0); seq < 4; seq++ {
		data, err := encodeDatagramPacket(datagramPacket{Sequence: seq, Lane: datagramLaneUnreliable, Payload: []byte("x")})
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err = p.handleIncoming(start.Add(time.Duration(seq)*time.Millisecond/4), data); err != nil {
			t.Fatal(err)
		}
		if !p.ackDueAt.Equal(start.Add(datagramAckCoalesceDelay)) {
			t.Fatalf("deadline moved: %v", p.ackDueAt)
		}
	}
	data, ok, err := p.nextAckPacket(start.Add(datagramAckCoalesceDelay))
	if err != nil || !ok {
		t.Fatalf("due ACK: %v %v", ok, err)
	}
	ack, err := decodeDatagramPacket(data)
	if err != nil || ack.ACK.Sequence != 3 || !ack.ACK.Mask.HasBit(2) {
		t.Fatalf("ACK snapshot: %+v %v", ack, err)
	}
	if p.ackDirty || !p.ackDueAt.IsZero() {
		t.Fatal("ACK deadline not cleared")
	}
}

func TestServerDecodeSizeAndBorrowingPolicy(t *testing.T) {
	data, err := encodeDatagramPacket(datagramPacket{Lane: datagramLaneUnreliable, Payload: []byte("x")})
	if err != nil {
		t.Fatal(err)
	}
	packet, err := decodeDatagramPacket(data)
	if err != nil {
		t.Fatal(err)
	}
	data[len(data)-1] = 'y'
	if string(packet.Payload) != "y" {
		t.Fatal("server decoder must borrow")
	}
	tooLarge := append(data[:datagramPacketHeaderBytes+1], make([]byte, maxUnreliableDatagramPayloadBytes+1)...)
	if _, err := decodeDatagramPacket(tooLarge); err == nil {
		t.Fatal("server accepted oversized protocol payload")
	}
	if _, err := decodeDatagramPacket(make([]byte, datagramPacketHeaderBytes)); !errors.Is(err, errDatagramPacketTooSmall) {
		t.Fatalf("short lane error: %v", err)
	}
	bad := append(make([]byte, datagramPacketHeaderBytes), 255)
	if _, err := decodeDatagramPacket(bad); !errors.Is(err, errDatagramUnknownLane) {
		t.Fatalf("unknown lane error: %v", err)
	}
}

func TestServerOrderedKeepsFirstPayloadAndUsesOrderedSequence(t *testing.T) {
	p := newDatagramProtocolState()
	now := time.Unix(1000, 0)
	var got []string
	for i, packet := range []datagramPacket{
		{Lane: datagramLaneReliableOrdered, OrderedSequence: 1, MessageID: 7, Payload: []byte("first")},
		{Lane: datagramLaneReliableOrdered, OrderedSequence: 1, MessageID: 8, Payload: []byte("replacement")},
		{Lane: datagramLaneReliableOrdered, OrderedSequence: 0, MessageID: 7, Payload: []byte("zero")},
	} {
		packet.Sequence = uint16(i)
		data, err := encodeDatagramPacket(packet)
		if err != nil {
			t.Fatal(err)
		}
		deliveries, _, err := p.handleIncoming(now, data)
		if err != nil {
			t.Fatal(err)
		}
		for _, delivery := range deliveries {
			got = append(got, string(delivery.data))
		}
	}
	if len(got) != 2 || got[0] != "zero" || got[1] != "first" {
		t.Fatalf("ordered policy: %v", got)
	}
}

func TestServerACKLossCanRetryBeforeTimer(t *testing.T) {
	p := newDatagramProtocolState()
	now := time.Unix(1000, 0)
	if err := p.enqueueReliable(datagramLaneReliableOrdered, []byte("x"), now); err != nil {
		t.Fatal(err)
	}
	if _, ok, _, err := p.nextReliablePacket(datagramLaneReliableOrdered, now, true); !ok || err != nil {
		t.Fatal("initial send", err)
	}
	// A newer ACK with a missing predecessor bit remains early loss evidence.
	if changed, err := p.applyPeerAcks(1, ackMask128{}, now.Add(time.Millisecond)); !changed || err != nil {
		t.Fatal("ACK loss", err)
	}
	if p.pendingOrdered[0].inFlight {
		t.Fatal("ACK gap did not classify loss")
	}
	if _, ok, _, err := p.nextReliablePacket(datagramLaneReliableOrdered, now.Add(time.Millisecond), false); !ok || err != nil {
		t.Fatal("early retry", err)
	}
}
