package schema

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadWorldSchemas_duplicateWorldType(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "a.yaml"), "world: Zone\nfields:\n  x:\n    type: int32\n")
	mustWrite(t, filepath.Join(dir, "b.yaml"), "world: Zone\nfields:\n  y:\n    type: int32\n")

	_, err := LoadWorldSchemas(dir, nil)
	if err == nil {
		t.Fatal("expected error for duplicate world type")
	}
}

func TestLoadWorldSchemas_emptyWorldName(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "bad.yaml"), "world: \"\"\nfields: {}\n")

	_, err := LoadWorldSchemas(dir, nil)
	if err == nil {
		t.Fatal("expected error for empty world name")
	}
}

func TestLoadWorldSchemas_sortedByTypeName(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "z.yaml"), "world: Zulu\nfields:\n  a:\n    type: int32\n")
	mustWrite(t, filepath.Join(dir, "a.yaml"), "world: Alpha\nfields:\n  b:\n    type: int32\n")

	types, err := LoadWorldSchemas(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(types) != 2 {
		t.Fatalf("got %d types, want 2", len(types))
	}
	if types[0].Name != "Alpha" || types[1].Name != "Zulu" {
		t.Fatalf("order = %q, %q; want Alpha, Zulu", types[0].Name, types[1].Name)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
