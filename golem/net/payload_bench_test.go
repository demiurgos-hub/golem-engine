package net

import (
	"bytes"
	"testing"
	"time"
)

var (
	benchPayloads [][]byte
	benchPacket   []byte
	benchChanged  bool
)

func benchmarkFrames(n, size int) [][]byte {
	frames := make([][]byte, n)
	for i := range frames {
		frames[i] = bytes.Repeat([]byte{byte('a' + i%26)}, size)
	}
	return frames
}

func benchmarkAckMask(bits int) ackMask128 {
	var mask ackMask128
	for bit := 0; bit < bits; bit++ {
		mask.setBit(bit)
	}
	return mask
}

func BenchmarkEventualStateDatagramPayloads(b *testing.B) {
	frames := benchmarkFrames(12, 80)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		payloads, err := EventualStateDatagramPayloads(frames)
		if err != nil {
			b.Fatal(err)
		}
		benchPayloads = payloads
	}
}

func BenchmarkEncodeDatagramPacket(b *testing.B) {
	packet := datagramPacket{
		packetSeq:  1,
		ackSeq:     2,
		ackMask:    benchmarkAckMask(16),
		lane:       datagramLaneReliableOrdered,
		messageID:  3,
		orderedSeq: 4,
		payload:    bytes.Repeat([]byte("x"), 256),
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		packet.packetSeq = uint16(i)
		out, err := encodeDatagramPacket(packet)
		if err != nil {
			b.Fatal(err)
		}
		benchPacket = out
	}
}

func BenchmarkAppendDatagramPacket(b *testing.B) {
	packet := datagramPacket{
		packetSeq:  1,
		ackSeq:     2,
		ackMask:    benchmarkAckMask(16),
		lane:       datagramLaneReliableOrdered,
		messageID:  3,
		orderedSeq: 4,
		payload:    bytes.Repeat([]byte("x"), 256),
	}
	buf := make([]byte, 0, maxWebTransportDatagramBytes)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		packet.packetSeq = uint16(i)
		out, err := appendDatagramPacket(buf[:0], packet)
		if err != nil {
			b.Fatal(err)
		}
		benchPacket = out
	}
}

func BenchmarkPruneEventualState(b *testing.B) {
	now := time.UnixMilli(0)
	ackMask := benchmarkAckMask(63)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		state := newDatagramProtocolState()
		state.eventualFeedback = make([]EventualStateDelivery, 0, 64)
		for packetSeq := uint16(0); packetSeq < 64; packetSeq++ {
			state.inFlightEventual[packetSeq] = pendingEventualStateDatagram{
				token:  uint64(packetSeq + 1),
				sentAt: now,
			}
		}
		b.StartTimer()
		changed, err := state.pruneEventualState(63, ackMask, now)
		if err != nil {
			b.Fatal(err)
		}
		benchChanged = changed
	}
}

func BenchmarkNextEventualStatePacketLargeQueue(b *testing.B) {
	now := time.UnixMilli(0)
	payload := bytes.Repeat([]byte("x"), 96)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		state := newDatagramProtocolState()
		for token := uint64(1); token <= 128; token++ {
			if err := state.enqueueEventualState(token, payload, now); err != nil {
				b.Fatal(err)
			}
		}
		b.StartTimer()
		for token := uint64(1); token <= 128; token++ {
			packet, ok, err := state.nextEventualStatePacket(now)
			if err != nil || !ok {
				b.Fatalf("nextEventualStatePacket token %d ok=%v err=%v", token, ok, err)
			}
			benchPacket = packet
		}
	}
}

func BenchmarkNextEventualStatePacketNoPendingManyInFlight(b *testing.B) {
	now := time.UnixMilli(0)
	state := newDatagramProtocolState()
	for packetSeq := uint16(0); packetSeq < 128; packetSeq++ {
		state.inFlightEventual[packetSeq] = pendingEventualStateDatagram{
			token:  uint64(packetSeq + 1),
			sentAt: now,
		}
	}
	state.recomputeEventualExpiry()

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		packet, ok, err := state.nextEventualStatePacket(now.Add(time.Millisecond))
		if err != nil {
			b.Fatal(err)
		}
		if ok {
			b.Fatal("nextEventualStatePacket returned packet for no pending sends")
		}
		benchPacket = packet
	}
}
