package snapshot

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/demiurgos-hub/golem-engine/golem/registry"
)

// captureTestEntity serializes to its ID; it can be transient, serialize to
// nothing, fail or panic.
type captureTestEntity struct {
	id                           int64
	transient, empty, fail, boom bool
}

func (e *captureTestEntity) EntityID() int64              { return e.id }
func (e *captureTestEntity) TypeName() string             { return "Thing" }
func (e *captureTestEntity) Position() (float32, float32) { return 0, 0 }
func (e *captureTestEntity) IsGlobal() bool               { return false }
func (e *captureTestEntity) FlushUpdate() ([]byte, error) { return nil, nil }
func (e *captureTestEntity) IsPersistent() bool           { return !e.transient }
func (e *captureTestEntity) FullUpdate() ([]byte, error) {
	switch {
	case e.boom:
		panic(fmt.Sprintf("entity %d panicked", e.id))
	case e.fail:
		return nil, errors.New("broken")
	case e.empty:
		return nil, nil
	}
	return []byte(fmt.Sprintf("state-%d", e.id)), nil
}

func captureTestEntities(n int) []registry.SpacedEntity {
	out := make([]registry.SpacedEntity, n)
	for i := range out {
		out[i] = registry.SpacedEntity{Entity: &captureTestEntity{id: int64(i + 1), transient: i%7 == 3, empty: i%11 == 5}, Space: registry.SpaceID(i % 3)}
	}
	return out
}

// forceCapture makes every capture run serially or on several goroutines.
func forceCapture(t *testing.T, parallel bool) {
	t.Helper()
	prevMin, prevGrain := parallelCaptureMin, parallelCaptureGrain
	t.Cleanup(func() { parallelCaptureMin, parallelCaptureGrain = prevMin, prevGrain })
	if parallel {
		parallelCaptureMin, parallelCaptureGrain = 1, 64
	} else {
		parallelCaptureMin = int(^uint(0) >> 1)
	}
}

func spacedAt(entities []registry.SpacedEntity) func(int) (registry.Entity, registry.SpaceID) {
	return func(i int) (registry.Entity, registry.SpaceID) { return entities[i].Entity, entities[i].Space }
}

// A capture spread over several goroutines yields the same records, in the
// same order, and the same file bytes as a serial one.
func TestCaptureParallelMatchesSerial(t *testing.T) {
	entities := captureTestEntities(10_000)
	forceCapture(t, false)
	serial, err := captureRecords(len(entities), spacedAt(entities))
	if err != nil {
		t.Fatal(err)
	}
	forceCapture(t, true)
	parallel, err := captureRecords(len(entities), spacedAt(entities))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(serial, parallel) {
		t.Fatal("parallel capture changed the records or their order")
	}
	var want []Record
	for _, se := range entities {
		e := se.Entity.(*captureTestEntity)
		if !e.transient && !e.empty {
			want = append(want, Record{EntityID: e.id, TypeName: "Thing", StateData: []byte(fmt.Sprintf("state-%d", e.id)), Space: se.Space})
		}
	}
	if !reflect.DeepEqual(parallel, want) {
		t.Fatalf("captured %d records, want %d in entity order", len(parallel), len(want))
	}
	if !bytes.Equal(encode(serial, "fp", 7), encode(parallel, "fp", 7)) {
		t.Fatal("parallel capture encodes to different bytes")
	}
	data, err := CaptureSpaced(entities, "fp")
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := Decode(data, "fp")
	if err != nil || !reflect.DeepEqual(decoded, want) {
		t.Fatalf("decoded %d records, %v", len(decoded), err)
	}
}

// The default thresholds take the parallel path for a large capture too.
func TestCaptureLargeDefault(t *testing.T) {
	entities := captureTestEntities(3 * parallelCaptureMin)
	large, err := captureRecords(len(entities), spacedAt(entities))
	if err != nil {
		t.Fatal(err)
	}
	forceCapture(t, false)
	serial, err := captureRecords(len(entities), spacedAt(entities))
	if err != nil || !reflect.DeepEqual(serial, large) {
		t.Fatalf("large capture differs from a serial one (%v)", err)
	}
}

// The pre-sized buffer does not change the encoded bytes.
func TestEncodeMatchesUnsizedWriter(t *testing.T) {
	records := []Record{
		{EntityID: 1, TypeName: "Thing", StateData: []byte("a")},
		{EntityID: 1 << 60, TypeName: strings.Repeat("LongTypeName", 40), StateData: bytes.Repeat([]byte{7}, 70_000), Space: ^registry.SpaceID(0)},
		{EntityID: 3, TypeName: "", StateData: []byte{}},
	}
	got := encode(records, "fingerprint", 123456789)
	decoded, err := Decode(got, "fingerprint")
	if err != nil || len(decoded) != len(records) {
		t.Fatalf("decode: %d records, %v", len(decoded), err)
	}
	for i := range records {
		if decoded[i].EntityID != records[i].EntityID || decoded[i].TypeName != records[i].TypeName || decoded[i].Space != records[i].Space || !bytes.Equal(decoded[i].StateData, records[i].StateData) {
			t.Fatalf("record %d did not round-trip", i)
		}
	}
}

// Whichever goroutine fails first, the reported error is the one a serial
// pass would report: the failing entity that comes first.
func TestCaptureParallelReportsFirstError(t *testing.T) {
	for _, parallel := range []bool{false, true} {
		forceCapture(t, parallel)
		entities := captureTestEntities(10_000)
		for _, i := range []int{9_500, 4_321, 7_000} {
			entities[i].Entity.(*captureTestEntity).fail = true
		}
		// Ten runs: goroutine scheduling must not change the answer.
		for run := 0; run < 10; run++ {
			_, err := CaptureSpaced(entities, "fp")
			if err == nil || !strings.Contains(err.Error(), "entity 4322 ") {
				t.Fatalf("parallel=%v: error = %v, want the first failing entity (4322)", parallel, err)
			}
		}
	}
}

// A panic in entity code reaches the caller, as it does without goroutines.
func TestCaptureParallelPropagatesPanic(t *testing.T) {
	forceCapture(t, true)
	entities := captureTestEntities(10_000)
	entities[6_000].Entity.(*captureTestEntity).boom = true
	defer func() {
		if r := recover(); r == nil || !strings.Contains(fmt.Sprint(r), "entity 6001 panicked") {
			t.Fatalf("recovered %v, want the entity's panic", r)
		}
	}()
	_, _ = CaptureSpaced(entities, "fp")
	t.Fatal("capture returned after an entity panicked")
}

// The first write to a path removes the temporary files a crashed process
// left for that path, and nothing else.
func TestWriteAsyncRemovesLeftoverTempFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "world.snap")
	leftovers := []string{".world.snap-123456.tmp", ".world.snap-7.tmp"}
	kept := []string{
		".world.snap-1-99.tmp", // temporary file of the destination "world.snap-1"
		".world.snap-.tmp",
		".world.snap-12a.tmp",
		".other.snap-55.tmp",
		"world.snap-123.tmp",
	}
	for _, name := range append(append([]string{}, leftovers...), kept...) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := <-WriteAsync(path, []byte("saved")); err != nil {
		t.Fatal(err)
	}
	for _, name := range leftovers {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Fatalf("leftover %s was not removed (%v)", name, err)
		}
	}
	for _, name := range append(kept, "world.snap") {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	// Only the first write of a process sweeps.
	late := filepath.Join(dir, ".world.snap-42.tmp")
	if err := os.WriteFile(late, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := <-WriteAsync(path, []byte("saved again")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(late); err != nil {
		t.Fatalf("a later write swept again: %v", err)
	}
}
