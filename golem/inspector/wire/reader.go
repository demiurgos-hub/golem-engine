package wire

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// ErrMalformed reports bytes that are not a valid protobuf encoding of the
// expected message. Decoders never panic on untrusted input.
var ErrMalformed = errors.New("wire: malformed message")

// Protobuf wire types.
const (
	wireVarint  = 0
	wireFixed64 = 1
	wireBytes   = 2
	wireFixed32 = 5
)

// reader is a bounds-checked protobuf decoder. Unlike golem/pb.Reader it
// records the first error instead of panicking, because the hub decodes
// frames from the network. Byte slices it returns alias the input buffer.
type reader struct {
	buf []byte
	pos int
	err error
}

func newReader(buf []byte) *reader { return &reader{buf: buf} }

func (r *reader) done() bool { return r.err != nil || r.pos >= len(r.buf) }

func (r *reader) fail(format string, args ...any) {
	if r.err == nil {
		r.err = fmt.Errorf("%w: %s", ErrMalformed, fmt.Sprintf(format, args...))
	}
	r.pos = len(r.buf)
}

func (r *reader) varint() uint64 {
	var v uint64
	for shift := uint(0); shift < 64; shift += 7 {
		if r.pos >= len(r.buf) {
			r.fail("truncated varint")
			return 0
		}
		b := r.buf[r.pos]
		r.pos++
		v |= uint64(b&0x7f) << shift
		if b < 0x80 {
			return v
		}
	}
	r.fail("varint overflow")
	return 0
}

// tag reads a field key. Field number 0 is invalid in protobuf.
func (r *reader) tag() (uint32, int) {
	key := r.varint()
	field := key >> 3
	if r.err == nil && (field == 0 || field > 1<<29-1) {
		r.fail("invalid field number %d", field)
	}
	return uint32(field), int(key & 7)
}

func (r *reader) bytes() []byte {
	n := r.varint()
	if r.err != nil {
		return nil
	}
	if n > uint64(len(r.buf)-r.pos) {
		r.fail("length %d exceeds remaining %d bytes", n, len(r.buf)-r.pos)
		return nil
	}
	b := r.buf[r.pos : r.pos+int(n)]
	r.pos += int(n)
	return b
}

func (r *reader) string() string { return string(r.bytes()) }

func (r *reader) fixed32() uint32 {
	if len(r.buf)-r.pos < 4 {
		r.fail("truncated fixed32")
		return 0
	}
	v := binary.LittleEndian.Uint32(r.buf[r.pos:])
	r.pos += 4
	return v
}

func (r *reader) fixed64() uint64 {
	if len(r.buf)-r.pos < 8 {
		r.fail("truncated fixed64")
		return 0
	}
	v := binary.LittleEndian.Uint64(r.buf[r.pos:])
	r.pos += 8
	return v
}

func (r *reader) skip(wt int) {
	switch wt {
	case wireVarint:
		r.varint()
	case wireFixed64:
		r.fixed64()
	case wireBytes:
		r.bytes()
	case wireFixed32:
		r.fixed32()
	default:
		r.fail("unsupported wire type %d", wt)
	}
}

// expect validates the wire type of a known field; mismatches are malformed.
func (r *reader) expect(field uint32, got, want int) bool {
	if got != want {
		r.fail("field %d has wire type %d, want %d", field, got, want)
		return false
	}
	return true
}

func (r *reader) uint64Field(field uint32, wt int) uint64 {
	if !r.expect(field, wt, wireVarint) {
		return 0
	}
	return r.varint()
}

func (r *reader) uint32Field(field uint32, wt int) uint32 {
	v := r.uint64Field(field, wt)
	if v > 1<<32-1 {
		r.fail("field %d overflows uint32", field)
	}
	return uint32(v)
}

func (r *reader) int64Field(field uint32, wt int) int64 { return int64(r.uint64Field(field, wt)) }

func (r *reader) boolField(field uint32, wt int) bool { return r.uint64Field(field, wt) != 0 }

func (r *reader) sint32Field(field uint32, wt int) int32 {
	v := uint32(r.uint64Field(field, wt))
	return int32(v>>1) ^ -int32(v&1)
}

func (r *reader) bytesField(field uint32, wt int) []byte {
	if !r.expect(field, wt, wireBytes) {
		return nil
	}
	return r.bytes()
}

func (r *reader) stringField(field uint32, wt int) string { return string(r.bytesField(field, wt)) }
