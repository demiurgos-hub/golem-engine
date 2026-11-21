package schema

import (
	"path/filepath"
	"strings"
	"testing"
)

// A world type whose generated accessor or listeners would duplicate another
// world manager member is rejected when the schemas load, before any client
// code that could not compile is written.
func TestLoadWorldSchemasRejectsWorldManagerNameClashes(t *testing.T) {
	const fields = "\nfields:\n  x:\n    type: int32\n"
	for _, tc := range []struct {
		name    string
		worlds  []string
		wantErr string
	}{
		{"clear world", []string{"ClearWorld"}, `world type "ClearWorld" generates the world manager accessor ClearWorld, which clashes with the generated world manager method ClearWorld`},
		{"js spelling", []string{"clearWorld"}, `world type "clearWorld" generates the world manager accessor ClearWorld`},
		{"apply update", []string{"ApplyUpdate"}, `clashes with the generated world manager method ApplyUpdate`},
		{"apply world update", []string{"ApplyWorldUpdate"}, `clashes with the generated world manager method ApplyWorldUpdate`},
		// World types are checked in name order, so "OnZoneUpdate" claims first.
		{"another type's listener", []string{"Zone", "OnZoneUpdate"}, `world type "Zone" generates the world manager update listener OnZoneUpdate, which clashes with the accessor of world type "OnZoneUpdate"`},
		{"first letter case", []string{"Zone", "zone"}, `clashes with the accessor of world type "Zone"`},
		{"ordinary names", []string{"Zone", "Weather", "Clear", "World", "Update"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for i, world := range tc.worlds {
				mustWrite(t, filepath.Join(dir, string(rune('a'+i))+".yaml"), "world: "+world+fields)
			}
			_, err := LoadWorldSchemas(dir, nil)
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("ordinary world names rejected: %v", err)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Fatalf("error = %v, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}

func TestValidateWorldManagerNamesCatalogLookup(t *testing.T) {
	catalog := WorldTypeData{Name: "Item", IsCatalog: true, CatalogKeyField: "id"}
	if err := validateWorldManagerNames([]WorldTypeData{catalog, {Name: "GetItemById"}}); err == nil || !strings.Contains(err.Error(), `clashes with the catalog lookup of world type "Item"`) {
		t.Fatalf("error = %v", err)
	}
	// Without a keyed catalog there is no lookup to clash with.
	if err := validateWorldManagerNames([]WorldTypeData{{Name: "Item"}, {Name: "GetItemById"}}); err != nil {
		t.Fatal(err)
	}
}
