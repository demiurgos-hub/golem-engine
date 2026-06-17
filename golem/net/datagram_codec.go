package net

import (
	"encoding/binary"
	"fmt"
)

func validateReliableDatagramMessageSize(lane datagramLane, payload []byte) error {
	if err := validateDatagramLane(lane); err != nil {
		return err
	}
	maxPayload := maxReliableOrderedDatagramPayloadBytes
	if lane == datagramLaneReliableUnordered {
		maxPayload = maxReliableUnorderedDatagramPayloadBytes
	}
	if len(payload) > maxPayload {
		return fmt.Errorf("golem/net: reliable datagram payload size %d exceeds max %d", len(payload), maxPayload)
	}
	return nil
}

func validateUnreliableDatagramPayloadSize(payload []byte) error {
	if len(payload) > maxUnreliableDatagramPayloadBytes {
		return fmt.Errorf("golem/net: unreliable datagram payload size %d exceeds max %d", len(payload), maxUnreliableDatagramPayloadBytes)
	}
	return nil
}

func validateEventualStateDatagramPayloadSize(payload []byte) error {
	if len(payload) > maxEventualStateDatagramPayloadBytes {
		return fmt.Errorf("golem/net: eventual state datagram payload size %d exceeds max %d", len(payload), maxEventualStateDatagramPayloadBytes)
	}
	return nil
}

func validateDatagramLane(lane datagramLane) error {
	switch lane {
	case datagramLaneUnreliable, datagramLaneReliableUnordered, datagramLaneReliableOrdered, datagramLaneEventualState:
		return nil
	default:
		return fmt.Errorf("%w: %d", errDatagramUnknownLane, lane)
	}
}

func encodeDatagramPacket(packet datagramPacket) ([]byte, error) {
	return appendDatagramPacket(nil, packet)
}

func appendDatagramPacket(dst []byte, packet datagramPacket) ([]byte, error) {
	baseLen := datagramPacketHeaderBytes
	if packet.flags&datagramFlagAckOnly == 0 {
		if err := validateDatagramLane(packet.lane); err != nil {
			return nil, err
		}
		baseLen += datagramLaneHeaderBytes
		switch packet.lane {
		case datagramLaneUnreliable:
			if err := validateUnreliableDatagramPayloadSize(packet.payload); err != nil {
				return nil, err
			}
		case datagramLaneReliableUnordered:
			if err := validateReliableDatagramMessageSize(packet.lane, packet.payload); err != nil {
				return nil, err
			}
			baseLen += datagramReliableMessageIDBytes
		case datagramLaneReliableOrdered:
			if err := validateReliableDatagramMessageSize(packet.lane, packet.payload); err != nil {
				return nil, err
			}
			baseLen += datagramReliableMessageIDBytes + datagramReliableOrderedSequenceBytes
		case datagramLaneEventualState:
			if err := validateEventualStateDatagramPayloadSize(packet.payload); err != nil {
				return nil, err
			}
			baseLen += datagramEventualStateTokenBytes
		}
		baseLen += len(packet.payload)
	}
	var out []byte
	if cap(dst) < baseLen {
		out = make([]byte, baseLen)
	} else {
		out = dst[:baseLen]
	}
	binary.BigEndian.PutUint16(out[0:2], packet.packetSeq)
	binary.BigEndian.PutUint16(out[2:4], packet.ackSeq)
	for i, word := range packet.ackMask {
		start := 4 + i*datagramAckMaskWordBytes
		binary.BigEndian.PutUint32(out[start:start+datagramAckMaskWordBytes], word)
	}
	out[datagramPacketHeaderBytes-1] = packet.flags
	if packet.flags&datagramFlagAckOnly != 0 {
		return out, nil
	}
	offset := datagramPacketHeaderBytes
	out[offset] = byte(packet.lane)
	offset++
	switch packet.lane {
	case datagramLaneReliableUnordered:
		binary.BigEndian.PutUint16(out[offset:offset+2], packet.messageID)
		offset += 2
	case datagramLaneReliableOrdered:
		binary.BigEndian.PutUint16(out[offset:offset+2], packet.messageID)
		offset += 2
		binary.BigEndian.PutUint16(out[offset:offset+2], packet.orderedSeq)
		offset += 2
	case datagramLaneEventualState:
		binary.BigEndian.PutUint64(out[offset:offset+datagramEventualStateTokenBytes], packet.stateToken)
		offset += datagramEventualStateTokenBytes
	}
	copy(out[offset:], packet.payload)
	return out, validateDatagramSize(out)
}

func decodeDatagramPacket(data []byte) (datagramPacket, error) {
	if len(data) < datagramPacketHeaderBytes {
		return datagramPacket{}, errDatagramPacketTooSmall
	}
	packet := datagramPacket{
		packetSeq: binary.BigEndian.Uint16(data[0:2]),
		ackSeq:    binary.BigEndian.Uint16(data[2:4]),
		flags:     data[datagramPacketHeaderBytes-1],
	}
	for i := range packet.ackMask {
		start := 4 + i*datagramAckMaskWordBytes
		packet.ackMask[i] = binary.BigEndian.Uint32(data[start : start+datagramAckMaskWordBytes])
	}
	if packet.flags&datagramFlagAckOnly != 0 {
		return packet, nil
	}
	if len(data) < datagramPacketHeaderBytes+datagramLaneHeaderBytes {
		return datagramPacket{}, errDatagramPacketTooSmall
	}
	offset := datagramPacketHeaderBytes
	packet.lane = datagramLane(data[offset])
	offset++
	if err := validateDatagramLane(packet.lane); err != nil {
		return datagramPacket{}, err
	}
	switch packet.lane {
	case datagramLaneReliableUnordered:
		if len(data) < offset+datagramReliableMessageIDBytes {
			return datagramPacket{}, errDatagramPacketTooSmall
		}
		packet.messageID = binary.BigEndian.Uint16(data[offset : offset+2])
		offset += 2
	case datagramLaneReliableOrdered:
		if len(data) < offset+datagramReliableMessageIDBytes+datagramReliableOrderedSequenceBytes {
			return datagramPacket{}, errDatagramPacketTooSmall
		}
		packet.messageID = binary.BigEndian.Uint16(data[offset : offset+2])
		offset += 2
		packet.orderedSeq = binary.BigEndian.Uint16(data[offset : offset+2])
		offset += 2
	case datagramLaneEventualState:
		if len(data) < offset+datagramEventualStateTokenBytes {
			return datagramPacket{}, errDatagramPacketTooSmall
		}
		packet.stateToken = binary.BigEndian.Uint64(data[offset : offset+datagramEventualStateTokenBytes])
		offset += datagramEventualStateTokenBytes
	}
	packet.payload = data[offset:]
	switch packet.lane {
	case datagramLaneUnreliable:
		return packet, validateUnreliableDatagramPayloadSize(packet.payload)
	case datagramLaneEventualState:
		return packet, validateEventualStateDatagramPayloadSize(packet.payload)
	default:
		return packet, validateReliableDatagramMessageSize(packet.lane, packet.payload)
	}
}
