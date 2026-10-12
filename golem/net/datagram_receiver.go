package net

import (
	"fmt"
	"time"

	"github.com/demiurgos-hub/golem-engine/internal/netwire"
)

func (p *datagramProtocolState) handleIncoming(now time.Time, data []byte) ([]datagramDelivery, bool, error) {
	packet, err := decodeDatagramPacket(data)
	if err != nil {
		return nil, false, err
	}
	p.inboundPackets++
	ackedChanged, err := p.applyPeerAcks(packet.ACK.Sequence, packet.ACK.Mask, now)
	if err != nil {
		return nil, false, err
	}
	if packet.Flags&datagramFlagAckOnly != 0 {
		p.inboundAckOnly++
		return nil, ackedChanged, nil
	}
	accepted := p.recvPackets.Accept(packet.Sequence)
	if accepted && !p.ackDirty {
		p.ackDirty = true
		p.ackDueAt = now.Add(datagramAckCoalesceDelay)
	}
	if !accepted {
		return nil, ackedChanged || accepted, nil
	}
	switch packet.Lane {
	case datagramLaneUnreliable:
		return []datagramDelivery{{lane: datagramLaneUnreliable, data: append([]byte(nil), packet.Payload...)}}, true, nil
	case datagramLaneReliableUnordered:
		if !p.recvReliableMsgs.Accept(packet.MessageID) {
			return nil, true, nil
		}
		return []datagramDelivery{{lane: datagramLaneReliableUnordered, data: append([]byte(nil), packet.Payload...)}}, true, nil
	case datagramLaneReliableOrdered:
		deliveries := p.orderedRecv.AcceptCopy(packet.OrderedSequence, packet.Payload, now, netwire.KeepFirst)
		if len(deliveries) == 0 {
			return nil, true, nil
		}
		out := make([]datagramDelivery, len(deliveries))
		for i, payload := range deliveries {
			out[i] = datagramDelivery{lane: datagramLaneReliableOrdered, data: payload}
		}
		return out, true, nil
	case datagramLaneEventualState:
		return []datagramDelivery{{lane: datagramLaneEventualState, data: append([]byte(nil), packet.Payload...)}}, true, nil
	default:
		return nil, true, fmt.Errorf("%w: %d", errDatagramUnknownLane, packet.Lane)
	}
}

func (p *datagramProtocolState) checkOrderedGap(now time.Time) error {
	if gap := p.orderedRecv.GapSince(); !gap.IsZero() && now.Sub(gap) > datagramReliableOrderedGapTimeout {
		return errReliableOrderedDatagramGap
	}
	return nil
}

func seqGreater(a, b uint16) bool { return netwire.Newer(a, b) }
