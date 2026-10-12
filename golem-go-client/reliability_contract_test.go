//go:build !js || !wasm

package golemclient

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/demiurgos-hub/golem-engine/internal/netwire"
)

func TestClientOrderedWaitsForZeroAndIdleGapExpires(t *testing.T) {
	p := newDatagramProtocol(func([]byte) error { return nil }, time.Millisecond)
	now := time.Unix(1000, 0)
	var delivered []string
	deliver := func(_ uint8, b []byte) { p.mu.Lock(); p.mu.Unlock(); delivered = append(delivered, string(b)) }
	if err := p.deliverOrdered(datagramPacket{OrderedSequence: 1, Payload: []byte("one")}, now, deliver); err != nil {
		t.Fatal(err)
	}
	if len(delivered) != 0 {
		t.Fatal("first observed sequence skipped zero")
	}
	if err := p.checkOrderedGap(now.Add(datagramReliableOrderedGapTimeout)); err != nil {
		t.Fatal("timeout must be strict")
	}
	if err := p.checkOrderedGap(now.Add(datagramReliableOrderedGapTimeout + time.Nanosecond)); err == nil {
		t.Fatal("idle gap did not expire")
	}
	if err := p.deliverOrdered(datagramPacket{OrderedSequence: 0, Payload: []byte("zero")}, now.Add(time.Second), deliver); err != nil {
		t.Fatal(err)
	}
	if strings.Join(delivered, ",") != "zero,one" {
		t.Fatalf("deliveries=%v", delivered)
	}
	if err := p.checkOrderedGap(now.Add(time.Hour)); err != nil {
		t.Fatalf("resolved gap expired: %v", err)
	}
}

func TestClientSchedulerDisconnectsOnIdleOrderedGap(t *testing.T) {
	p := newDatagramProtocol(func([]byte) error { return nil }, time.Millisecond)
	if err := p.deliverOrdered(datagramPacket{OrderedSequence: 1}, time.Now().Add(-4*time.Second), nil); err != nil {
		t.Fatal(err)
	}
	closed := make(chan DisconnectInfo, 1)
	c := &WebTransportChannel{protocol: p, done: make(chan struct{}), callbacks: channelCallbacks{onClose: func(info DisconnectInfo) { closed <- info }}}
	c.connected.Store(true)
	t.Cleanup(func() { c.doneOnce.Do(func() { close(c.done) }) })
	go c.schedulerLoop()
	select {
	case info := <-closed:
		if info.WasClean || info.Err == nil || info.Err.Error() != "golem-go-client: reliable ordered datagram gap expired" || c.Connected() {
			t.Fatalf("idle disconnect: %+v", info)
		}
	case <-time.After(time.Second):
		t.Fatal("idle scheduler did not report the expired gap")
	}
}

func TestClientDecodeOwnsPayloadWithoutNewReceiveLimit(t *testing.T) {
	data, err := netwire.EncodePacketInto(nil, netwire.Packet{Lane: netwire.LaneUnreliable, Payload: bytes.Repeat([]byte("x"), 1300)})
	if err != nil {
		t.Fatal(err)
	}
	p, err := decodeDatagramPacket(data)
	if err != nil || len(p.Payload) != 1300 {
		t.Fatalf("decode: %v length %d", err, len(p.Payload))
	}
	data[len(data)-1] = 'y'
	if p.Payload[len(p.Payload)-1] != 'x' {
		t.Fatal("client decoder retained borrowed bytes")
	}
	if _, err := decodeDatagramPacket(make([]byte, datagramPacketHeaderBytes)); err == nil || err.Error() != "golem-go-client: datagram lane missing" {
		t.Fatalf("short lane: %v", err)
	}
}

func TestClientStampingPreservesACKCommitAndFailureState(t *testing.T) {
	now := time.Unix(1000, 0)
	want := errors.New("write failed")
	writes := 0
	p := newDatagramProtocol(func([]byte) error { writes++; return want }, time.Millisecond)
	p.nextPacketSeq = 65535
	p.recvPackets.Accept(10)
	p.scheduleAckLocked(now)
	var packet datagramPacket
	p.stampPacketLocked(&packet)
	if packet.Sequence != 65535 || p.nextPacketSeq != 0 || packet.ACK.Sequence != 10 || !p.ackDirty || p.ackDueAt.IsZero() {
		t.Fatal("stamping changed ACK consumption or wrap")
	}
	if err := p.send(datagramLaneUnreliable, []byte("x")); err != want {
		t.Fatalf("write error: %v", err)
	}
	if p.nextPacketSeq != 1 || p.ackDirty || !p.ackDueAt.IsZero() {
		t.Fatal("write failure rolled back committed state")
	}
	p.scheduleAckLocked(now)
	if err := p.sendReliable(datagramLaneReliableOrdered, make([]byte, 1200), now); err == nil {
		t.Fatal("oversized packet accepted")
	}
	if writes != 1 || p.nextPacketSeq != 2 || p.nextMessageID != 1 || p.nextOrderedSeq != 1 || p.ackDirty || len(p.pendingReliable) != 1 {
		t.Fatal("encode failure changed bookkeeping order")
	}
}

func TestClientRetryAndStreamACKBoundaries(t *testing.T) {
	now := time.Unix(1000, 0)
	var written []datagramPacket
	p := newDatagramProtocol(func(b []byte) error {
		packet, err := decodeDatagramPacket(b)
		written = append(written, packet)
		return err
	}, time.Millisecond)
	if err := p.sendReliable(datagramLaneReliableOrdered, []byte("x"), now); err != nil {
		t.Fatal(err)
	}
	p.recvPackets.Accept(4)
	p.scheduleAckLocked(now)
	if err := p.drainRetries(now); err != nil || !p.ackDirty {
		t.Fatal("no retry consumed ACK")
	}
	if err := p.drainRetries(now.Add(datagramReliableRetryBaseDelay)); err != nil {
		t.Fatal(err)
	}
	if len(written) != 2 || written[1].Sequence == written[0].Sequence || written[1].MessageID != written[0].MessageID || written[1].OrderedSequence != written[0].OrderedSequence || written[1].ACK.Sequence != 4 || p.ackDirty {
		t.Fatalf("retry metadata: %+v", written)
	}
	p.scheduleAckLocked(now)
	if err := p.drainRetries(now.Add(time.Hour)); err == nil || !p.ackDirty {
		t.Fatal("stalled traversal consumed ACK")
	}
	seq := p.nextPacketSeq
	large := make([]byte, maxReliableMessageBytes-netwire.ACKControlHeaderBytes+1)
	if got := p.wrapStreamPayload(large); len(got) != len(large) || !p.ackDirty {
		t.Fatal("oversize piggyback consumed ACK")
	}
	if got := p.wrapStreamPayload(large[:len(large)-1]); len(got) != maxReliableMessageBytes || p.ackDirty || !p.ackDueAt.IsZero() || p.nextPacketSeq != seq {
		t.Fatal("fitting piggyback did not consume only ACK")
	}
}

func TestClientOrderedReplacementAndCrossLaneMessageDedup(t *testing.T) {
	p := newDatagramProtocol(func([]byte) error { return nil }, time.Millisecond)
	var got []string
	for i, packet := range []datagramPacket{
		{Lane: datagramLaneReliableUnordered, MessageID: 7, Payload: []byte("unordered")},
		{Lane: datagramLaneReliableOrdered, OrderedSequence: 0, MessageID: 7, Payload: []byte("duplicate ID")},
		{Lane: datagramLaneReliableOrdered, OrderedSequence: 1, MessageID: 8, Payload: []byte("first")},
		{Lane: datagramLaneReliableOrdered, OrderedSequence: 1, MessageID: 9, Payload: []byte("replacement")},
		{Lane: datagramLaneReliableOrdered, OrderedSequence: 0, MessageID: 10, Payload: []byte("zero")},
	} {
		packet.Sequence = uint16(i)
		data, err := encodeDatagramPacket(packet)
		if err != nil {
			t.Fatal(err)
		}
		if err := p.receive(data, func(_ uint8, payload []byte) { got = append(got, string(payload)) }); err != nil {
			t.Fatal(err)
		}
	}
	if strings.Join(got, ",") != "unordered,zero,replacement" {
		t.Fatalf("receive policy: %v", got)
	}
}

func TestClientACKGapDoesNotAccelerateRetry(t *testing.T) {
	writes := 0
	p := newDatagramProtocol(func([]byte) error { writes++; return nil }, time.Millisecond)
	now := time.Unix(1000, 0)
	if err := p.sendReliable(datagramLaneReliableOrdered, []byte("x"), now); err != nil {
		t.Fatal(err)
	}
	p.applyPeerAcksLocked(1, ackMask128{})
	if len(p.pendingReliable) != 1 {
		t.Fatal("missing ACK bit pruned packet")
	}
	if err := p.drainRetries(now.Add(time.Millisecond)); err != nil || writes != 1 {
		t.Fatal("ACK gap accelerated client retry", err)
	}
	if err := p.drainRetries(now.Add(datagramReliableRetryBaseDelay)); err != nil || writes != 2 {
		t.Fatal("timer retry", err)
	}
}

func TestGameClientUsesDynamicDatagramLimits(t *testing.T) {
	for _, ordered := range []bool{false, true} {
		overhead := 24
		if ordered {
			overhead = 26
		}
		for _, limit := range []int{1, overhead - 1, overhead, 1200, 1500} {
			channel := &fakeChannel{connected: true, maxDatagramBytes: limit}
			client := NewGameClient(GameClientOptions{EncodeCommand: func(v any) ([]byte, error) { return make([]byte, v.(int)), nil }})
			client.channel = channel
			budget := limit - overhead
			if budget >= 0 {
				if err := client.sendCommand(budget, ordered); err != nil {
					t.Fatalf("limit %d boundary: %v", limit, err)
				}
			}
			if err := client.sendCommand(max(0, budget+1), ordered); err == nil {
				t.Fatalf("limit %d exceeded", limit)
			}
			if len(channel.sent) != 0 {
				t.Fatal("oversized datagram fell back to stream")
			}
		}
	}
}
