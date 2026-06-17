package net

import (
	"bytes"
	"errors"
	"testing"
	"time"
)

// ackMaskWithBits returns a 128-bit selective-ACK mask with the requested bits set.
func ackMaskWithBits(bits ...int) ackMask128 {
	var mask ackMask128
	for _, bit := range bits {
		mask.setBit(bit)
	}
	return mask
}

func assertNextWakeTime(t *testing.T, state *datagramProtocolState, now time.Time, want time.Time) {
	t.Helper()
	got, ok := state.nextWakeTime(now)
	if !ok {
		t.Fatal("nextWakeTime ok = false, want true")
	}
	if !got.Equal(want) {
		t.Fatalf("nextWakeTime = %v, want %v", got, want)
	}
}

func TestDatagramPacketRoundTripAckOnly(t *testing.T) {
	packet := datagramPacket{
		packetSeq: 7,
		ackSeq:    6,
		ackMask:   ackMask128{0x01020304, 0x05060708, 0x090a0b0c, 0x0d0e0f10},
		flags:     datagramFlagAckOnly,
	}
	encoded, err := encodeDatagramPacket(packet)
	if err != nil {
		t.Fatalf("encodeDatagramPacket: %v", err)
	}
	decoded, err := decodeDatagramPacket(encoded)
	if err != nil {
		t.Fatalf("decodeDatagramPacket: %v", err)
	}
	if decoded.packetSeq != packet.packetSeq || decoded.ackSeq != packet.ackSeq || decoded.ackMask != packet.ackMask || decoded.flags != packet.flags {
		t.Fatalf("decoded packet = %+v, want %+v", decoded, packet)
	}
}

func TestDatagramPacketRoundTripReliableOrdered(t *testing.T) {
	packet := datagramPacket{
		packetSeq:  9,
		ackSeq:     8,
		ackMask:    ackMask128{0xfeedbeef, 0xdecafbad, 0x01020304, 0xaabbccdd},
		lane:       datagramLaneReliableOrdered,
		messageID:  11,
		orderedSeq: 12,
		payload:    []byte("hello"),
	}
	encoded, err := encodeDatagramPacket(packet)
	if err != nil {
		t.Fatalf("encodeDatagramPacket: %v", err)
	}
	decoded, err := decodeDatagramPacket(encoded)
	if err != nil {
		t.Fatalf("decodeDatagramPacket: %v", err)
	}
	if decoded.lane != packet.lane || decoded.messageID != packet.messageID || decoded.orderedSeq != packet.orderedSeq || !bytes.Equal(decoded.payload, packet.payload) {
		t.Fatalf("decoded packet = %+v, want %+v", decoded, packet)
	}
}

func TestDatagramProtocolAckOnlyDoesNotElicitAck(t *testing.T) {
	state := newDatagramProtocolState()
	now := time.UnixMilli(0)
	packet, err := encodeDatagramPacket(datagramPacket{
		packetSeq: 100,
		ackSeq:    7,
		ackMask:   ackMaskWithBits(0, 2),
		flags:     datagramFlagAckOnly,
	})
	if err != nil {
		t.Fatalf("encodeDatagramPacket: %v", err)
	}

	if deliveries, wake, err := state.handleIncoming(now, packet); err != nil {
		t.Fatalf("handleIncoming: %v", err)
	} else if len(deliveries) != 0 || wake {
		t.Fatalf("handleIncoming deliveries=%d wake=%v, want no delivery or wake", len(deliveries), wake)
	}
	if state.ackDirty {
		t.Fatal("ackDirty = true after ACK-only packet")
	}
	if state.recvPackets.init {
		t.Fatal("ACK-only packet advanced receive packet window")
	}
}

func TestDatagramProtocolAckOnlyStillPrunesPeerAcks(t *testing.T) {
	state := newDatagramProtocolState()
	now := time.UnixMilli(0)
	if err := state.enqueueReliable(datagramLaneReliableOrdered, []byte("state"), now); err != nil {
		t.Fatalf("enqueueReliable: %v", err)
	}
	out, ok, _, err := state.nextReliablePacket(datagramLaneReliableOrdered, now, true)
	if err != nil || !ok {
		t.Fatalf("nextReliablePacket ok=%v err=%v", ok, err)
	}
	sent, err := decodeDatagramPacket(out)
	if err != nil {
		t.Fatalf("decodeDatagramPacket: %v", err)
	}
	ack, err := encodeDatagramPacket(datagramPacket{
		packetSeq: 100,
		ackSeq:    sent.packetSeq,
		flags:     datagramFlagAckOnly,
	})
	if err != nil {
		t.Fatalf("encodeDatagramPacket ACK: %v", err)
	}

	if _, wake, err := state.handleIncoming(now.Add(time.Millisecond), ack); err != nil {
		t.Fatalf("handleIncoming ACK: %v", err)
	} else if !wake {
		t.Fatal("handleIncoming ACK wake = false, want true after pruning")
	}
	if len(state.pendingOrdered) != 0 {
		t.Fatalf("pendingOrdered len = %d, want 0", len(state.pendingOrdered))
	}
	if state.ackDirty {
		t.Fatal("ackDirty = true after ACK-only packet")
	}
	if state.recvPackets.init {
		t.Fatal("ACK-only packet advanced receive packet window")
	}
}

func TestDatagramProtocolReliableUnorderedResendAndAck(t *testing.T) {
	state := newDatagramProtocolState()
	now := time.UnixMilli(0)
	if err := state.enqueueReliable(datagramLaneReliableUnordered, []byte("cmd"), now); err != nil {
		t.Fatalf("enqueueReliable: %v", err)
	}
	packet1, ok, resend, err := state.nextReliablePacket(datagramLaneReliableUnordered, now, true)
	if err != nil || !ok || resend {
		t.Fatalf("nextReliablePacket = ok:%v resend:%v err:%v", ok, resend, err)
	}
	decoded1, err := decodeDatagramPacket(packet1)
	if err != nil {
		t.Fatalf("decodeDatagramPacket: %v", err)
	}
	if _, _, resend, err = state.nextReliablePacket(datagramLaneReliableUnordered, now, false); err != nil || resend {
		t.Fatalf("unexpected resend state err:%v resend:%v", err, resend)
	}
	packet2, ok, resend, err := state.nextReliablePacket(datagramLaneReliableUnordered, now.Add(datagramReliableRetryBaseDelay), true)
	if err != nil || !ok || !resend {
		t.Fatalf("resend packet = ok:%v resend:%v err:%v", ok, resend, err)
	}
	decoded2, err := decodeDatagramPacket(packet2)
	if err != nil {
		t.Fatalf("decodeDatagramPacket: %v", err)
	}
	state.applyPeerAcks(decoded2.packetSeq, ackMask128{}, now.Add(2*datagramReliableRetryBaseDelay))
	if len(state.pendingUnordered) != 0 {
		t.Fatalf("pendingUnordered len = %d, want 0", len(state.pendingUnordered))
	}
	if decoded1.messageID != decoded2.messageID {
		t.Fatalf("messageID changed across resend: %d vs %d", decoded1.messageID, decoded2.messageID)
	}
}

func TestDatagramProtocolEventualStateAckFeedback(t *testing.T) {
	state := newDatagramProtocolState()
	now := time.UnixMilli(0)
	if err := state.enqueueEventualState(99, []byte("state"), now); err != nil {
		t.Fatalf("enqueueEventualState: %v", err)
	}
	packet, ok, err := state.nextEventualStatePacket(now)
	if err != nil || !ok {
		t.Fatalf("nextEventualStatePacket ok=%v err=%v", ok, err)
	}
	decoded, err := decodeDatagramPacket(packet)
	if err != nil {
		t.Fatalf("decodeDatagramPacket: %v", err)
	}
	if decoded.lane != datagramLaneEventualState || decoded.stateToken != 99 {
		t.Fatalf("decoded eventual packet = %+v", decoded)
	}
	if _, err := state.applyPeerAcks(decoded.packetSeq, ackMask128{}, now.Add(time.Millisecond)); err != nil {
		t.Fatalf("applyPeerAcks: %v", err)
	}
	feedback := state.drainEventualFeedback()
	if len(feedback) != 1 || feedback[0].Token != 99 || !feedback[0].Delivered {
		t.Fatalf("feedback = %+v, want delivered token 99", feedback)
	}
}

func TestDatagramProtocolEventualStateLossFeedback(t *testing.T) {
	state := newDatagramProtocolState()
	now := time.UnixMilli(0)
	if err := state.enqueueEventualState(7, []byte("old"), now); err != nil {
		t.Fatalf("enqueueEventualState: %v", err)
	}
	packet, ok, err := state.nextEventualStatePacket(now)
	if err != nil || !ok {
		t.Fatalf("nextEventualStatePacket ok=%v err=%v", ok, err)
	}
	decoded, err := decodeDatagramPacket(packet)
	if err != nil {
		t.Fatalf("decodeDatagramPacket: %v", err)
	}
	if _, err := state.applyPeerAcks(decoded.packetSeq+1, ackMask128{}, now.Add(time.Millisecond)); err != nil {
		t.Fatalf("applyPeerAcks: %v", err)
	}
	feedback := state.drainEventualFeedback()
	if len(feedback) != 1 || feedback[0].Token != 7 || feedback[0].Delivered {
		t.Fatalf("feedback = %+v, want lost token 7", feedback)
	}
}

func TestDatagramProtocolPendingEventualQueueFIFOAndReuse(t *testing.T) {
	state := newDatagramProtocolState()
	now := time.UnixMilli(0)

	for token := uint64(1); token <= 70; token++ {
		if err := state.enqueueEventualState(token, []byte{byte(token)}, now); err != nil {
			t.Fatalf("enqueueEventualState %d: %v", token, err)
		}
	}
	for token := uint64(1); token <= 40; token++ {
		packet, ok, err := state.nextEventualStatePacket(now)
		if err != nil || !ok {
			t.Fatalf("nextEventualStatePacket token %d ok=%v err=%v", token, ok, err)
		}
		decoded, err := decodeDatagramPacket(packet)
		if err != nil {
			t.Fatalf("decodeDatagramPacket token %d: %v", token, err)
		}
		if decoded.stateToken != token {
			t.Fatalf("stateToken = %d, want %d", decoded.stateToken, token)
		}
	}
	if got, want := state.pendingEventualLen(), 30; got != want {
		t.Fatalf("pendingEventualLen after partial drain = %d, want %d", got, want)
	}

	for token := uint64(71); token <= 110; token++ {
		if err := state.enqueueEventualState(token, []byte{byte(token)}, now); err != nil {
			t.Fatalf("enqueueEventualState reused %d: %v", token, err)
		}
	}
	for token := uint64(41); token <= 110; token++ {
		packet, ok, err := state.nextEventualStatePacket(now)
		if err != nil || !ok {
			t.Fatalf("nextEventualStatePacket reused token %d ok=%v err=%v", token, ok, err)
		}
		decoded, err := decodeDatagramPacket(packet)
		if err != nil {
			t.Fatalf("decodeDatagramPacket reused token %d: %v", token, err)
		}
		if decoded.stateToken != token {
			t.Fatalf("reused stateToken = %d, want %d", decoded.stateToken, token)
		}
	}
	if got := state.pendingEventualLen(); got != 0 {
		t.Fatalf("pendingEventualLen after full drain = %d, want 0", got)
	}
	if state.pendingEventualHead != 0 {
		t.Fatalf("pendingEventualHead after full drain = %d, want 0", state.pendingEventualHead)
	}
}

func TestDatagramProtocolPendingEventualQueueOverflowCountsLiveItems(t *testing.T) {
	state := newDatagramProtocolState()
	now := time.UnixMilli(0)

	for token := uint64(1); token <= sendBufSize; token++ {
		if err := state.enqueueEventualState(token, []byte("x"), now); err != nil {
			t.Fatalf("enqueueEventualState %d: %v", token, err)
		}
	}
	if err := state.enqueueEventualState(uint64(sendBufSize+1), []byte("overflow"), now); err == nil {
		t.Fatal("enqueueEventualState overflow returned nil")
	}
	if _, ok, err := state.nextEventualStatePacket(now); err != nil || !ok {
		t.Fatalf("nextEventualStatePacket after full queue ok=%v err=%v", ok, err)
	}
	if err := state.enqueueEventualState(uint64(sendBufSize+1), []byte("reused"), now); err != nil {
		t.Fatalf("enqueueEventualState after one pop: %v", err)
	}
	if got, want := state.pendingEventualLen(), sendBufSize; got != want {
		t.Fatalf("pendingEventualLen after reuse = %d, want %d", got, want)
	}
}

func TestDatagramProtocolEventualStateRefreshesLossPastAckWindow(t *testing.T) {
	state := newDatagramProtocolState()
	now := time.UnixMilli(0)
	state.inFlightEventual[10] = pendingEventualStateDatagram{
		token:  55,
		sentAt: now,
	}

	if _, err := state.applyPeerAcks(10+datagramPacketAckWindow+1, ackMask128{}, now.Add(time.Millisecond)); err != nil {
		t.Fatalf("applyPeerAcks: %v", err)
	}
	if len(state.inFlightEventual) != 0 {
		t.Fatalf("inFlightEventual len = %d, want 0", len(state.inFlightEventual))
	}
	feedback := state.drainEventualFeedback()
	if len(feedback) != 1 || feedback[0].Token != 55 || feedback[0].Delivered {
		t.Fatalf("feedback = %+v, want lost token 55", feedback)
	}
}

func TestDatagramProtocolEventualStateExpiryDeadline(t *testing.T) {
	state := newDatagramProtocolState()
	now := time.UnixMilli(0)
	if err := state.enqueueEventualState(1, []byte("state"), now); err != nil {
		t.Fatalf("enqueueEventualState: %v", err)
	}
	if _, ok, err := state.nextEventualStatePacket(now); err != nil || !ok {
		t.Fatalf("nextEventualStatePacket ok=%v err=%v", ok, err)
	}

	beforeExpiry := now.Add(datagramEventualStateTTL - time.Millisecond)
	if _, ok, err := state.nextEventualStatePacket(beforeExpiry); err != nil || ok {
		t.Fatalf("before expiry ok=%v err=%v, want no packet and no error", ok, err)
	}

	afterExpiry := now.Add(datagramEventualStateTTL + time.Millisecond)
	if _, ok, err := state.nextEventualStatePacket(afterExpiry); !errors.Is(err, errEventualStateDatagramStalled) || ok {
		t.Fatalf("after expiry ok=%v err=%v, want errEventualStateDatagramStalled", ok, err)
	}
}

func TestDatagramProtocolEventualStateExpiryRecomputesAfterAck(t *testing.T) {
	state := newDatagramProtocolState()
	now := time.UnixMilli(0)
	state.inFlightEventual[1] = pendingEventualStateDatagram{token: 1, sentAt: now}
	state.inFlightEventual[2] = pendingEventualStateDatagram{token: 2, sentAt: now.Add(time.Second)}
	state.recomputeEventualExpiry()

	if want := now.Add(datagramEventualStateTTL); !state.eventualExpiry.Equal(want) {
		t.Fatalf("initial eventualExpiry = %v, want %v", state.eventualExpiry, want)
	}
	if _, err := state.applyPeerAcks(1, ackMask128{}, now.Add(time.Millisecond)); err != nil {
		t.Fatalf("applyPeerAcks: %v", err)
	}
	if want := now.Add(time.Second).Add(datagramEventualStateTTL); !state.eventualExpiry.Equal(want) {
		t.Fatalf("eventualExpiry after ack = %v, want %v", state.eventualExpiry, want)
	}
}

func TestDatagramProtocolOrderedDeliveryBlocksUntilGapFilled(t *testing.T) {
	state := newDatagramProtocolState()
	now := time.UnixMilli(0)
	p2, err := encodeDatagramPacket(datagramPacket{
		packetSeq:  2,
		ackSeq:     0,
		ackMask:    ackMask128{},
		lane:       datagramLaneReliableOrdered,
		messageID:  2,
		orderedSeq: 1,
		payload:    []byte("two"),
	})
	if err != nil {
		t.Fatalf("encodeDatagramPacket: %v", err)
	}
	deliveries, _, err := state.handleIncoming(now, p2)
	if err != nil {
		t.Fatalf("handleIncoming: %v", err)
	}
	if len(deliveries) != 0 {
		t.Fatalf("deliveries len = %d, want 0", len(deliveries))
	}
	p1, err := encodeDatagramPacket(datagramPacket{
		packetSeq:  1,
		ackSeq:     0,
		ackMask:    ackMask128{},
		lane:       datagramLaneReliableOrdered,
		messageID:  1,
		orderedSeq: 0,
		payload:    []byte("one"),
	})
	if err != nil {
		t.Fatalf("encodeDatagramPacket: %v", err)
	}
	deliveries, _, err = state.handleIncoming(now.Add(time.Millisecond), p1)
	if err != nil {
		t.Fatalf("handleIncoming second: %v", err)
	}
	if len(deliveries) != 2 {
		t.Fatalf("deliveries len = %d, want 2", len(deliveries))
	}
	if !bytes.Equal(deliveries[0].data, []byte("one")) || !bytes.Equal(deliveries[1].data, []byte("two")) {
		t.Fatalf("deliveries = %+v", deliveries)
	}
}

func TestDatagramProtocolOrderedGapExpires(t *testing.T) {
	state := newDatagramProtocolState()
	now := time.UnixMilli(0)
	packet, err := encodeDatagramPacket(datagramPacket{
		packetSeq:  1,
		ackSeq:     0,
		ackMask:    ackMask128{},
		lane:       datagramLaneReliableOrdered,
		messageID:  1,
		orderedSeq: 1,
		payload:    []byte("late"),
	})
	if err != nil {
		t.Fatalf("encodeDatagramPacket: %v", err)
	}
	if _, _, err := state.handleIncoming(now, packet); err != nil {
		t.Fatalf("handleIncoming: %v", err)
	}
	if err := state.checkOrderedGap(now.Add(datagramReliableOrderedGapTimeout + time.Millisecond)); err == nil {
		t.Fatal("checkOrderedGap returned nil after timeout")
	}
}

func TestDatagramProtocolHandleIncomingPropagatesAckPruneStall(t *testing.T) {
	state := newDatagramProtocolState()
	now := time.UnixMilli(0)
	state.pendingOrdered = append(state.pendingOrdered, &reliableDatagramMessage{
		lane:      datagramLaneReliableOrdered,
		messageID: 1,
		payload:   []byte("stalled"),
		queuedAt:  now.Add(-datagramReliableMessageTTL - time.Millisecond),
		inFlight:  true,
	})
	packet, err := encodeDatagramPacket(datagramPacket{
		packetSeq: 1,
		ackSeq:    0,
		ackMask:   ackMask128{},
		flags:     datagramFlagAckOnly,
	})
	if err != nil {
		t.Fatalf("encodeDatagramPacket: %v", err)
	}
	if _, _, err := state.handleIncoming(now, packet); !errors.Is(err, errReliableDatagramStalled) {
		t.Fatalf("handleIncoming error = %v, want errReliableDatagramStalled", err)
	}
}

func TestDatagramProtocolNextWakeTimeAckCoalesce(t *testing.T) {
	state := newDatagramProtocolState()
	now := time.UnixMilli(1000)
	state.ackDirty = true
	state.ackDueAt = now.Add(5 * time.Millisecond)

	assertNextWakeTime(t, state, now, state.ackDueAt)
}

func TestDatagramProtocolNextWakeTimeReliableRetry(t *testing.T) {
	state := newDatagramProtocolState()
	now := time.UnixMilli(1000)
	if err := state.enqueueReliable(datagramLaneReliableOrdered, []byte("retry"), now); err != nil {
		t.Fatalf("enqueueReliable: %v", err)
	}
	if _, ok, _, err := state.nextReliablePacket(datagramLaneReliableOrdered, now, true); err != nil || !ok {
		t.Fatalf("nextReliablePacket ok=%v err=%v", ok, err)
	}

	assertNextWakeTime(t, state, now, now.Add(datagramReliableRetryBaseDelay))
}

func TestDatagramProtocolNextWakeTimeReliableTTL(t *testing.T) {
	state := newDatagramProtocolState()
	now := time.UnixMilli(1000)
	state.pendingOrdered = append(state.pendingOrdered, &reliableDatagramMessage{
		lane:       datagramLaneReliableOrdered,
		messageID:  1,
		payload:    []byte("ttl"),
		queuedAt:   now.Add(-datagramReliableMessageTTL + 10*time.Millisecond),
		nextSendAt: now.Add(time.Hour),
		inFlight:   true,
	})

	assertNextWakeTime(t, state, now, now.Add(10*time.Millisecond))
}

func TestDatagramProtocolNextWakeTimeEventualStateTTL(t *testing.T) {
	state := newDatagramProtocolState()
	now := time.UnixMilli(1000)
	state.inFlightEventual[1] = pendingEventualStateDatagram{
		token:  1,
		sentAt: now.Add(-datagramEventualStateTTL + 20*time.Millisecond),
	}

	assertNextWakeTime(t, state, now, now.Add(20*time.Millisecond))
}

func TestDatagramProtocolNextWakeTimeOrderedGap(t *testing.T) {
	state := newDatagramProtocolState()
	now := time.UnixMilli(1000)
	state.orderedRecv.gapSince = now.Add(-datagramReliableOrderedGapTimeout + 30*time.Millisecond)

	assertNextWakeTime(t, state, now, now.Add(30*time.Millisecond))
}

func TestDatagramPacketReliableOrderedMaxPayloadFitsCeiling(t *testing.T) {
	packet, err := encodeDatagramPacket(datagramPacket{
		packetSeq:  1,
		ackSeq:     0,
		ackMask:    ackMask128{},
		lane:       datagramLaneReliableOrdered,
		messageID:  1,
		orderedSeq: 1,
		payload:    bytes.Repeat([]byte("x"), maxReliableOrderedDatagramPayloadBytes),
	})
	if err != nil {
		t.Fatalf("encodeDatagramPacket: %v", err)
	}
	if got, want := len(packet), maxWebTransportDatagramBytes; got != want {
		t.Fatalf("packet len = %d, want %d", got, want)
	}
}

func TestEventualStateDatagramFramePayloadLenMatchesChunkingBudget(t *testing.T) {
	frame := bytes.Repeat([]byte("x"), EventualStateDatagramPayloadBudget()-reliableFrameHeaderBytes)
	if got, want := EventualStateDatagramFramePayloadLen(frame), EventualStateDatagramPayloadBudget(); got != want {
		t.Fatalf("EventualStateDatagramFramePayloadLen = %d, want %d", got, want)
	}
	if _, err := EventualStateDatagramPayloads([][]byte{frame}); err != nil {
		t.Fatalf("EventualStateDatagramPayloads exact budget: %v", err)
	}

	oversized := append(frame, 'x')
	if got, want := EventualStateDatagramFramePayloadLen(oversized), EventualStateDatagramPayloadBudget()+1; got != want {
		t.Fatalf("oversized EventualStateDatagramFramePayloadLen = %d, want %d", got, want)
	}
	if _, err := EventualStateDatagramPayloads([][]byte{oversized}); err == nil {
		t.Fatal("EventualStateDatagramPayloads returned nil for oversized frame")
	}
}

func TestPacketAckStateSupportsBitsBeyondLegacyWindow(t *testing.T) {
	if got := packetAckState(60, 100, ackMaskWithBits(39)); got != packetAckDelivered {
		t.Fatalf("packetAckState delta40 = %v, want delivered", got)
	}
	if got := packetAckState(1, 129, ackMaskWithBits(127)); got != packetAckDelivered {
		t.Fatalf("packetAckState delta128 = %v, want delivered", got)
	}
	if got := packetAckState(1, 129, ackMask128{}); got != packetAckLost {
		t.Fatalf("packetAckState missing delta128 bit = %v, want lost", got)
	}
}

func TestPruneReliableQueueAcksPacketsBeyondLegacyWindow(t *testing.T) {
	now := time.UnixMilli(0)
	queue := []*reliableDatagramMessage{{
		lane:       datagramLaneReliableOrdered,
		messageID:  7,
		payload:    []byte("deep"),
		queuedAt:   now,
		inFlight:   true,
		lastPacket: 11,
	}}

	changed, err := pruneReliableQueue(&queue, 51, ackMaskWithBits(39), now)
	if err != nil {
		t.Fatalf("pruneReliableQueue: %v", err)
	}
	if !changed {
		t.Fatal("pruneReliableQueue changed = false, want true")
	}
	if len(queue) != 0 {
		t.Fatalf("queue len = %d, want 0", len(queue))
	}
}

func TestReliableRetryDelayBackoffCaps(t *testing.T) {
	tests := []struct {
		attempt int
		want    time.Duration
	}{
		{attempt: 1, want: 75 * time.Millisecond},
		{attempt: 2, want: 150 * time.Millisecond},
		{attempt: 3, want: 300 * time.Millisecond},
		{attempt: 4, want: 400 * time.Millisecond},
		{attempt: 8, want: 400 * time.Millisecond},
	}
	for _, tt := range tests {
		if got := reliableRetryDelay(tt.attempt); got != tt.want {
			t.Fatalf("reliableRetryDelay(%d) = %v, want %v", tt.attempt, got, tt.want)
		}
	}
}

func TestSequenceWindowWraparound(t *testing.T) {
	var w sequenceWindow
	if !w.accept(65534) {
		t.Fatal("accept 65534 = false, want true")
	}
	if !w.accept(65535) {
		t.Fatal("accept 65535 = false, want true")
	}
	if !w.accept(0) {
		t.Fatal("accept 0 after wrap = false, want true")
	}
	if !w.accept(1) {
		t.Fatal("accept 1 after wrap = false, want true")
	}
	if w.accept(65535) {
		t.Fatal("accept duplicate wrapped predecessor = true, want false")
	}
}

func TestSequenceWindowTracksBitsBeyondLegacyWindow(t *testing.T) {
	var w sequenceWindow
	if !w.accept(1) {
		t.Fatal("accept 1 = false, want true")
	}
	if !w.accept(41) {
		t.Fatal("accept 41 = false, want true")
	}
	if !w.mask.hasBit(39) {
		t.Fatal("bit 39 = false, want true")
	}
}
