package snapshot

import (
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/demiurgos-hub/golem-engine/golem/pb"
	"github.com/demiurgos-hub/golem-engine/golem/registry"
)

func TestDecodeRejectsMalformedSnapshots(t *testing.T) {
	w := (&pb.Writer{}).Raw(magic[:]).Uint32(1).Int64(0).String("schema").Uint32(1).Int64(1).String("Monster").Bytes([]byte{37})
	valid := w.Finish()
	if _, err := Decode(valid, "schema"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < len(valid); i++ {
		if _, err := Decode(valid[:i], "schema"); err == nil {
			t.Fatalf("accepted truncated snapshot at %d", i)
		}
	}
	if _, err := Decode(append(append([]byte(nil), valid...), 0), "schema"); err == nil {
		t.Fatal("trailing bytes accepted")
	}
	huge := (&pb.Writer{}).Raw(magic[:]).Uint32(1).Int64(0).String("schema").Uint32(^uint32(0)).Finish()
	if _, err := Decode(huge, "schema"); err == nil {
		t.Fatal("unbounded record count accepted")
	}
	bad := append(append([]byte(nil), magic[:]...), []byte{0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 1}...)
	if _, err := Decode(bad, "schema"); err == nil {
		t.Fatal("overflow varint accepted")
	}
}

// v1Golden is the version-1 encoding of goldenRecords with fingerprint "fp" and
// timestamp 1700000000000000000. It must never change.
const v1Golden = "474c4d01018080a8b1e39fe7cb170266700201074d6f6e73746572040a020801ac02044865726f00"

func goldenRecords() []Record {
	return []Record{
		{EntityID: 1, TypeName: "Monster", StateData: []byte{0x0a, 0x02, 0x08, 0x01}},
		{EntityID: 300, TypeName: "Hero", StateData: []byte{}},
	}
}

func TestEncodeVersion1GoldenBytesUnchanged(t *testing.T) {
	got := hex.EncodeToString(encode(goldenRecords(), "fp", 1700000000000000000))
	if got != v1Golden {
		t.Fatalf("v1 bytes changed:\n got %s\nwant %s", got, v1Golden)
	}
	data, _ := hex.DecodeString(v1Golden)
	records, err := Decode(data, "fp")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(records, goldenRecords()) {
		t.Fatalf("v1 decode = %+v", records)
	}
}

func TestCaptureWritesVersion1WithoutSpaces(t *testing.T) {
	entities := []registry.SpacedEntity{{Entity: &snapshotTestEntity{id: 1}}, {Entity: &snapshotTestEntity{id: 2}}}
	spaced, err := CaptureSpaced(entities, "fp")
	if err != nil {
		t.Fatal(err)
	}
	plain, err := Capture([]registry.Entity{entities[0].Entity, entities[1].Entity}, "fp")
	if err != nil {
		t.Fatal(err)
	}
	if spaced[4] != 1 || plain[4] != 1 {
		t.Fatalf("space-0 capture versions = %d, %d; want 1", spaced[4], plain[4])
	}
}

func TestSpacedRoundTrip(t *testing.T) {
	entities := []registry.SpacedEntity{
		{Entity: &snapshotTestEntity{id: 1}},
		{Entity: &snapshotTestEntity{id: 2}, Space: 7},
		{Entity: &snapshotTestEntity{id: 3, transient: true}, Space: 9},
		{Entity: &snapshotTestEntity{id: 4}, Space: ^registry.SpaceID(0)},
	}
	data, err := CaptureSpaced(entities, "fp")
	if err != nil {
		t.Fatal(err)
	}
	if data[4] != 2 {
		t.Fatalf("spaced capture version = %d, want 2", data[4])
	}
	records, err := Decode(data, "fp")
	if err != nil {
		t.Fatal(err)
	}
	want := []Record{
		{EntityID: 1, TypeName: "Thing", StateData: []byte{1}},
		{EntityID: 2, TypeName: "Thing", StateData: []byte{2}, Space: 7},
		{EntityID: 4, TypeName: "Thing", StateData: []byte{4}, Space: ^registry.SpaceID(0)},
	}
	if !reflect.DeepEqual(records, want) {
		t.Fatalf("v2 decode = %+v, want %+v", records, want)
	}
	for i := 0; i < len(data); i++ {
		if _, err := Decode(data[:i], "fp"); err == nil {
			t.Fatalf("accepted truncated v2 snapshot at %d", i)
		}
	}
	tooLarge := (&pb.Writer{}).Raw(magic[:]).Uint32(2).Int64(0).String("fp").Uint32(1).Int64(1).Uint64(1 << 32).String("Thing").Bytes(nil).Finish()
	if _, err := Decode(tooLarge, "fp"); err == nil {
		t.Fatal("space beyond uint32 accepted")
	}
	future := (&pb.Writer{}).Raw(magic[:]).Uint32(3).Int64(0).String("fp").Uint32(0).Finish()
	if _, err := Decode(future, "fp"); err == nil {
		t.Fatal("unknown version accepted")
	}
}

type snapshotTestEntity struct {
	id        int64
	transient bool
}

func (e *snapshotTestEntity) EntityID() int64              { return e.id }
func (e *snapshotTestEntity) TypeName() string             { return "Thing" }
func (e *snapshotTestEntity) Position() (float32, float32) { return 0, 0 }
func (e *snapshotTestEntity) IsGlobal() bool               { return false }
func (e *snapshotTestEntity) FlushUpdate() ([]byte, error) { return nil, nil }
func (e *snapshotTestEntity) FullUpdate() ([]byte, error)  { return []byte{byte(e.id)}, nil }
func (e *snapshotTestEntity) IsPersistent() bool           { return !e.transient }

func TestReadFingerprint(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "world.snapshot")
	data := (&pb.Writer{}).Raw(magic[:]).Uint32(2).Int64(0).String("stored-schema").Uint32(0).Finish()
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := ReadFingerprint(path); err != nil || got != "stored-schema" {
		t.Fatalf("ReadFingerprint = %q, %v", got, err)
	}
	if _, err := Load(path, "other"); !errors.Is(err, ErrFingerprintMismatch) {
		t.Fatalf("Load = %v, want ErrFingerprintMismatch", err)
	}
	if err := os.WriteFile(path, []byte("nope"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadFingerprint(path); err == nil {
		t.Fatal("accepted a non-snapshot file")
	}
	if _, err := ReadFingerprint(filepath.Join(dir, "missing")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing file error = %v", err)
	}
}
