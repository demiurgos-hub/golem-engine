package wire

import (
	"fmt"
	"sort"
)

// UnwrapUpdate splits a generated EntityUpdate message into its oneof tag and
// the bytes of the State, Delta or EntityRemoved message it carries.
func UnwrapUpdate(buf []byte) (uint32, []byte, error) {
	r := newReader(buf)
	var tag uint32
	var payload []byte
	found := false
	for !r.done() {
		f, wt := r.tag()
		if wt != wireBytes {
			r.skip(wt)
			continue
		}
		tag, payload, found = f, r.bytes(), true
	}
	if r.err != nil {
		return 0, nil, r.err
	}
	if !found {
		return 0, nil, fmt.Errorf("%w: EntityUpdate has no payload", ErrMalformed)
	}
	return tag, payload, nil
}

// EntityFields is a game-agnostic view of one entity's state: the raw encoded
// protobuf entries (key and value) grouped by field number. It can absorb
// generated deltas and re-encode a State message without knowing the game.
// EntityFields is not safe for concurrent use.
type EntityFields struct {
	TypeTag uint32
	entries map[uint32][][]byte
}

// ParseState copies a generated XState message into an EntityFields.
func ParseState(typeTag uint32, state []byte) (*EntityFields, error) {
	entries, err := splitFields(state)
	if err != nil {
		return nil, err
	}
	return &EntityFields{TypeTag: typeTag, entries: entries}, nil
}

// ApplyDelta merges a generated XDelta message. Every field present in delta
// replaces the stored entries for its tag, and every field whose bit is set in
// mask but is absent from delta is reset to its default.
func (f *EntityFields) ApplyDelta(entity *Entity, mask uint64, delta []byte) error {
	entries, err := splitFields(delta)
	if err != nil {
		return err
	}
	if entity != nil {
		for _, tag := range entity.TagsForMask(mask) {
			if _, ok := entries[tag]; !ok {
				delete(f.entries, tag)
			}
		}
	}
	for tag, list := range entries {
		f.entries[tag] = list
	}
	return nil
}

// EncodeState re-encodes the stored entries as a State message, in field
// number order with repeated entries kept in arrival order.
func (f *EntityFields) EncodeState() []byte {
	tags := make([]uint32, 0, len(f.entries))
	size := 0
	for tag, list := range f.entries {
		tags = append(tags, tag)
		for _, e := range list {
			size += len(e)
		}
	}
	sort.Slice(tags, func(i, j int) bool { return tags[i] < tags[j] })
	out := make([]byte, 0, size)
	for _, tag := range tags {
		for _, e := range f.entries[tag] {
			out = append(out, e...)
		}
	}
	return out
}

// Revision returns the stored revision field, or 0 when absent.
func (f *EntityFields) Revision() uint64 { return f.varintField(TagRevision) }

// ID returns the stored entity_id field, or 0 when absent.
func (f *EntityFields) ID() int64 { return int64(f.varintField(TagEntityID)) }

func (f *EntityFields) varintField(tag uint32) uint64 {
	list := f.entries[tag]
	if len(list) == 0 {
		return 0
	}
	r := newReader(list[len(list)-1])
	_, wt := r.tag()
	if wt != wireVarint {
		return 0
	}
	return r.varint()
}

// splitFields groups the raw entries of a message by field number, copying
// them so the result does not alias buf.
func splitFields(buf []byte) (map[uint32][][]byte, error) {
	r := newReader(buf)
	out := map[uint32][][]byte{}
	for !r.done() {
		start := r.pos
		f, wt := r.tag()
		r.skip(wt)
		if r.err != nil {
			break
		}
		entry := make([]byte, r.pos-start)
		copy(entry, buf[start:r.pos])
		out[f] = append(out[f], entry)
	}
	if r.err != nil {
		return nil, r.err
	}
	return out, nil
}
