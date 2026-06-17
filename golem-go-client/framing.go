package golemclient

import (
	"encoding/binary"
	"fmt"
	"io"

	"golem-engine/golem-go-client/pb"
)

const (
	maxReliableMessageBytes      = 32000
	maxWebTransportDatagramBytes = 1200
	reliableFrameHeaderBytes     = 4
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

func appendLengthPrefixedFrame(dst []byte, data []byte) []byte {
	start := len(dst)
	dst = append(dst, 0, 0, 0, 0)
	binary.BigEndian.PutUint32(dst[start:start+4], uint32(len(data)))
	return append(dst, data...)
}

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
	var hdr [reliableFrameHeaderBytes]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n > maxReliableMessageBytes {
		return nil, fmt.Errorf("golem-go-client: reliable frame length %d exceeds max %d", n, maxReliableMessageBytes)
	}
	data := make([]byte, int(n))
	if _, err := io.ReadFull(r, data); err != nil {
		return nil, err
	}
	return data, nil
}

func writeReliableFrame(w io.Writer, data []byte) error {
	if len(data) > maxReliableMessageBytes {
		return fmt.Errorf("golem-go-client: reliable message size %d exceeds max %d", len(data), maxReliableMessageBytes)
	}
	_, err := w.Write(appendLengthPrefixedFrame(nil, data))
	return err
}

func validateDatagramSize(data []byte) error {
	if len(data) > maxWebTransportDatagramBytes {
		return fmt.Errorf("golem-go-client: datagram size %d exceeds max %d", len(data), maxWebTransportDatagramBytes)
	}
	return nil
}
