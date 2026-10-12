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
