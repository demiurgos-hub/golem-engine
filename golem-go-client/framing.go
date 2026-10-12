package golemclient

import (
	"encoding/binary"
	"fmt"
	"io"

	"github.com/demiurgos-hub/golem-engine/golem-go-client/pb"
	"github.com/demiurgos-hub/golem-engine/internal/netwire"
)

const (
	maxReliableMessageBytes      = netwire.MaxReliableMessageBytes
	maxWebTransportDatagramBytes = netwire.MaxDatagramBytes
	reliableFrameHeaderBytes     = netwire.FrameHeaderBytes
)

// ServerMessage contains one decoded server envelope.
type ServerMessage struct {
	EntityUpdate []byte
	WorldUpdate  []byte
	ServerEvent  []byte
}

func decodeServerMessages(data []byte) ([]ServerMessage, error) {
	var out []ServerMessage
	r := pb.NewReader(data)
	for !r.Done() {
		field, wire := r.Tag()
		if wire != 2 {
			r.Skip(wire)
			continue
		}
		msg := ServerMessage{}
		switch field {
		case 1:
			msg.EntityUpdate = r.Bytes()
		case 2:
			msg.WorldUpdate = r.Bytes()
		case 3:
			msg.ServerEvent = r.Bytes()
		default:
			r.Skip(wire)
			continue
		}
		out = append(out, msg)
	}
	return out, nil
}

func appendLengthPrefixedFrame(dst, data []byte) []byte { return netwire.AppendFrame(dst, data) }

func decodeLengthPrefixedFrames(data []byte, fn func([]byte)) error {
	for offset := 0; offset < len(data); {
		if len(data)-offset < reliableFrameHeaderBytes {
			return fmt.Errorf("golem-go-client: length-prefixed batch truncated")
		}
		n := int(binary.BigEndian.Uint32(data[offset : offset+reliableFrameHeaderBytes]))
		offset += reliableFrameHeaderBytes
		if n < 0 || len(data)-offset < n {
			return fmt.Errorf("golem-go-client: length-prefixed frame truncated")
		}
		if fn != nil {
			fn(data[offset : offset+n])
		}
		offset += n
	}
	return nil
}

func readReliableFrame(r io.Reader) ([]byte, error) {
	data, err := netwire.ReadFrame(r)
	if size, ok := err.(netwire.SizeError); ok {
		return nil, fmt.Errorf("golem-go-client: reliable frame length %d exceeds max %d", size.Size, size.Limit)
	}
	return data, err
}

func writeReliableFrame(w io.Writer, data []byte) error {
	err := netwire.WriteFrame(w, data)
	if size, ok := err.(netwire.SizeError); ok {
		return fmt.Errorf("golem-go-client: reliable message size %d exceeds max %d", size.Size, size.Limit)
	}
	return err
}

func validateDatagramSize(data []byte) error {
	if len(data) > maxWebTransportDatagramBytes {
		return fmt.Errorf("golem-go-client: datagram size %d exceeds max %d", len(data), maxWebTransportDatagramBytes)
	}
	return nil
}
