package net

import (
	"fmt"
	"time"
)

func (p *datagramProtocolState) enqueueReliable(lane datagramLane, payload []byte, now time.Time) error {
	if err := validateReliableDatagramMessageSize(lane, payload); err != nil {
		return err
	}
	msg := &reliableDatagramMessage{
		lane:       lane,
		messageID:  p.nextMessageID,
		payload:    append([]byte(nil), payload...),
		queuedAt:   now,
		nextSendAt: now,
	}
	p.nextMessageID++
	if lane == datagramLaneReliableOrdered {
		msg.orderedSeq = p.nextOrderedSeq
		p.nextOrderedSeq++
	}
	queue := p.pendingQueue(lane)
	if len(*queue) >= sendBufSize {
		return fmt.Errorf("golem/net: datagram send buffer overflow for lane %d", lane)
	}
	*queue = append(*queue, msg)
	return nil
}

func (p *datagramProtocolState) enqueueEventualState(token uint64, payload []byte, now time.Time) error {
	if err := validateEventualStateDatagramPayloadSize(payload); err != nil {
		return err
	}
	if p.pendingEventualLen() >= sendBufSize {
		return fmt.Errorf("golem/net: datagram send buffer overflow for lane %d", datagramLaneEventualState)
	}
	p.compactPendingEventualForAppend()
	p.pendingEventual = append(p.pendingEventual, eventualStateDatagram{
		token:    token,
		payload:  payload,
		queuedAt: now,
	})
	return nil
}

func (p *datagramProtocolState) nextAckPacket(now time.Time) ([]byte, bool, error) {
	return p.nextAckPacketInto(now, nil)
}

func (p *datagramProtocolState) nextAckPacketInto(now time.Time, dst []byte) ([]byte, bool, error) {
	if !p.ackDirty || (!p.ackDueAt.IsZero() && now.Before(p.ackDueAt)) {
		return nil, false, nil
	}
	packet, err := p.newPacketInto(datagramPacket{flags: datagramFlagAckOnly}, dst)
	if err != nil {
		return nil, false, err
	}
	p.ackDirty = false
	return packet, true, nil
}

func (p *datagramProtocolState) nextReliablePacket(lane datagramLane, now time.Time, allowResend bool) ([]byte, bool, bool, error) {
	return p.nextReliablePacketInto(lane, now, allowResend, nil)
}

func (p *datagramProtocolState) nextReliablePacketInto(lane datagramLane, now time.Time, allowResend bool, dst []byte) ([]byte, bool, bool, error) {
	queue := p.pendingQueue(lane)
	for _, msg := range *queue {
		if msg.inFlight && now.Before(msg.nextSendAt) {
			continue
		}
		if now.Sub(msg.queuedAt) > datagramReliableMessageTTL || msg.attempts >= datagramReliableRetryLimit {
			return nil, false, false, errReliableDatagramStalled
		}
		if msg.inFlight && !allowResend {
			continue
		}
		packetBytes, err := p.newPacketInto(datagramPacket{
			lane:       lane,
			messageID:  msg.messageID,
			orderedSeq: msg.orderedSeq,
			payload:    msg.payload,
		}, dst)
		if err != nil {
			return nil, false, false, err
		}
		resend := msg.inFlight
		msg.inFlight = true
		msg.lastSentAt = now
		msg.lastPacket = p.lastPacketSeq()
		msg.attempts++
		msg.nextSendAt = now.Add(reliableRetryDelay(msg.attempts))
		return packetBytes, true, resend, nil
	}
	return nil, false, false, nil
}

func (p *datagramProtocolState) nextEventualStatePacket(now time.Time) ([]byte, bool, error) {
	return p.nextEventualStatePacketInto(now, nil)
}

func (p *datagramProtocolState) nextEventualStatePacketInto(now time.Time, dst []byte) ([]byte, bool, error) {
	if len(p.inFlightEventual) > datagramEventualStatePendingLimit {
		return nil, false, errEventualStateDatagramStalled
	}
	if p.pendingEventualLen() == 0 {
		if err := p.checkEventualStateExpiry(now); err != nil {
			return nil, false, err
		}
		return nil, false, nil
	}
	msg := p.popPendingEventual()
	sentAt := now
	packetBytes, err := p.newPacketInto(datagramPacket{
		lane:       datagramLaneEventualState,
		stateToken: msg.token,
		payload:    msg.payload,
	}, dst)
	if err != nil {
		return nil, false, err
	}
	p.inFlightEventual[p.lastPacketSeq()] = pendingEventualStateDatagram{
		token:  msg.token,
		sentAt: sentAt,
	}
	p.trackEventualExpiry(sentAt.Add(datagramEventualStateTTL))
	return packetBytes, true, nil
}

func (p *datagramProtocolState) nextWakeTime(now time.Time) (time.Time, bool) {
	var wake time.Time
	var ok bool
	add := func(t time.Time) {
		if t.IsZero() {
			t = now
		}
		if !ok || t.Before(wake) {
			wake = t
			ok = true
		}
	}
	if p.ackDirty {
		if p.ackDueAt.IsZero() || !p.ackDueAt.After(now) {
			add(now)
		} else {
			add(p.ackDueAt)
		}
	}
	for _, queue := range [][]*reliableDatagramMessage{p.pendingOrdered, p.pendingUnordered} {
		for _, msg := range queue {
			add(msg.queuedAt.Add(datagramReliableMessageTTL))
			if !msg.inFlight || msg.attempts >= datagramReliableRetryLimit {
				add(now)
				continue
			}
			if msg.nextSendAt.After(now) {
				add(msg.nextSendAt)
			} else {
				add(now)
			}
		}
	}
	if p.pendingEventualLen() > 0 {
		add(now)
	}
	if p.eventualExpiry.IsZero() && len(p.inFlightEventual) > 0 {
		p.recomputeEventualExpiry()
	}
	if !p.eventualExpiry.IsZero() {
		add(p.eventualExpiry)
	}
	if !p.orderedRecv.gapSince.IsZero() {
		add(p.orderedRecv.gapSince.Add(datagramReliableOrderedGapTimeout))
	}
	return wake, ok
}

// pendingEventualLen returns the number of queued eventual-state messages.
func (p *datagramProtocolState) pendingEventualLen() int {
	return len(p.pendingEventual) - p.pendingEventualHead
}

// compactPendingEventualForAppend reclaims consumed queue slots before append
// when the backing slice would otherwise grow or too much prefix is stale.
func (p *datagramProtocolState) compactPendingEventualForAppend() {
	if p.pendingEventualHead == 0 {
		return
	}
	if p.pendingEventualHead == len(p.pendingEventual) {
		clear(p.pendingEventual)
		p.pendingEventual = p.pendingEventual[:0]
		p.pendingEventualHead = 0
		return
	}
	if len(p.pendingEventual) < cap(p.pendingEventual) && p.pendingEventualHead <= len(p.pendingEventual)/2 {
		return
	}
	p.compactPendingEventualNow()
}

// popPendingEventual removes and returns the oldest queued eventual-state message.
func (p *datagramProtocolState) popPendingEventual() eventualStateDatagram {
	msg := p.pendingEventual[p.pendingEventualHead]
	p.pendingEventual[p.pendingEventualHead] = eventualStateDatagram{}
	p.pendingEventualHead++
	if p.pendingEventualHead == len(p.pendingEventual) {
		p.pendingEventual = p.pendingEventual[:0]
		p.pendingEventualHead = 0
	} else if p.pendingEventualHead > 32 && p.pendingEventualHead > len(p.pendingEventual)/2 {
		p.compactPendingEventualNow()
	}
	return msg
}

// compactPendingEventualNow moves the live queue suffix to the front.
func (p *datagramProtocolState) compactPendingEventualNow() {
	if p.pendingEventualHead == 0 {
		return
	}
	remaining := copy(p.pendingEventual, p.pendingEventual[p.pendingEventualHead:])
	clear(p.pendingEventual[remaining:])
	p.pendingEventual = p.pendingEventual[:remaining]
	p.pendingEventualHead = 0
}

// trackEventualExpiry records the earliest eventual-state in-flight deadline.
func (p *datagramProtocolState) trackEventualExpiry(expiry time.Time) {
	if p.eventualExpiry.IsZero() || expiry.Before(p.eventualExpiry) {
		p.eventualExpiry = expiry
	}
}

// checkEventualStateExpiry reports stalled delivery once the earliest deadline passes.
func (p *datagramProtocolState) checkEventualStateExpiry(now time.Time) error {
	if p.eventualExpiry.IsZero() && len(p.inFlightEventual) > 0 {
		p.recomputeEventualExpiry()
	}
	if p.eventualExpiry.IsZero() || now.Before(p.eventualExpiry) {
		return nil
	}
	p.recomputeEventualExpiry()
	if p.eventualExpiry.IsZero() || now.Before(p.eventualExpiry) {
		return nil
	}
	return errEventualStateDatagramStalled
}

// recomputeEventualExpiry recalculates the earliest eventual-state deadline.
func (p *datagramProtocolState) recomputeEventualExpiry() {
	var expiry time.Time
	for _, pending := range p.inFlightEventual {
		candidate := pending.sentAt.Add(datagramEventualStateTTL)
		if expiry.IsZero() || candidate.Before(expiry) {
			expiry = candidate
		}
	}
	p.eventualExpiry = expiry
}

func (p *datagramProtocolState) newUnreliablePacket(payload []byte) ([]byte, error) {
	return p.newUnreliablePacketInto(payload, nil)
}

func (p *datagramProtocolState) newUnreliablePacketInto(payload []byte, dst []byte) ([]byte, error) {
	if err := validateUnreliableDatagramPayloadSize(payload); err != nil {
		return nil, err
	}
	return p.newPacketInto(datagramPacket{
		lane:    datagramLaneUnreliable,
		payload: payload,
	}, dst)
}

func (p *datagramProtocolState) lastPacketSeq() uint16 {
	if p.nextPacketSeq == 0 {
		return ^uint16(0)
	}
	return p.nextPacketSeq - 1
}

func (p *datagramProtocolState) newPacket(packet datagramPacket) ([]byte, error) {
	return p.newPacketInto(packet, nil)
}

func (p *datagramProtocolState) newPacketInto(packet datagramPacket, dst []byte) ([]byte, error) {
	packet.packetSeq = p.nextPacketSeq
	packet.ackSeq = p.recvPackets.latest
	packet.ackMask = p.recvPackets.mask
	packetBytes, err := appendDatagramPacket(dst, packet)
	if err != nil {
		return nil, err
	}
	p.nextPacketSeq++
	p.ackDirty = false
	return packetBytes, nil
}

func (p *datagramProtocolState) applyPeerAcks(ackSeq uint16, ackMask ackMask128, now time.Time) (bool, error) {
	if !p.peerPackets.init {
		p.peerPackets.init = true
		p.peerPackets.latest = ackSeq
		p.peerPackets.mask = ackMask
		return p.pruneAckedMessages(ackSeq, ackMask, now)
	}
	if seqGreater(ackSeq, p.peerPackets.latest) || ackSeq == p.peerPackets.latest {
		p.peerPackets.latest = ackSeq
		p.peerPackets.mask = ackMask
	}
	return p.pruneAckedMessages(ackSeq, ackMask, now)
}

func (p *datagramProtocolState) pruneAckedMessages(ackSeq uint16, ackMask ackMask128, now time.Time) (bool, error) {
	changedOrdered, errOrdered := pruneReliableQueue(&p.pendingOrdered, ackSeq, ackMask, now)
	changedUnordered, errUnordered := pruneReliableQueue(&p.pendingUnordered, ackSeq, ackMask, now)
	changedEventual, errEventual := p.pruneEventualState(ackSeq, ackMask, now)
	if errOrdered != nil {
		return changedOrdered || changedUnordered || changedEventual, errOrdered
	}
	if errUnordered != nil {
		return changedOrdered || changedUnordered || changedEventual, errUnordered
	}
	if errEventual != nil {
		return changedOrdered || changedUnordered || changedEventual, errEventual
	}
	return changedOrdered || changedUnordered || changedEventual, nil
}

func (p *datagramProtocolState) drainEventualFeedback() []EventualStateDelivery {
	if len(p.eventualFeedback) == 0 {
		return nil
	}
	out := p.eventualFeedback
	p.eventualFeedback = nil
	return out
}

func (p *datagramProtocolState) pruneEventualState(ackSeq uint16, ackMask ackMask128, now time.Time) (bool, error) {
	if len(p.inFlightEventual) == 0 {
		return false, nil
	}
	changed := false
	for packetSeq, pending := range p.inFlightEventual {
		if now.Sub(pending.sentAt) > datagramEventualStateTTL {
			return changed, errEventualStateDatagramStalled
		}
		switch packetAckState(packetSeq, ackSeq, ackMask) {
		case packetAckDelivered:
			p.eventualFeedback = append(p.eventualFeedback, EventualStateDelivery{Token: pending.token, Delivered: true})
			delete(p.inFlightEventual, packetSeq)
			changed = true
		case packetAckLost:
			p.eventualFeedback = append(p.eventualFeedback, EventualStateDelivery{Token: pending.token, Delivered: false})
			delete(p.inFlightEventual, packetSeq)
			changed = true
		}
	}
	if changed {
		p.recomputeEventualExpiry()
	}
	return changed, nil
}

func pruneReliableQueue(queue *[]*reliableDatagramMessage, ackSeq uint16, ackMask ackMask128, now time.Time) (bool, error) {
	if len(*queue) == 0 {
		return false, nil
	}
	changed := false
	dst := (*queue)[:0]
	for _, msg := range *queue {
		if now.Sub(msg.queuedAt) > datagramReliableMessageTTL || msg.attempts >= datagramReliableRetryLimit {
			return true, errReliableDatagramStalled
		}
		if !msg.inFlight {
			dst = append(dst, msg)
			continue
		}
		switch packetAckState(msg.lastPacket, ackSeq, ackMask) {
		case packetAckDelivered:
			changed = true
			continue
		case packetAckLost:
			msg.inFlight = false
			if msg.nextSendAt.Before(now) {
				msg.nextSendAt = now
			}
			changed = true
		}
		dst = append(dst, msg)
	}
	*queue = dst
	return changed, nil
}

func (p *datagramProtocolState) pendingQueue(lane datagramLane) *[]*reliableDatagramMessage {
	if lane == datagramLaneReliableOrdered {
		return &p.pendingOrdered
	}
	return &p.pendingUnordered
}

func reliableRetryDelay(attempt int) time.Duration {
	delay := datagramReliableRetryBaseDelay
	for i := 1; i < attempt; i++ {
		delay *= 2
		if delay >= datagramReliableRetryMaxDelay {
			return datagramReliableRetryMaxDelay
		}
	}
	if delay > datagramReliableRetryMaxDelay {
		return datagramReliableRetryMaxDelay
	}
	return delay
}

type packetAckStatus uint8

const (
	packetAckPending packetAckStatus = iota
	packetAckDelivered
	packetAckLost
)

func packetAckState(packetSeq uint16, ackSeq uint16, ackMask ackMask128) packetAckStatus {
	if packetSeq == ackSeq {
		return packetAckDelivered
	}
	if !seqGreater(ackSeq, packetSeq) {
		return packetAckPending
	}
	delta := seqDistance(packetSeq, ackSeq)
	if delta == 0 {
		return packetAckPending
	}
	if delta > datagramPacketAckWindow {
		return packetAckLost
	}
	if ackMask.hasBit(int(delta - 1)) {
		return packetAckDelivered
	}
	return packetAckLost
}
