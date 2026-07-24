package footprint_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/demiurgos-hub/golem-engine/golem/footprint"
)

func TestParse_golden2D(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "golden_footprints.golem.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	set, err := footprint.Parse(data)
	if err != nil {
		t.Fatalf("Parse golden: %v", err)
	}
	if set.Version != 1 || set.Dimensions != 2 {
		t.Fatalf("version/dimensions = %d/%d", set.Version, set.Dimensions)
	}

	wall, ok := set.LookupGUID("0123456789ABCDEF0123456789ABCDEF")
	if !ok {
		t.Fatal("LookupGUID case-insensitive failed")
	}
	if wall.Alias != "wall" || wall.Name != "Wall" {
		t.Fatalf("wall identity = %+v", wall)
	}
	if len(wall.Shapes) != 2 {
		t.Fatalf("wall shapes = %d", len(wall.Shapes))
	}
	if wall.Shapes[0].Type != footprint.ShapeAABB || wall.Shapes[0].W != 2 || wall.Shapes[0].H != 1 {
		t.Fatalf("wall aabb = %+v", wall.Shapes[0])
	}
	if wall.Shapes[0].OffsetX != 0 || wall.Shapes[0].OffsetY != 0.5 {
		t.Fatalf("wall aabb offset = %+v", wall.Shapes[0])
	}
	if wall.Shapes[1].Type != footprint.ShapeCircle || wall.Shapes[1].R != 0.25 || !wall.Shapes[1].Trigger {
		t.Fatalf("wall circle = %+v", wall.Shapes[1])
	}

	byAlias, ok := set.LookupAlias("wall")
	if !ok || byAlias != wall {
		t.Fatal("LookupAlias(wall) mismatch")
	}
	cap, ok := set.LookupGUID("fedcba9876543210fedcba9876543210")
	if !ok || cap.Alias != "" {
		t.Fatalf("cap footprint = %+v ok=%v", cap, ok)
	}
	// Duplicate diagnostic name is allowed.
	if wall.Name != cap.Name {
		t.Fatal("expected duplicate diagnostic names in golden fixture")
	}
}

func TestParse_golden3D(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "golden_footprints_3d.golem.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	set, err := footprint.Parse(data)
	if err != nil {
		t.Fatalf("Parse 3D golden: %v", err)
	}
	fp, ok := set.LookupAlias("crate")
	if !ok || set.Dimensions != 3 {
		t.Fatalf("crate lookup ok=%v dimensions=%d", ok, set.Dimensions)
	}
	if len(fp.Shapes) != 2 || fp.Shapes[0].D != 3 || fp.Shapes[1].Type != footprint.ShapeSphere {
		t.Fatalf("crate shapes = %+v", fp.Shapes)
	}
	if fp.Shapes[1].OffsetZ != 0.25 {
		t.Fatalf("sphere offset z = %v", fp.Shapes[1].OffsetZ)
	}
}

func TestLoad_path(t *testing.T) {
	set, err := footprint.Load(filepath.Join("testdata", "golden_footprints.golem.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := set.LookupAlias("wall"); !ok {
		t.Fatal("expected wall alias")
	}
}

func TestParse_rejectsMalformed(t *testing.T) {
	cases := []struct {
		name string
		yaml string
		want string
	}{
		{"missing version", "dimensions: 2\nfootprints: {}\n", "version is required"},
		{"version", "version: 2\ndimensions: 2\nfootprints: {}\n", "unsupported version 2 (supported: 1)"},
		{"missing dimensions", "version: 1\nfootprints: {}\n", "dimensions is required"},
		{"dimensions", "version: 1\ndimensions: 4\nfootprints: {}\n", "dimensions must be 2 or 3, got 4"},
		{"missing footprints", "version: 1\ndimensions: 2\n", "footprints map is required"},
		{"bad guid", "version: 1\ndimensions: 2\nfootprints:\n  not-a-guid:\n    shapes: []\n", "invalid GUID"},
		{"duplicate alias", `
version: 1
dimensions: 2
footprints:
  aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa:
    alias: wall
    shapes: []
  bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb:
    alias: wall
    shapes: []
`, "duplicate alias"},
		{"unknown field", "version: 1\ndimensions: 2\nfootprints: {}\nextra: 1\n", "field extra not found"},
		{"2d sphere", `
version: 1
dimensions: 2
footprints:
  aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa:
    shapes:
      - type: sphere
        r: 1
        offset: {x: 0, y: 0}
        layer: Default
`, "sphere is not valid"},
		{"3d circle", `
version: 1
dimensions: 3
footprints:
  aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa:
    shapes:
      - type: circle
        r: 1
        offset: {x: 0, y: 0, z: 0}
        layer: Default
`, "circle is not valid"},
		{"offset z in 2d", `
version: 1
dimensions: 2
footprints:
  aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa:
    shapes:
      - type: circle
        r: 1
        offset: {x: 0, y: 0, z: 0}
        layer: Default
`, "exactly keys x,y"},
		{"non positive r", `
version: 1
dimensions: 2
footprints:
  aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa:
    shapes:
      - type: circle
        r: 0
        offset: {x: 0, y: 0}
        layer: Default
`, "r must be > 0"},
		{"missing layer", `
version: 1
dimensions: 2
footprints:
  aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa:
    shapes:
      - type: circle
        r: 1
        offset: {x: 0, y: 0}
`, "layer is required"},
		{"aabb missing d in 3d", `
version: 1
dimensions: 3
footprints:
  aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa:
    shapes:
      - type: aabb
        w: 1
        h: 1
        offset: {x: 0, y: 0, z: 0}
        layer: Default
`, "d must be > 0"},
		{"whitespace alias", `
version: 1
dimensions: 2
footprints:
  aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa:
    alias: "   "
    shapes: []
`, "alias must not be whitespace"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := footprint.Parse([]byte(tc.yaml))
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not contain %q", err.Error(), tc.want)
			}
		})
	}
}

func TestLookup_namesAreNotIdentity(t *testing.T) {
	set, err := footprint.Parse([]byte(`
version: 1
dimensions: 2
footprints:
  aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa:
    name: Shared
    shapes: []
  bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb:
    name: Shared
    shapes: []
`))
	if err != nil {
		t.Fatal(err)
	}
	a, _ := set.LookupGUID("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	b, _ := set.LookupGUID("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	if a.Name != b.Name {
		t.Fatal("expected shared diagnostic names")
	}
	if _, ok := set.LookupAlias("Shared"); ok {
		t.Fatal("names must not be alias identities")
	}
}

func TestParse_sortedGUIDKeysDeterministicErrors(t *testing.T) {
	yaml := `
version: 1
dimensions: 2
footprints:
  zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz:
    shapes: []
  not-a-guid:
    shapes: []
  aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa:
    shapes: []
`
	_, err := footprint.Parse([]byte(yaml))
	if err == nil {
		t.Fatal("expected error")
	}
	// Lexicographic key order hits "aaaaaaaa..." first (valid), then "not-a-guid".
	if !strings.Contains(err.Error(), `invalid GUID "not-a-guid"`) {
		t.Fatalf("got %v", err)
	}
}

func TestLoad_singleFootprintPrefixOnParseError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.golem.yaml")
	if err := os.WriteFile(path, []byte("dimensions: 2\nfootprints: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := footprint.Load(path)
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	if !strings.Contains(msg, path) || !strings.Contains(msg, "version is required") {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Count(msg, "footprint:") != 1 {
		t.Fatalf("expected single footprint: prefix, got %q", msg)
	}
}
