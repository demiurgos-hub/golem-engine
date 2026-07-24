package footprint_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/demiurgos-hub/golem-engine/golem/collision"
	"github.com/demiurgos-hub/golem-engine/golem/footprint"
)

// End-to-end Phase 5 fixture: a non-entity wall footprint is placed and removed through the
// collision-only placer. Synthetic IDs stay negative (collision-only); there is no registry entity
// and no replication surface for the wall.
func TestE2E_WallFootprint_PlaceAndRemove_NoEntityIDs(t *testing.T) {
	path := filepath.Join(testdataDir(t), "e2e_wall.golem.yaml")
	set, err := footprint.Load(path)
	if err != nil {
		t.Fatal(err)
	}

	fp, ok := set.LookupAlias("scribe_e2e_wall")
	if !ok {
		t.Fatal("expected alias scribe_e2e_wall")
	}
	if _, ok := set.LookupGUID("fedcba9876543210fedcba9876543210"); !ok {
		t.Fatal("expected GUID lookup for wall")
	}
	if fp.Name != "ScribeE2EWall" {
		t.Fatalf("name = %q", fp.Name)
	}
	if len(fp.Shapes) != 1 {
		t.Fatalf("shapes = %d", len(fp.Shapes))
	}

	backend := &stub2D{}
	layers := collision.NewLayers().Define("Default")
	placer := &footprint.Placer2D{
		Backend: backend,
		Resolve: footprint.LayersResolver(layers),
		IDs:     footprint.NewAtomicAllocator(),
	}

	handle, err := placer.Place(fp, footprint.Transform2D{X: 3, Y: 4, Scale: 1, RotationDegrees: 0})
	if err != nil {
		t.Fatal(err)
	}
	ids := handle.IDs()
	if len(ids) != 1 {
		t.Fatalf("ids = %v", ids)
	}
	if ids[0] >= 0 {
		t.Fatalf("wall collision id must be synthetic negative, got %d", ids[0])
	}
	if len(backend.adds) != 1 || len(backend.updates) != 1 {
		t.Fatalf("adds=%d updates=%d", len(backend.adds), len(backend.updates))
	}
	if backend.updates[0].x != 3 || backend.updates[0].y != 4.5 {
		t.Fatalf("world pos = (%v,%v)", backend.updates[0].x, backend.updates[0].y)
	}

	handle.Remove()
	handle.Remove()
	if len(backend.removes) != 1 || backend.removes[0] != ids[0] {
		t.Fatalf("removes = %v", backend.removes)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "entity:") {
		t.Fatal("wall footprint fixture must not contain entity schema keys")
	}
}

func testdataDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(file), "testdata")
}
