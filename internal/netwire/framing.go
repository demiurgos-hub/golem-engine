package netwire

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
)

const (
	CloseControlPrefix    = "\x00OGS\x01"
	ACKControlPrefix      = "\x00OGS\x02"
	ACKControlHeaderBytes = len(ACKControlPrefix) + 2 + AckMaskBytes
	// SpaceAckControlPrefix starts the client's reliable acknowledgement of a
	// space transition: the prefix followed by the transition counter as a
	// big-endian uint64, SpaceAckControlBytes in total.
	SpaceAckControlPrefix = "\x00OGS\x03"
	SpaceAckControlBytes  = len(SpaceAckControlPrefix) + 8
	// EntityResyncControlPrefix starts the client's reliable request for a
	// full state of one entity after it evicted compact records held for it:
	// the prefix followed by the entity ID as a big-endian uint64,
	// EntityResyncControlBytes in total.
	EntityResyncControlPrefix = "\x00OGS\x04"
	EntityResyncControlBytes  = len(EntityResyncControlPrefix) + 8
)

// SizeError reports a size boundary without choosing an endpoint's diagnostic.
type SizeError struct{ Size, Limit uint64 }

// Error describes the exceeded boundary.
func (e SizeError) Error() string { return fmt.Sprintf("size %d exceeds limit %d", e.Size, e.Limit) }

// AppendFrame appends a four-byte length and payload, without size validation.
func AppendFrame(dst, payload []byte) []byte {
	start := len(dst)
	dst = append(dst, 0, 0, 0, 0)
	binary.BigEndian.PutUint32(dst[start:start+FrameHeaderBytes], uint32(len(payload)))
	return append(dst, payload...)
}

// EventRecordTag starts a datagram frame that carries one marshalled ServerEvent.
// Compact state records start with a nonzero entity ID varint, so the tag never
// collides with state on a shared datagram lane.
//
// An event record is either untagged, [EventRecordTag][ServerEvent], or tagged
// with the session's space transition, [EventRecordTag][EventRecordTag]
// [uvarint transition][ServerEvent]. A valid non-empty ServerEvent never starts
// with a zero byte (field number 0 is invalid), so a second zero byte always
// marks the tagged form; an empty untagged event is the lone tag byte.
const EventRecordTag byte = 0

// EventRecordBytes returns the datagram payload size of one untagged event record.
func EventRecordBytes(event []byte) int { return FrameHeaderBytes + 1 + len(event) }

// EventRecordTransitionBytes returns the datagram payload size of the event
// record AppendEventRecordTransition writes for transition.
func EventRecordTransitionBytes(event []byte, transition uint64) int {
	if transition == 0 {
		return EventRecordBytes(event)
	}
	return FrameHeaderBytes + 2 + uvarintBytes(transition) + len(event)
}

// AppendEventRecord appends one length-prefixed, untagged event frame, without size validation.
func AppendEventRecord(dst, event []byte) []byte {
	start := len(dst)
	dst = append(dst, 0, 0, 0, 0, EventRecordTag)
	binary.BigEndian.PutUint32(dst[start:start+FrameHeaderBytes], uint32(1+len(event)))
	return append(dst, event...)
}

// AppendEventRecordTransition appends one length-prefixed event frame tagged
// with a session's space transition, without size validation. Transition 0
// writes the untagged record, so sessions that never changed space receive
// the same bytes as AppendEventRecord.
func AppendEventRecordTransition(dst, event []byte, transition uint64) []byte {
	if transition == 0 {
		return AppendEventRecord(dst, event)
	}
	start := len(dst)
	dst = append(dst, 0, 0, 0, 0, EventRecordTag, EventRecordTag)
	dst = binary.AppendUvarint(dst, transition)
	dst = append(dst, event...)
	binary.BigEndian.PutUint32(dst[start:start+FrameHeaderBytes], uint32(len(dst)-start-FrameHeaderBytes))
	return dst
}

// EventRecord borrows the ServerEvent bytes of a tagged or untagged datagram
// event frame; see EventRecordTransition.
func EventRecord(frame []byte) ([]byte, bool) {
	_, event, ok := EventRecordTransition(frame)
	return event, ok
}

// EventRecordTransition borrows the ServerEvent bytes of a datagram event
// frame and returns its space transition (0 for an untagged record). ok is
// false for frames that do not start with EventRecordTag and for tagged
// records whose transition varint is truncated or overlong; callers that see
// the tag byte but ok false must drop the frame rather than read it as state.
func EventRecordTransition(frame []byte) (transition uint64, event []byte, ok bool) {
	if len(frame) == 0 || frame[0] != EventRecordTag {
		return 0, nil, false
	}
	if len(frame) == 1 || frame[1] != EventRecordTag {
		return 0, frame[1:], true
	}
	transition, n := binary.Uvarint(frame[2:])
	if n <= 0 {
		return 0, nil, false
	}
	return transition, frame[2+n:], true
}

func uvarintBytes(v uint64) int {
	n := 1
	for v >= 0x80 {
		v >>= 7
		n++
	}
	return n
}

// ReadFrame reads one complete frame, enforcing the logical size cap before allocation.
func ReadFrame(r io.Reader) ([]byte, error) {
	var header [FrameHeaderBytes]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(header[:])
	if n > MaxReliableMessageBytes {
		return nil, SizeError{uint64(n), MaxReliableMessageBytes}
	}
	payload := make([]byte, int(n))
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, err
	}
	return payload, nil
}

// WriteFrame validates and emits one frame in a single Write call. It preserves
// the runtimes' convention of returning the writer error without retrying short writes.
func WriteFrame(w io.Writer, payload []byte) error {
	if len(payload) > MaxReliableMessageBytes {
		return SizeError{uint64(len(payload)), MaxReliableMessageBytes}
	}
	_, err := w.Write(AppendFrame(nil, payload))
	return err
}

// AppendCloseControl appends the reserved close request to caller-owned storage.
func AppendCloseControl(dst []byte) []byte { return append(dst, CloseControlPrefix...) }

// IsCloseControl recognizes only an exact close request.
func IsCloseControl(data []byte) bool { return string(data) == CloseControlPrefix }

// AppendACKControl appends an ACK snapshot and an optional application payload.
func AppendACKControl(dst []byte, ack ACK, payload []byte) []byte {
	start := len(dst)
	end := start + ACKControlHeaderBytes + len(payload)
	if cap(dst) < end {
		grown := make([]byte, end)
		copy(grown, dst)
		dst = grown
	} else {
		dst = dst[:end]
	}
	copy(dst[start:], ACKControlPrefix)
	putACK(dst[start+len(ACKControlPrefix):], ack)
	copy(dst[start+ACKControlHeaderBytes:], payload)
	return dst
}

// DecodeACKControl recognizes the ACK prefix and borrows the trailing payload.
// Unmatched data passes through unchanged; a recognized short header is an error.
func DecodeACKControl(data []byte) ([]byte, ACK, bool, error) {
	if !bytes.HasPrefix(data, []byte(ACKControlPrefix)) {
		return data, ACK{}, false, nil
	}
	if len(data) < ACKControlHeaderBytes {
		return nil, ACK{}, true, io.ErrUnexpectedEOF
	}
	return data[ACKControlHeaderBytes:], readACK(data[len(ACKControlPrefix):]), true, nil
}

// AppendSpaceAckControl appends a SpaceAck control frame for transition.
func AppendSpaceAckControl(dst []byte, transition uint64) []byte {
	dst = append(dst, SpaceAckControlPrefix...)
	return binary.BigEndian.AppendUint64(dst, transition)
}

// DecodeSpaceAckControl recognizes the SpaceAck prefix. Unmatched data reports
// false; a recognized frame of the wrong length is an error.
func DecodeSpaceAckControl(data []byte) (uint64, bool, error) {
	if !bytes.HasPrefix(data, []byte(SpaceAckControlPrefix)) {
		return 0, false, nil
	}
	if len(data) != SpaceAckControlBytes {
		return 0, true, SizeError{uint64(len(data)), uint64(SpaceAckControlBytes)}
	}
	return binary.BigEndian.Uint64(data[len(SpaceAckControlPrefix):]), true, nil
}

// AppendEntityResyncControl appends an entity resync request for entityID.
func AppendEntityResyncControl(dst []byte, entityID int64) []byte {
	dst = append(dst, EntityResyncControlPrefix...)
	return binary.BigEndian.AppendUint64(dst, uint64(entityID))
}

// DecodeEntityResyncControl recognizes the entity resync prefix. Unmatched
// data reports false; a recognized frame of the wrong length is an error.
func DecodeEntityResyncControl(data []byte) (int64, bool, error) {
	if !bytes.HasPrefix(data, []byte(EntityResyncControlPrefix)) {
		return 0, false, nil
	}
	if len(data) != EntityResyncControlBytes {
		return 0, true, SizeError{uint64(len(data)), uint64(EntityResyncControlBytes)}
	}
	return int64(binary.BigEndian.Uint64(data[len(EntityResyncControlPrefix):])), true, nil
}
