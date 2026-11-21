package net

import (
	"fmt"

	"github.com/demiurgos-hub/golem-engine/internal/netwire"
)

const clientReliableAckControlFrame = netwire.ACKControlPrefix

const clientReliableAckControlHeaderBytes = netwire.ACKControlHeaderBytes

func isClientCloseControlFrame(data []byte) bool { return netwire.IsCloseControl(data) }

// ClientCloseControlFrame returns independent bytes for the reserved reliable close request.
func ClientCloseControlFrame() []byte { return netwire.AppendCloseControl(nil) }

func decodeClientReliableAckControlFrame(data []byte) ([]byte, uint16, ackMask128, bool, error) {
	payload, ack, matched, err := netwire.DecodeACKControl(data)
	if err != nil {
		return nil, 0, ackMask128{}, true, fmt.Errorf("golem/net: reliable ACK control frame too short: %d", len(data))
	}
	return payload, ack.Sequence, ack.Mask, matched, nil
}

// ClientSpaceAckControlFrame returns the reliable control frame a client sends
// to acknowledge the space transition with the given counter.
func ClientSpaceAckControlFrame(transition uint64) []byte {
	return netwire.AppendSpaceAckControl(nil, transition)
}

func decodeClientSpaceAckControlFrame(data []byte) (uint64, bool, error) {
	transition, matched, err := netwire.DecodeSpaceAckControl(data)
	if err != nil {
		return 0, true, fmt.Errorf("golem/net: SpaceAck control frame has %d bytes, want %d", len(data), netwire.SpaceAckControlBytes)
	}
	return transition, matched, nil
}

// ClientEntityResyncControlFrame returns the reliable control frame a client
// sends to request a full state of entityID after it evicted compact records
// held for that entity.
func ClientEntityResyncControlFrame(entityID int64) []byte {
	return netwire.AppendEntityResyncControl(nil, entityID)
}

func decodeClientEntityResyncControlFrame(data []byte) (int64, bool, error) {
	entityID, matched, err := netwire.DecodeEntityResyncControl(data)
	if err != nil {
		return 0, true, fmt.Errorf("golem/net: EntityResync control frame has %d bytes, want %d", len(data), netwire.EntityResyncControlBytes)
	}
	return entityID, matched, nil
}
