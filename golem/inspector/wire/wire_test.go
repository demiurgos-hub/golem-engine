package wire

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/demiurgos-hub/golem-engine/golem/pb"
)

var update = flag.Bool("update", false, "rewrite golden fixtures under testdata/")

// fixtureSchema mirrors what golem-bake emits for a 3D game with an Actor
// entity covering every field kind, plus a 2D-style Marker entity.
func fixtureSchema() *Schema {
	return &Schema{
		Fingerprint: "abc123",
		Dimensions:  3,
		RemovedTag:  5,
		Entities: []Entity{
			{
				Name: "Actor", StateTag: 1, DeltaTag: 2, Is3D: true,
				Fields: []Field{
					{Name: "entity_id", Tag: 1, Bit: -1, Type: "int64", Role: RoleIdentity},
					{Name: "pos_x", Tag: 2, Bit: 0, Type: "float", Role: RolePosition},
					{Name: "pos_y", Tag: 3, Bit: 1, Type: "float", Role: RolePosition},
					{Name: "pos_z", Tag: 4, Bit: 2, Type: "float", Role: RolePosition},
					{Name: "revision", Tag: 1000, Bit: -1, Type: "uint64", Role: RoleRevision},
					{Name: "name", Tag: 5, Bit: -1, Sync: "once", Type: "string", Role: RoleVariable},
					{Name: "health", Tag: 6, Bit: 3, Sync: "tick", Type: "int32", Role: RoleVariable},
					{Name: "gold", Tag: 7, Bit: 4, Sync: "tick", OwnerOnly: true, Type: "uint32", Role: RoleVariable},
					{Name: "tags", Tag: 8, Bit: 5, Sync: "tick", Kind: KindList, Type: "string", Role: RoleVariable},
					{Name: "stats", Tag: 9, Bit: 6, Sync: "tick", Kind: KindMap, Type: "int32", KeyType: "string", Role: RoleVariable},
					{Name: "held", Tag: 10, Bit: 7, Sync: "tick", Kind: KindCustom, Type: "Item", Role: RoleVariable},
				},
			},
			{
				Name: "Marker", StateTag: 3, DeltaTag: 4, Global: true,
				Fields: []Field{
					{Name: "entity_id", Tag: 1, Bit: -1, Type: "int64", Role: RoleIdentity},
					{Name: "pos_x", Tag: 2, Bit: 0, Type: "float", Role: RolePosition},
					{Name: "pos_y", Tag: 3, Bit: 1, Type: "float", Role: RolePosition},
					{Name: "revision", Tag: 1000, Bit: -1, Type: "uint64", Role: RoleRevision},
				},
			},
		},
		Types: []CustomType{{Name: "Item", Fields: []Field{
			{Name: "name", Tag: 1, Bit: -1, Type: "string"},
			{Name: "qty", Tag: 2, Bit: -1, Type: "int32"},
		}}},
	}
}

func item(name string, qty int32) []byte {
	w := &pb.Writer{}
	w.Tag(1, 2).String(name)
	w.Tag(2, 0).Int32(qty)
	return w.Finish()
}

func listString(w *pb.Writer, tag int, v string) {
	inner := &pb.Writer{}
	inner.String(v)
	w.Tag(tag, 2).Bytes(inner.Finish())
}

func mapEntry(w *pb.Writer, tag int, k string, v int32) {
	kv := &pb.Writer{}
	kv.Tag(1, 2).String(k)
	kv.Tag(2, 0).Int32(v)
	w.Tag(tag, 2).Bytes(kv.Finish())
}

// actorState encodes an ActorState the way generated code does.
func actorState() []byte {
	w := &pb.Writer{}
	w.Tag(1, 0).Int64(42)
	w.Tag(2, 5).Float32(1.5)
	w.Tag(3, 5).Float32(-2)
	w.Tag(4, 5).Float32(10.25)
	w.Tag(5, 2).String("Ada")
	w.Tag(6, 0).Int32(-7)
	w.Tag(7, 0).Uint32(99)
	listString(w, 8, "red")
	listString(w, 8, "")
	mapEntry(w, 9, "str", 12)
	w.Tag(10, 2).Bytes(item("sword", 1))
	w.Tag(1000, 0).Uint64(3)
	return w.Finish()
}

// actorDelta moves x, sets health to 0, clears tags and stats (mask covers them).
func actorDelta() ([]byte, uint64) {
	w := &pb.Writer{}
	w.Tag(1, 0).Int64(42)
	w.Tag(2, 5).Float32(4)
	w.Tag(6, 0).Int32(0)
	w.Tag(8, 2).Bytes(nil)
	w.Tag(1000, 0).Uint64(4)
	mask := uint64(1<<0 | 1<<3 | 1<<5 | 1<<6)
	return w.Finish(), mask
}

func fixtureMessages() []struct {
	name string
	msg  Message
} {
	delta, mask := actorDelta()
	return []struct {
		name string
		msg  Message
	}{
		{"register", &Register{TargetID: "cave-7", Labels: map[string]string{"map": "caves", "region": "eu"}, Epoch: 2, EngineVersion: "v0.4.0", Schema: fixtureSchema(), TickRate: 20}},
		{"observe_status", &ObserveStatus{State: ObserveReady, EntityCount: 2, Epoch: 2}},
		{"snapshot_begin", &SnapshotBegin{TargetID: "cave-7", Epoch: 2, Seq: 1, Tick: 900, EntityCount: 1}},
		{"snapshot_chunk", &SnapshotChunk{TargetID: "cave-7", Epoch: 2, Seq: 2, Entities: []EntityState{{ID: 42, TypeTag: 1, State: actorState()}}}},
		{"snapshot_end", &SnapshotEnd{TargetID: "cave-7", Epoch: 2, Seq: 3}},
		{"batch", &Batch{TargetID: "cave-7", Epoch: 2, Seq: 4, Tick: 905,
			Spawns:   []EntityState{{ID: 43, TypeTag: 3, State: []byte{8, 43}}},
			Deltas:   []EntityDelta{{ID: 42, TypeTag: 1, Revision: 4, Mask: mask, Delta: delta}},
			Removals: []Removal{{ID: 41, Revision: 9}}}},
		{"ping", &Ping{ClientTime: 1700000000000}},
		{"pong", &Pong{ClientTime: 1700000000000}},
		{"observe_start", &ObserveStart{}},
		{"observe_stop", &ObserveStop{}},
		{"error", &Error{Code: "target_not_found", Message: "no such target", TargetID: "x"}},
		{"subscribe", &Subscribe{TargetID: "cave-7"}},
		{"resync", &Resync{TargetID: "cave-7"}},
		{"target_schema", &TargetSchema{TargetID: "cave-7", Schema: fixtureSchema()}},
		{"world_status", &WorldStatus{TargetID: "cave-7", Epoch: 2, EntityCount: 1, Ready: true, AgentConnected: true, ObserveState: ObserveReady, Tick: 905}},
		{"resync_required", &ResyncRequired{TargetID: "cave-7", Epoch: 2, Reason: "seq_gap"}},
	}
}

func TestRoundTrip(t *testing.T) {
	for _, tc := range fixtureMessages() {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Decode(Encode(tc.msg))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.msg) {
				t.Fatalf("round trip mismatch\n got %#v\nwant %#v", got, tc.msg)
			}
		})
	}
}

type fixtureIndex struct {
	Messages []fixtureEntry `json:"messages"`
	Merge    mergeFixture   `json:"merge"`
}

type fixtureEntry struct {
	Name string          `json:"name"`
	File string          `json:"file"`
	Type string          `json:"type"`
	Want json.RawMessage `json:"want"`
}

type mergeFixture struct {
	TypeTag uint32 `json:"typeTag"`
	State   string `json:"state"`
	Delta   string `json:"delta"`
	Mask    string `json:"mask"`
	Merged  string `json:"merged"`
}

// TestGoldenFixtures pins the binary encoding shared with the TypeScript codec
// in golem-inspector. Run with -update to regenerate after a protocol change.
func TestGoldenFixtures(t *testing.T) {
	dir := "testdata"
	index := fixtureIndex{}
	for _, tc := range fixtureMessages() {
		encoded := Encode(tc.msg)
		file := tc.name + ".bin"
		path := filepath.Join(dir, file)
		if *update {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, encoded, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		golden, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s: %v (run go test -update)", path, err)
		}
		if !bytes.Equal(golden, encoded) {
			t.Errorf("%s: encoding changed (run go test -update if intended)", path)
		}
		want, _ := json.Marshal(tc.msg)
		index.Messages = append(index.Messages, fixtureEntry{Name: tc.name, File: file, Type: reflect.TypeOf(tc.msg).Elem().Name(), Want: want})
	}

	state := actorState()
	delta, mask := actorDelta()
	fields, err := ParseState(1, state)
	if err != nil {
		t.Fatal(err)
	}
	if err := fields.ApplyDelta(fixtureSchema().EntityByTag(1), mask, delta); err != nil {
		t.Fatal(err)
	}
	enc := base64.StdEncoding.EncodeToString
	index.Merge = mergeFixture{TypeTag: 1, State: enc(state), Delta: enc(delta), Mask: "105", Merged: enc(fields.EncodeState())}

	indexBytes, _ := json.MarshalIndent(index, "", "  ")
	indexBytes = append(indexBytes, '\n')
	indexPath := filepath.Join(dir, "fixtures.json")
	if *update {
		if err := os.WriteFile(indexPath, indexBytes, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	golden, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatalf("%s: %v (run go test -update)", indexPath, err)
	}
	if !bytes.Equal(bytes.ReplaceAll(golden, []byte("\r\n"), []byte("\n")), indexBytes) {
		t.Errorf("%s: fixture index changed (run go test -update if intended)", indexPath)
	}
}

func TestMergeSemantics(t *testing.T) {
	schema := fixtureSchema()
	actor := schema.EntityByTag(2)
	fields, err := ParseState(1, actorState())
	if err != nil {
		t.Fatal(err)
	}
	delta, mask := actorDelta()
	if err := fields.ApplyDelta(actor, mask, delta); err != nil {
		t.Fatal(err)
	}
	if fields.Revision() != 4 || fields.ID() != 42 {
		t.Fatalf("revision/id = %d/%d", fields.Revision(), fields.ID())
	}
	merged, err := splitFields(fields.EncodeState())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := merged[9]; ok {
		t.Fatal("stats should be cleared: bit set and absent from delta")
	}
	if got := merged[8]; len(got) != 1 || !bytes.Equal(got[0], []byte{8<<3 | 2, 0}) {
		t.Fatalf("tags should hold the zero-length clear marker, got %v", got)
	}
	if len(merged[7]) != 1 || len(merged[10]) != 1 || len(merged[5]) != 1 {
		t.Fatal("untouched fields (gold, held, name) must be kept")
	}
	if got := merged[6]; len(got) != 1 || !bytes.Equal(got[0], []byte{6 << 3, 0}) {
		t.Fatalf("health should be explicit zero, got %v", got)
	}
}

func TestDecodeRejectsMalformed(t *testing.T) {
	good := Encode(&SnapshotBegin{TargetID: "t", Epoch: 1, Seq: 1})
	cases := map[string][]byte{
		"empty":         {},
		"truncated":     good[:len(good)-2],
		"bad length":    {0x08, 0x01, 0x62, 0x7f},
		"field zero":    {0x00, 0x01},
		"no body":       {0x08, 0x01},
		"huge varint":   {0x08, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x01},
		"bad wire type": {0x08, 0x01, 0x63},
	}
	for name, buf := range cases {
		if _, err := Decode(buf); !errors.Is(err, ErrMalformed) {
			t.Errorf("%s: err = %v, want ErrMalformed", name, err)
		}
	}
	other := []byte{0x08, 0x02, 0x82, 0x01, 0x00}
	if _, err := Decode(other); !errors.Is(err, ErrVersion) {
		t.Errorf("version 2: err = %v, want ErrVersion", err)
	}
}

func TestSchemaValidate(t *testing.T) {
	if err := fixtureSchema().Validate(); err != nil {
		t.Fatal(err)
	}
	s := fixtureSchema()
	s.Entities[1].StateTag = 2
	if s.Validate() == nil {
		t.Fatal("duplicate oneof tag accepted")
	}
	s = fixtureSchema()
	s.Entities[0].Fields[6].Bit = 0
	if s.Validate() == nil {
		t.Fatal("duplicate bit accepted")
	}
}

func TestUnwrapUpdate(t *testing.T) {
	w := &pb.Writer{}
	w.Tag(3, 2).Bytes([]byte{8, 43})
	tag, payload, err := UnwrapUpdate(w.Finish())
	if err != nil || tag != 3 || !bytes.Equal(payload, []byte{8, 43}) {
		t.Fatalf("UnwrapUpdate = %d %v %v", tag, payload, err)
	}
	if _, _, err := UnwrapUpdate(nil); err == nil {
		t.Fatal("empty update accepted")
	}
}

func FuzzDecode(f *testing.F) {
	for _, tc := range fixtureMessages() {
		f.Add(Encode(tc.msg))
	}
	f.Fuzz(func(t *testing.T, buf []byte) {
		_, _ = Decode(buf)
		_, _ = DecodeSchema(buf)
		_, _ = ParseState(1, buf)
	})
}

func TestEncodeBatchFrameMatchesEncode(t *testing.T) {
	delta, mask := actorDelta()
	b := &Batch{TargetID: "t", Epoch: 3, Seq: 0, Tick: 9, Deltas: []EntityDelta{{ID: 42, TypeTag: 1, Revision: 4, Mask: mask, Delta: delta}}}
	frame := EncodeBatchFrame(EncodeBatchBody(b), 77)
	got, err := Decode(frame)
	if err != nil {
		t.Fatal(err)
	}
	want := *b
	want.Seq = 77
	if !reflect.DeepEqual(got, &want) {
		t.Fatalf("got %#v want %#v", got, &want)
	}
}
