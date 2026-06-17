package net

import (
	"fmt"
	"time"
)

func (p *datagramProtocolState) handleIncoming(now time.Time, data []byte) ([]datagramDelivery, bool, error) {
	packet, err := decodeDatagramPacket(data)
	if err != nil {
		return nil, false, err
	}
	p.inboundPackets++
	ackedChanged, err := p.applyPeerAcks(packet.ackSeq, packet.ackMask, now)
	if err != nil {
		return nil, false, err
	}
	if packet.flags&datagramFlagAckOnly != 0 {
		p.inboundAckOnly++
		return nil, ackedChanged, nil
	}
	accepted := p.recvPackets.accept(packet.packetSeq)
	if accepted {
		p.ackDirty = true
		p.ackDueAt = now.Add(datagramAckCoalesceDelay)
	}
	if !accepted {
		return nil, ackedChanged || accepted, nil
	}
	switch packet.lane {
	case datagramLaneUnreliable:
		return []datagramDelivery{{lane: datagramLaneUnreliable, data: append([]byte(nil), packet.payload...)}}, true, nil
	case datagramLaneReliableUnordered:
		if !p.recvReliableMsgs.accept(packet.messageID) {
			return nil, true, nil
		}
		return []datagramDelivery{{lane: datagramLaneReliableUnordered, data: append([]byte(nil), packet.payload...)}}, true, nil
	case datagramLaneReliableOrdered:
		deliveries, err := p.orderedRecv.accept(packet.orderedSeq, packet.payload, now)
		if err != nil {
			return nil, true, err
		}
		if len(deliveries) == 0 {
			return nil, true, nil
		}
		out := make([]datagramDelivery, len(deliveries))
		for i, payload := range deliveries {
			out[i] = datagramDelivery{lane: datagramLaneReliableOrdered, data: payload}
		}
		return out, true, nil
	case datagramLaneEventualState:
		return []datagramDelivery{{lane: datagramLaneEventualState, data: append([]byte(nil), packet.payload...)}}, true, nil
	default:
		return nil, true, fmt.Errorf("%w: %d", errDatagramUnknownLane, packet.lane)
	}
}

func (p *datagramProtocolState) checkOrderedGap(now time.Time) error {
	if p.orderedRecv.checkTimeout(now) {
		return errReliableOrderedDatagramGap
	}
	return nil
}

func (w *sequenceWindow) accept(seq uint16) bool {
	if !w.init {
		w.init = true
		w.latest = seq
		w.mask = ackMask128{}
		return true
	}
	if seq == w.latest {
		return false
	}
	if seqGreater(seq, w.latest) {
		delta := seqDistance(w.latest, seq)
		if delta > datagramPacketAckWindow {
			w.mask = ackMask128{}
		} else {
			w.mask.shiftLeft(int(delta))
			w.mask.setBit(int(delta - 1))
		}
		w.latest = seq
		return true
	}
	delta := seqDistance(seq, w.latest)
	if delta == 0 || delta > datagramPacketAckWindow {
		return false
	}
	if w.mask.hasBit(int(delta - 1)) {
		return false
	}
	w.mask.setBit(int(delta - 1))
	return true
}

func (o *orderedReceiveState) accept(seq uint16, payload []byte, now time.Time) ([][]byte, error) {
	if seq == o.nextSeq {
		deliveries := [][]byte{append([]byte(nil), payload...)}
		o.nextSeq++
		for {
			if o.pending == nil {
				break
			}
			next, ok := o.pending[o.nextSeq]
			if !ok {
				break
			}
			deliveries = append(deliveries, next)
			delete(o.pending, o.nextSeq)
			o.nextSeq++
		}
		if len(o.pending) == 0 {
			o.gapSince = time.Time{}
		}
		return deliveries, nil
	}
	if seqGreater(seq, o.nextSeq) {
		if o.pending == nil {
			o.pending = make(map[uint16][]byte)
		}
		if _, exists := o.pending[seq]; !exists {
			o.pending[seq] = append([]byte(nil), payload...)
			if o.gapSince.IsZero() {
				o.gapSince = now
			}
		}
		return nil, nil
	}
	return nil, nil
}

func (o *orderedReceiveState) checkTimeout(now time.Time) bool {
	return !o.gapSince.IsZero() && now.Sub(o.gapSince) > datagramReliableOrderedGapTimeout
}

func seqDistance(older uint16, newer uint16) uint16 {
	return newer - older
}

func seqGreater(a uint16, b uint16) bool {
	return a != b && ((a > b && a-b <= 1<<15) || (a < b && b-a > 1<<15))
}
