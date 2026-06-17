package pb

import (
	"encoding/binary"
	"fmt"
	"math"
)

// Reader is a minimal protobuf wire-format decoder.
type Reader struct {
	buf []byte
	pos int
}

// NewReader creates a Reader over the given byte slice.
func NewReader(buf []byte) *Reader {
	return &Reader{buf: buf}
}

// Done returns true when all bytes have been consumed.
func (r *Reader) Done() bool {
	return r.pos >= len(r.buf)
}

// Tag reads a field tag and returns the field number and wire type.
func (r *Reader) Tag() (field, wire int) {
	v := r.uvarint()
	return int(v >> 3), int(v & 7)
}

// Int32 reads a varint and returns it as int32.
func (r *Reader) Int32() int32 {
	return int32(r.uvarint())
}

// Int64 reads a varint and returns it as int64.
func (r *Reader) Int64() int64 {
	return int64(r.uvarint())
}

// Uint32 reads a varint and returns it as uint32.
func (r *Reader) Uint32() uint32 {
	return uint32(r.uvarint())
}

// Uint64 reads a varint and returns it as uint64.
func (r *Reader) Uint64() uint64 {
	return r.uvarint()
}

// Sint32 reads a zigzag-encoded varint and returns it as int32.
func (r *Reader) Sint32() int32 {
	v := uint32(r.uvarint())
	return int32((v >> 1) ^ -(v & 1))
}

// Sint64 reads a zigzag-encoded varint and returns it as int64.
func (r *Reader) Sint64() int64 {
	v := r.uvarint()
	return int64((v >> 1) ^ -(v & 1))
}

// Bool reads a varint and returns it as a boolean.
func (r *Reader) Bool() bool {
	return r.uvarint() != 0
}

// Float32 reads a 32-bit little-endian float.
func (r *Reader) Float32() float32 {
	bits := binary.LittleEndian.Uint32(r.buf[r.pos:])
	r.pos += 4
	return math.Float32frombits(bits)
}

// Float64 reads a 64-bit little-endian float.
func (r *Reader) Float64() float64 {
	bits := binary.LittleEndian.Uint64(r.buf[r.pos:])
	r.pos += 8
	return math.Float64frombits(bits)
}

// String reads a length-delimited field and returns it as a string.
func (r *Reader) String() string {
	n := int(r.uvarint())
	s := string(r.buf[r.pos : r.pos+n])
	r.pos += n
	return s
}

// Bytes reads a length-delimited field and returns the raw bytes.
func (r *Reader) Bytes() []byte {
	n := int(r.uvarint())
	b := make([]byte, n)
	copy(b, r.buf[r.pos:r.pos+n])
	r.pos += n
	return b
}

// Remaining returns a copy of the unread bytes.
func (r *Reader) Remaining() []byte {
	b := make([]byte, len(r.buf)-r.pos)
	copy(b, r.buf[r.pos:])
	r.pos = len(r.buf)
	return b
}

// Skip advances past a field value based on its wire type.
func (r *Reader) Skip(wireType int) {
	switch wireType {
	case 0: // varint
		r.uvarint()
	case 1: // 64-bit fixed
		r.pos += 8
	case 2: // length-delimited
		n := int(r.uvarint())
		r.pos += n
	case 5: // 32-bit fixed
		r.pos += 4
	default:
		panic(fmt.Sprintf("pb: unsupported wire type %d", wireType))
	}
}

func (r *Reader) uvarint() uint64 {
	var v uint64
	var shift uint
	for r.pos < len(r.buf) {
		b := r.buf[r.pos]
		r.pos++
		v |= uint64(b&0x7f) << shift
		if b < 0x80 {
			return v
		}
		shift += 7
	}
	return v
}
