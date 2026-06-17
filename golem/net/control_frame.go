package net

import (
	"bytes"
	"encoding/binary"
	"fmt"
)

var clientCloseControlFrame = []byte{0x00, 'O', 'G', 'S', 0x01}
var clientReliableAckControlFrame = []byte{0x00, 'O', 'G', 'S', 0x02}

const clientReliableAckControlHeaderBytes = 5 + 2 + datagramAckMaskBytes

func isClientCloseControlFrame(data []byte) bool {
	return bytes.Equal(data, clientCloseControlFrame)
}

// ClientCloseControlFrame returns the reserved reliable frame that asks the
// server to close the session without delivering a game message.
func ClientCloseControlFrame() []byte {
	return append([]byte(nil), clientCloseControlFrame...)
}

// decodeClientReliableAckControlFrame unwraps an optional client stream ACK frame.
func decodeClientReliableAckControlFrame(data []byte) ([]byte, uint16, ackMask128, bool, error) {
	if !bytes.HasPrefix(data, clientReliableAckControlFrame) {
		return data, 0, ackMask128{}, false, nil
	}
	if len(data) < clientReliableAckControlHeaderBytes {
		return nil, 0, ackMask128{}, true, fmt.Errorf("golem/net: reliable ACK control frame too short: %d", len(data))
	}
	offset := len(clientReliableAckControlFrame)
	ackSeq := binary.BigEndian.Uint16(data[offset : offset+2])
	offset += 2
	var ackMask ackMask128
	for i := range ackMask {
		ackMask[i] = binary.BigEndian.Uint32(data[offset : offset+datagramAckMaskWordBytes])
		offset += datagramAckMaskWordBytes
	}
	return data[offset:], ackSeq, ackMask, true, nil
}
