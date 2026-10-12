package net

import (
	"errors"
	"fmt"

	"github.com/demiurgos-hub/golem-engine/internal/netwire"
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
	if _, err := netwire.PayloadBudget(0, lane); err != nil {
		return fmt.Errorf("%w: %d", errDatagramUnknownLane, lane)
	}
	return nil
}

func encodeDatagramPacket(packet datagramPacket) ([]byte, error) {
	return appendDatagramPacket(nil, packet)
}

func validatePacketPayload(packet datagramPacket) error {
	if packet.Flags&datagramFlagAckOnly != 0 {
		return nil
	}
	if err := validateDatagramLane(packet.Lane); err != nil {
		return err
	}
	switch packet.Lane {
	case datagramLaneUnreliable:
		return validateUnreliableDatagramPayloadSize(packet.Payload)
	case datagramLaneEventualState:
		return validateEventualStateDatagramPayloadSize(packet.Payload)
	default:
		return validateReliableDatagramMessageSize(packet.Lane, packet.Payload)
	}
}

func appendDatagramPacket(dst []byte, packet datagramPacket) ([]byte, error) {
	if err := validatePacketPayload(packet); err != nil {
		return nil, err
	}
	return netwire.EncodePacketInto(dst, packet)
}

func decodeDatagramPacket(data []byte) (datagramPacket, error) {
	packet, err := netwire.DecodePacket(data)
	if err != nil {
		var laneErr netwire.UnknownLaneError
		if errors.As(err, &laneErr) {
			return datagramPacket{}, fmt.Errorf("%w: %d", errDatagramUnknownLane, laneErr.Lane)
		}
		return datagramPacket{}, errDatagramPacketTooSmall
	}
	return packet, validatePacketPayload(packet)
}
