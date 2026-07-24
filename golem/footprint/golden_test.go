package footprint_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/demiurgos-hub/golem-engine/golem/footprint"
)

// GoldenFootprintsPath is the Phase 4 Unity exporter contract fixture (2D).
const GoldenFootprintsPath = "testdata/golden_footprints.golem.yaml"

func TestGoldenFootprints_frozenContract(t *testing.T) {
	data, err := os.ReadFile(GoldenFootprintsPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 {
		t.Fatal("golden fixture is empty")
	}
	set, err := footprint.Parse(data)
	if err != nil {
		t.Fatalf("golden must parse: %v", err)
	}
	if set.Dimensions != 2 || set.Version != footprint.FormatVersion {
		t.Fatalf("unexpected golden header: version=%d dimensions=%d", set.Version, set.Dimensions)
	}
	// Absolute path helper for cross-language tests that resolve from repo root.
	abs, err := filepath.Abs(GoldenFootprintsPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(abs); err != nil {
		t.Fatal(err)
	}
}
