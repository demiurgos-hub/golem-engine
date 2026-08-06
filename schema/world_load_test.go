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
	if types[0].UpdateTag != 1 || types[1].UpdateTag != 2 {
		t.Fatalf("auto tags = %d, %d; want 1, 2", types[0].UpdateTag, types[1].UpdateTag)
	}
}

func TestLoadWorldSchemas_explicitUpdateTags(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "new.yaml"), "world: NewMap\ntag: 9\nfields:\n  a:\n    type: int32\n")
	mustWrite(t, filepath.Join(dir, "old.yaml"), "world: OldMap\ntag: 7\nfields:\n  b:\n    type: int32\n")

	types, err := LoadWorldSchemas(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(types) != 2 || types[0].Name != "NewMap" || types[1].Name != "OldMap" {
		t.Fatalf("unexpected order/types: %+v", types)
	}
	if types[0].UpdateTag != 9 || types[1].UpdateTag != 7 {
		t.Fatalf("explicit tags = %d, %d; want 9, 7", types[0].UpdateTag, types[1].UpdateTag)
	}
	pd := BuildProtoData(&Config{}, nil, nil, types, nil, nil)
	if len(pd.WorldUpdateFields) != 2 {
		t.Fatalf("WorldUpdateFields len = %d", len(pd.WorldUpdateFields))
	}
	if pd.WorldUpdateFields[0].Tag != 9 || pd.WorldUpdateFields[1].Tag != 7 {
		t.Fatalf("proto tags = %d, %d; want 9, 7", pd.WorldUpdateFields[0].Tag, pd.WorldUpdateFields[1].Tag)
	}
}

func TestLoadWorldSchemas_mixedTagsRejected(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "a.yaml"), "world: Alpha\ntag: 1\nfields:\n  a:\n    type: int32\n")
	mustWrite(t, filepath.Join(dir, "b.yaml"), "world: Beta\nfields:\n  b:\n    type: int32\n")

	_, err := LoadWorldSchemas(dir, nil)
	if err == nil {
		t.Fatal("expected mixed tag error")
	}
}

func TestLoadWorldSchemas_duplicateTagsRejected(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "a.yaml"), "world: Alpha\ntag: 3\nfields:\n  a:\n    type: int32\n")
	mustWrite(t, filepath.Join(dir, "b.yaml"), "world: Beta\ntag: 3\nfields:\n  b:\n    type: int32\n")

	_, err := LoadWorldSchemas(dir, nil)
	if err == nil {
		t.Fatal("expected duplicate tag error")
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
