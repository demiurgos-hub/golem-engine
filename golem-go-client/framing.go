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

// ServerMessage field numbers. One stream frame may concatenate many fields;
// clients apply them in order.
const (
	serverMessageEntityField       = 1
	serverMessageWorldField        = 2
	serverMessageEventField        = 3
	serverMessageSpaceChangedField = 4
	serverMessageSpaceSettledField = 5
	serverMessageWorldClearedField = 6
)

// ServerMessage contains one decoded server envelope field. A stream frame
// decodes into one of these per field, so the type stays small: the field
// number says what Payload is.
type ServerMessage struct {
	// Field is the ServerMessage field number: 1 entity update, 2 world update,
	// 3 server event, 4 space changed, 5 space settled, 6 world cleared.
	Field int
	// Payload is the field's length-delimited payload, which may be empty.
	Payload []byte
}

func decodeServerMessages(data []byte) ([]ServerMessage, error) {
	var out []ServerMessage
	r := pb.NewReader(data)
	for !r.Done() {
		field, wire := r.Tag()
		if wire != 2 || field < serverMessageEntityField || field > serverMessageWorldClearedField {
			r.Skip(wire)
			continue
		}
		out = append(out, ServerMessage{Field: field, Payload: r.Bytes()})
	}
	return out, nil
}

// decodeSpaceChanged decodes SpaceChanged{uint32 space = 1; uint64 transition = 2}.
func decodeSpaceChanged(data []byte) (SpaceID, uint64) {
	var space SpaceID
	var transition uint64
	r := pb.NewReader(data)
	for !r.Done() {
		field, wire := r.Tag()
		switch {
		case field == 1 && wire == 0:
			space = SpaceID(r.Uint32())
		case field == 2 && wire == 0:
			transition = r.Uint64()
		default:
			r.Skip(wire)
		}
	}
	return space, transition
}

// decodeSpaceSettled decodes SpaceSettled{uint64 transition = 1}.
func decodeSpaceSettled(data []byte) uint64 {
	var transition uint64
	r := pb.NewReader(data)
	for !r.Done() {
		field, wire := r.Tag()
		if field == 1 && wire == 0 {
			transition = r.Uint64()
			continue
		}
		r.Skip(wire)
	}
	return transition
}

// decodeWorldCleared decodes WorldCleared{string name = 1}.
func decodeWorldCleared(data []byte) string {
	var name string
	r := pb.NewReader(data)
	for !r.Done() {
		field, wire := r.Tag()
		if field == 1 && wire == 2 {
			name = r.String()
			continue
		}
		r.Skip(wire)
	}
	return name
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
