package pb

import (
	"encoding/binary"
	"math"
)

// Writer is a minimal protobuf wire-format encoder supporting the scalar
// types used by golem schemas. Methods are chainable.
type Writer struct {
	buf []byte
}

// Tag writes a field tag (field number + wire type).
func (w *Writer) Tag(field, wireType int) *Writer {
	return w.uvarint(uint64(field<<3 | wireType))
}

// Int32 encodes a signed 32-bit integer as a varint.
func (w *Writer) Int32(v int32) *Writer {
	return w.uvarint(uint64(v))
}

// Int64 encodes a signed 64-bit integer as a varint.
func (w *Writer) Int64(v int64) *Writer {
	return w.uvarint(uint64(v))
}

// Uint32 encodes an unsigned 32-bit integer as a varint.
func (w *Writer) Uint32(v uint32) *Writer {
	return w.uvarint(uint64(v))
}

// Uint64 encodes an unsigned 64-bit integer as a varint.
func (w *Writer) Uint64(v uint64) *Writer {
	return w.uvarint(v)
}

// Sint32 encodes a signed 32-bit integer using zigzag encoding.
func (w *Writer) Sint32(v int32) *Writer {
	return w.uvarint(uint64((uint32(v) << 1) ^ uint32(v>>31)))
}

// Sint64 encodes a signed 64-bit integer using zigzag encoding.
func (w *Writer) Sint64(v int64) *Writer {
	return w.uvarint(uint64(v<<1) ^ uint64(v>>63))
}

// Bool encodes a boolean as a single-byte varint (0 or 1).
func (w *Writer) Bool(v bool) *Writer {
	if v {
		w.buf = append(w.buf, 1)
	} else {
		w.buf = append(w.buf, 0)
	}
	return w
}

// Float32 encodes a 32-bit float in little-endian fixed format (wire type 5).
func (w *Writer) Float32(v float32) *Writer {
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], math.Float32bits(v))
	w.buf = append(w.buf, b[:]...)
	return w
}

// Float64 encodes a 64-bit float in little-endian fixed format (wire type 1).
func (w *Writer) Float64(v float64) *Writer {
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], math.Float64bits(v))
	w.buf = append(w.buf, b[:]...)
	return w
}

// String encodes a UTF-8 string as a length-delimited field.
func (w *Writer) String(v string) *Writer {
	w.uvarint(uint64(len(v)))
	w.buf = append(w.buf, v...)
	return w
}

// Bytes encodes a byte slice as a length-delimited field.
func (w *Writer) Bytes(v []byte) *Writer {
	w.uvarint(uint64(len(v)))
	w.buf = append(w.buf, v...)
	return w
}

// Raw appends pre-encoded bytes directly without any framing.
func (w *Writer) Raw(v []byte) *Writer {
	w.buf = append(w.buf, v...)
	return w
}

// Finish returns the encoded bytes.
func (w *Writer) Finish() []byte {
	return w.buf
}

func (w *Writer) uvarint(v uint64) *Writer {
	for v > 0x7f {
		w.buf = append(w.buf, byte(v&0x7f)|0x80)
		v >>= 7
	}
	w.buf = append(w.buf, byte(v))
	return w
}
