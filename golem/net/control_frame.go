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
