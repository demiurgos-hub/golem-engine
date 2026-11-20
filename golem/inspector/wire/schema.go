package wire

import (
	"fmt"
	"sort"

	"github.com/demiurgos-hub/golem-engine/golem/pb"
)

// FieldKind classifies how a schema field is encoded on the wire.
type FieldKind uint32

// Field kinds, matching inspector.proto FieldKind.
const (
	KindScalar FieldKind = 0
	KindCustom FieldKind = 1
	KindList   FieldKind = 2
	KindMap    FieldKind = 3
)

// Field roles used by Field.Role.
const (
	RoleIdentity = "identity"
	RolePosition = "position"
	RoleRevision = "revision"
	RoleVariable = "variable"
)

// Implicit entity field numbers shared by every generated entity message.
const (
	TagEntityID = 1
	TagPosX     = 2
	TagPosY     = 3
	TagPosZ     = 4
	TagRevision = 1000
)

// Schema describes a game's entity wire layout so the hub and UI can decode
// generated XState/XDelta messages without game-specific code. golem-bake
// emits one as InspectorSchema() in the go-server integration.
type Schema struct {
	Fingerprint string       `json:"fingerprint"`
	Dimensions  uint32       `json:"dimensions"`
	RemovedTag  uint32       `json:"removedTag"`
	Entities    []Entity     `json:"entities"`
	Types       []CustomType `json:"types"`
}

// Entity describes one entity type: its EntityUpdate oneof tags and fields.
type Entity struct {
	Name     string  `json:"name"`
	StateTag uint32  `json:"stateTag"`
	DeltaTag uint32  `json:"deltaTag"`
	Global   bool    `json:"global"`
	Fields   []Field `json:"fields"`
	Is3D     bool    `json:"is3d"`
}

// Field describes one field of an entity or custom type.
type Field struct {
	Name      string    `json:"name"`
	Tag       uint32    `json:"tag"`
	Bit       int32     `json:"bit"`
	Sync      string    `json:"sync"`
	OwnerOnly bool      `json:"ownerOnly"`
	Kind      FieldKind `json:"kind"`
	Type      string    `json:"type"`
	KeyType   string    `json:"keyType"`
	Role      string    `json:"role"`
}

// CustomType describes a nested message type declared in the types schema.
type CustomType struct {
	Name   string  `json:"name"`
	Fields []Field `json:"fields"`
}

// EntityByTag returns the entity whose State or Delta oneof tag equals tag.
func (s *Schema) EntityByTag(tag uint32) *Entity {
	if s == nil {
		return nil
	}
	for i := range s.Entities {
		if s.Entities[i].StateTag == tag || s.Entities[i].DeltaTag == tag {
			return &s.Entities[i]
		}
	}
	return nil
}

// Validate checks structural invariants a decoder relies on: unique entity
// oneof tags, unique field tags and bits per entity, and in-range bits.
func (s *Schema) Validate() error {
	if s == nil {
		return fmt.Errorf("wire: nil schema")
	}
	if s.Dimensions != 2 && s.Dimensions != 3 {
		return fmt.Errorf("wire: schema dimensions %d, want 2 or 3", s.Dimensions)
	}
	tags := map[uint32]string{}
	if s.RemovedTag != 0 {
		tags[s.RemovedTag] = "EntityRemoved"
	}
	for _, e := range s.Entities {
		if e.Name == "" || e.StateTag == 0 || e.DeltaTag == 0 {
			return fmt.Errorf("wire: entity %q needs a name and nonzero state/delta tags", e.Name)
		}
		for _, t := range []uint32{e.StateTag, e.DeltaTag} {
			if prev, ok := tags[t]; ok {
				return fmt.Errorf("wire: entity %q tag %d already used by %q", e.Name, t, prev)
			}
			tags[t] = e.Name
		}
		if err := validateFields(e.Name, e.Fields, true); err != nil {
			return err
		}
	}
	for _, t := range s.Types {
		if err := validateFields(t.Name, t.Fields, false); err != nil {
			return err
		}
	}
	return nil
}

func validateFields(owner string, fields []Field, bits bool) error {
	tags := map[uint32]bool{}
	used := map[int32]bool{}
	for _, f := range fields {
		if f.Tag == 0 || tags[f.Tag] {
			return fmt.Errorf("wire: %s field %q has invalid or duplicate tag %d", owner, f.Name, f.Tag)
		}
		tags[f.Tag] = true
		if f.Kind > KindMap {
			return fmt.Errorf("wire: %s field %q has unknown kind %d", owner, f.Name, f.Kind)
		}
		if f.Bit < -1 || f.Bit > 63 || (!bits && f.Bit != -1) {
			return fmt.Errorf("wire: %s field %q has invalid bit %d", owner, f.Name, f.Bit)
		}
		if f.Bit >= 0 {
			if used[f.Bit] {
				return fmt.Errorf("wire: %s field %q reuses bit %d", owner, f.Name, f.Bit)
			}
			used[f.Bit] = true
		}
	}
	return nil
}

// TagsForMask returns the field tags whose dirty bits are set in mask, sorted.
func (e *Entity) TagsForMask(mask uint64) []uint32 {
	var out []uint32
	for _, f := range e.Fields {
		if f.Bit >= 0 && mask&(1<<uint(f.Bit)) != 0 {
			out = append(out, f.Tag)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Encode returns the protobuf encoding of s.
func (s *Schema) Encode() []byte {
	w := &pb.Writer{}
	s.encodeTo(w)
	return w.Finish()
}

func (s *Schema) encodeTo(w *pb.Writer) {
	putString(w, 1, s.Fingerprint)
	putUint(w, 2, uint64(s.Dimensions))
	putUint(w, 3, uint64(s.RemovedTag))
	for i := range s.Entities {
		e := &s.Entities[i]
		ew := &pb.Writer{}
		putString(ew, 1, e.Name)
		putUint(ew, 2, uint64(e.StateTag))
		putUint(ew, 3, uint64(e.DeltaTag))
		putBool(ew, 4, e.Global)
		for j := range e.Fields {
			putMessage(ew, 5, encodeField(&e.Fields[j]))
		}
		putBool(ew, 6, e.Is3D)
		putMessage(w, 4, ew.Finish())
	}
	for i := range s.Types {
		t := &s.Types[i]
		tw := &pb.Writer{}
		putString(tw, 1, t.Name)
		for j := range t.Fields {
			putMessage(tw, 2, encodeField(&t.Fields[j]))
		}
		putMessage(w, 5, tw.Finish())
	}
}

func encodeField(f *Field) []byte {
	w := &pb.Writer{}
	putString(w, 1, f.Name)
	putUint(w, 2, uint64(f.Tag))
	if f.Bit != 0 {
		w.Tag(3, wireVarint).Sint32(f.Bit)
	}
	putString(w, 4, f.Sync)
	putBool(w, 5, f.OwnerOnly)
	putUint(w, 6, uint64(f.Kind))
	putString(w, 7, f.Type)
	putString(w, 8, f.KeyType)
	putString(w, 9, f.Role)
	return w.Finish()
}

// DecodeSchema parses a Schema encoded by Encode.
func DecodeSchema(buf []byte) (*Schema, error) {
	r := newReader(buf)
	s := decodeSchema(r)
	if r.err != nil {
		return nil, r.err
	}
	return s, nil
}

func decodeSchema(r *reader) *Schema {
	s := &Schema{}
	for !r.done() {
		f, wt := r.tag()
		switch f {
		case 1:
			s.Fingerprint = r.stringField(f, wt)
		case 2:
			s.Dimensions = r.uint32Field(f, wt)
		case 3:
			s.RemovedTag = r.uint32Field(f, wt)
		case 4:
			sub := newReader(r.bytesField(f, wt))
			e := Entity{}
			for !sub.done() {
				ef, ewt := sub.tag()
				switch ef {
				case 1:
					e.Name = sub.stringField(ef, ewt)
				case 2:
					e.StateTag = sub.uint32Field(ef, ewt)
				case 3:
					e.DeltaTag = sub.uint32Field(ef, ewt)
				case 4:
					e.Global = sub.boolField(ef, ewt)
				case 5:
					e.Fields = append(e.Fields, decodeField(sub, sub.bytesField(ef, ewt)))
				case 6:
					e.Is3D = sub.boolField(ef, ewt)
				default:
					sub.skip(ewt)
				}
			}
			r.adopt(sub)
			s.Entities = append(s.Entities, e)
		case 5:
			sub := newReader(r.bytesField(f, wt))
			t := CustomType{}
			for !sub.done() {
				tf, twt := sub.tag()
				switch tf {
				case 1:
					t.Name = sub.stringField(tf, twt)
				case 2:
					t.Fields = append(t.Fields, decodeField(sub, sub.bytesField(tf, twt)))
				default:
					sub.skip(twt)
				}
			}
			r.adopt(sub)
			s.Types = append(s.Types, t)
		default:
			r.skip(wt)
		}
	}
	return s
}

func decodeField(parent *reader, buf []byte) Field {
	r := newReader(buf)
	f := Field{}
	for !r.done() {
		n, wt := r.tag()
		switch n {
		case 1:
			f.Name = r.stringField(n, wt)
		case 2:
			f.Tag = r.uint32Field(n, wt)
		case 3:
			f.Bit = r.sint32Field(n, wt)
		case 4:
			f.Sync = r.stringField(n, wt)
		case 5:
			f.OwnerOnly = r.boolField(n, wt)
		case 6:
			f.Kind = FieldKind(r.uint32Field(n, wt))
		case 7:
			f.Type = r.stringField(n, wt)
		case 8:
			f.KeyType = r.stringField(n, wt)
		case 9:
			f.Role = r.stringField(n, wt)
		default:
			r.skip(wt)
		}
	}
	parent.adopt(r)
	return f
}

// adopt propagates a nested reader's error to its parent.
func (r *reader) adopt(sub *reader) {
	if sub.err != nil && r.err == nil {
		r.err = sub.err
		r.pos = len(r.buf)
	}
}

func putUint(w *pb.Writer, field int, v uint64) {
	if v != 0 {
		w.Tag(field, wireVarint).Uint64(v)
	}
}

func putInt(w *pb.Writer, field int, v int64) {
	if v != 0 {
		w.Tag(field, wireVarint).Int64(v)
	}
}

func putBool(w *pb.Writer, field int, v bool) {
	if v {
		w.Tag(field, wireVarint).Bool(true)
	}
}

func putString(w *pb.Writer, field int, v string) {
	if v != "" {
		w.Tag(field, wireBytes).String(v)
	}
}

func putBytes(w *pb.Writer, field int, v []byte) {
	if len(v) > 0 {
		w.Tag(field, wireBytes).Bytes(v)
	}
}

// putMessage always writes the field, so empty sub-messages stay present.
func putMessage(w *pb.Writer, field int, v []byte) {
	w.Tag(field, wireBytes).Bytes(v)
}
