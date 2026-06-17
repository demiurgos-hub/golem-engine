package schema

import (
	"testing"
)

func TestBuildWorldTypeData_noSourceNoSyntheticField(t *testing.T) {
	wf := WorldSchemaFile{
		World:  "Zone",
		Fields: map[string]WorldFieldDef{"radius": {Type: "float"}},
	}
	wt := BuildWorldTypeData(wf, nil)
	if len(wt.Fields) != 1 {
		t.Fatalf("expected 1 field, got %d", len(wt.Fields))
	}
	if wt.Fields[0].SnakeName != "radius" {
		t.Errorf("expected field radius, got %q", wt.Fields[0].SnakeName)
	}
	if wt.Source != nil {
		t.Error("Source should be nil when no source: block")
	}
}

func TestBuildWorldTypeData_sourceInjectsTileDataLast(t *testing.T) {
	wf := WorldSchemaFile{
		World:  "Level1",
		Fields: map[string]WorldFieldDef{"height": {Type: "int32"}, "width": {Type: "int32"}},
		Source: &WorldSourceDef{Format: "tiled", File: "maps/level1.tmj"},
	}
	wt := BuildWorldTypeData(wf, nil)

	// width and height are sorted alphabetically; tile_data is last.
	if len(wt.Fields) != 3 {
		t.Fatalf("expected 3 fields (height, width, tile_data), got %d", len(wt.Fields))
	}
	last := wt.Fields[len(wt.Fields)-1]
	if last.SnakeName != "tile_data" {
		t.Errorf("last field should be tile_data, got %q", last.SnakeName)
	}
	if last.ProtoType != "bytes" {
		t.Errorf("tile_data should have proto type bytes, got %q", last.ProtoType)
	}
	if last.GoType != "[]byte" {
		t.Errorf("tile_data should have Go type []byte, got %q", last.GoType)
	}
}

func TestBuildWorldTypeData_sourceURLPrefixInjectsMapURL(t *testing.T) {
	wf := WorldSchemaFile{
		World: "Level2",
		Source: &WorldSourceDef{
			Format:    "ldtk",
			File:      "maps/level2.ldtk",
			URLPrefix: "/maps/",
		},
	}
	wt := BuildWorldTypeData(wf, nil)

	if len(wt.Fields) != 1 {
		t.Fatalf("expected 1 field (map_url), got %d", len(wt.Fields))
	}
	f := wt.Fields[0]
	if f.SnakeName != "map_url" {
		t.Errorf("expected field map_url, got %q", f.SnakeName)
	}
	if f.ProtoType != "string" {
		t.Errorf("map_url should have proto type string, got %q", f.ProtoType)
	}
}

func TestBuildWorldTypeData_extractFieldsMergedWithFields(t *testing.T) {
	wf := WorldSchemaFile{
		World:  "Zone",
		Fields: map[string]WorldFieldDef{"radius": {Type: "float"}},
		Source: &WorldSourceDef{
			Format: "tiled",
			File:   "maps/zone.tmj",
			Extract: []WorldExtractDef{
				{Name: "gravity", Type: "float"},
				{Name: "max_enemies", Type: "int32"},
			},
		},
	}
	wt := BuildWorldTypeData(wf, nil)

	// Fields: gravity, max_enemies, radius (sorted) + tile_data (synthetic last).
	if len(wt.Fields) != 4 {
		t.Fatalf("expected 4 fields, got %d: %v", len(wt.Fields), fieldNames(wt.Fields))
	}

	names := fieldNames(wt.Fields)
	if names[0] != "gravity" || names[1] != "max_enemies" || names[2] != "radius" {
		t.Errorf("unexpected field order: %v", names)
	}
	if names[3] != "tile_data" {
		t.Errorf("last field should be tile_data, got %q", names[3])
	}
}

func TestBuildWorldTypeData_extractFieldsHaveStableProtoTags(t *testing.T) {
	wf := WorldSchemaFile{
		World: "Zone",
		Source: &WorldSourceDef{
			Format: "tiled",
			File:   "maps/zone.tmj",
			Extract: []WorldExtractDef{
				{Name: "beta", Type: "int32"},
				{Name: "alpha", Type: "int32"},
			},
		},
	}
	wt := BuildWorldTypeData(wf, nil)

	// Sorted: alpha=1, beta=2, tile_data=3.
	if wt.Fields[0].SnakeName != "alpha" || wt.Fields[0].ProtoTag != 1 {
		t.Errorf("alpha should have tag 1, got tag %d", wt.Fields[0].ProtoTag)
	}
	if wt.Fields[1].SnakeName != "beta" || wt.Fields[1].ProtoTag != 2 {
		t.Errorf("beta should have tag 2, got tag %d", wt.Fields[1].ProtoTag)
	}
	if wt.Fields[2].SnakeName != "tile_data" || wt.Fields[2].ProtoTag != 3 {
		t.Errorf("tile_data should have tag 3, got tag %d", wt.Fields[2].ProtoTag)
	}
}

func TestBuildWorldTypeData_sourceCarriedIntoWorldTypeData(t *testing.T) {
	src := &WorldSourceDef{Format: "ldtk", File: "maps/world.ldtk"}
	wf := WorldSchemaFile{World: "World", Source: src}
	wt := BuildWorldTypeData(wf, nil)
	if wt.Source == nil {
		t.Fatal("Source should be carried into WorldTypeData")
	}
	if wt.Source.Format != "ldtk" {
		t.Errorf("expected format ldtk, got %q", wt.Source.Format)
	}
}

func TestLoadWorldSchemas_sourceInvalidFormat(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, dir+"/level.yaml", "world: Level\nsource:\n  format: godot\n  file: maps/x.map\n")

	_, err := LoadWorldSchemas(dir, nil)
	if err == nil {
		t.Fatal("expected error for unknown source format")
	}
}

func TestLoadWorldSchemas_sourceEmptyFile(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, dir+"/level.yaml", "world: Level\nsource:\n  format: tiled\n  file: \"\"\n")

	_, err := LoadWorldSchemas(dir, nil)
	if err == nil {
		t.Fatal("expected error for empty source.file")
	}
}

func TestLoadWorldSchemas_sourceValidTiledLoads(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, dir+"/level.yaml",
		"world: Level\nsource:\n  format: tiled\n  file: maps/level.tmj\n"+
			"  extract:\n    - name: gravity\n      type: float\n")

	types, err := LoadWorldSchemas(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(types) != 1 {
		t.Fatalf("expected 1 type, got %d", len(types))
	}
	if types[0].Source == nil || types[0].Source.Format != "tiled" {
		t.Error("Source not populated on loaded world type")
	}
}

func fieldNames(fields []WorldFieldInfo) []string {
	names := make([]string, len(fields))
	for i, f := range fields {
		names[i] = f.SnakeName
	}
	return names
}

// --- Catalog source tests ---

func itemCustomTypes() map[string]CustomTypeData {
	return map[string]CustomTypeData{
		"Item": BuildCustomTypeData(TypeSchemaFile{
			Type: "Item",
			Fields: map[string]TypeFieldDef{
				"id":     {Type: "int32", Tag: 1},
				"name":   {Type: "string", Tag: 2},
				"damage": {Type: "int32", Tag: 3},
			},
		}),
	}
}

func TestBuildWorldTypeData_catalogListMode(t *testing.T) {
	wf := WorldSchemaFile{
		World: "ItemCatalog",
		Source: &WorldSourceDef{
			Format: "catalog",
			File:   "catalogs/items.yaml",
			Type:   "Item",
		},
	}
	wt := BuildWorldTypeData(wf, itemCustomTypes())

	if !wt.IsCatalog {
		t.Error("IsCatalog should be true")
	}
	if wt.CatalogKeyField != "" {
		t.Errorf("CatalogKeyField should be empty for list mode, got %q", wt.CatalogKeyField)
	}
	if len(wt.Fields) != 1 {
		t.Fatalf("expected 1 synthetic field, got %d", len(wt.Fields))
	}
	f := wt.Fields[0]
	if f.SnakeName != "items" {
		t.Errorf("field name = %q, want items", f.SnakeName)
	}
	if !f.IsRepeated {
		t.Error("synthetic field should be repeated (list mode)")
	}
	if f.IsMap {
		t.Error("synthetic field should not be a map in list mode")
	}
	if f.ElemProtoType != "Item" {
		t.Errorf("ElemProtoType = %q, want Item", f.ElemProtoType)
	}
	if !f.ElemIsCustom {
		t.Error("ElemIsCustom should be true")
	}
	if f.ProtoTag != 1 {
		t.Errorf("ProtoTag = %d, want 1", f.ProtoTag)
	}
}

func TestBuildWorldTypeData_catalogDictMode(t *testing.T) {
	wf := WorldSchemaFile{
		World: "ItemCatalog",
		Source: &WorldSourceDef{
			Format: "catalog",
			File:   "catalogs/items.yaml",
			Type:   "Item",
			Key:    "id",
		},
	}
	wt := BuildWorldTypeData(wf, itemCustomTypes())

	if !wt.IsCatalog {
		t.Error("IsCatalog should be true")
	}
	if wt.CatalogKeyField != "id" {
		t.Errorf("CatalogKeyField = %q, want id", wt.CatalogKeyField)
	}
	if len(wt.Fields) != 1 {
		t.Fatalf("expected 1 synthetic field, got %d", len(wt.Fields))
	}
	f := wt.Fields[0]
	if !f.IsMap {
		t.Error("synthetic field should be a map in dict mode")
	}
	if f.IsRepeated {
		t.Error("synthetic field should not be repeated in dict mode")
	}
	if f.MapKeyProtoType != "int32" {
		t.Errorf("MapKeyProtoType = %q, want int32", f.MapKeyProtoType)
	}
	if f.ElemProtoType != "Item" {
		t.Errorf("ElemProtoType = %q, want Item", f.ElemProtoType)
	}
	if wt.CatalogKeyTSType != "number" {
		t.Errorf("CatalogKeyTSType = %q, want number", wt.CatalogKeyTSType)
	}
}

func TestBuildWorldTypeData_catalogMissingTypeFails(t *testing.T) {
	// BuildWorldTypeData calls log.Fatalf for unknown types; the test is a
	// schema-level check via validateWorldSource which returns an error for
	// a missing type: field.
	filename := "items.yaml"
	src := &WorldSourceDef{Format: "catalog", File: "catalogs/items.yaml", Type: ""}
	if err := validateWorldSource(filename, src); err == nil {
		t.Fatal("expected error for catalog source with empty type")
	}
}

func TestValidateCatalogFile_uniqueKeys(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/items.yaml"
	mustWrite(t, path, "- id: 1\n  name: Sword\n- id: 2\n  name: Staff\n")

	if err := ValidateCatalogFile(path, "id"); err != nil {
		t.Fatalf("unexpected error for unique keys: %v", err)
	}
}

func TestValidateCatalogFile_duplicateKeyErrors(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/items.yaml"
	mustWrite(t, path, "- id: 1\n  name: Sword\n- id: 1\n  name: Dagger\n")

	if err := ValidateCatalogFile(path, "id"); err == nil {
		t.Fatal("expected error for duplicate key")
	}
}

func TestValidateCatalogFile_noKeyFieldSkipsCheck(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/items.yaml"
	// Even with duplicate ids, no error when keyField is empty.
	mustWrite(t, path, "- id: 1\n  name: Sword\n- id: 1\n  name: Dagger\n")

	if err := ValidateCatalogFile(path, ""); err != nil {
		t.Fatalf("unexpected error with empty keyField: %v", err)
	}
}

func TestValidateCatalogFile_missingKeyFieldErrors(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/items.yaml"
	mustWrite(t, path, "- name: Sword\n- name: Staff\n") // no id field

	if err := ValidateCatalogFile(path, "id"); err == nil {
		t.Fatal("expected error for missing key field in entry")
	}
}

func TestLoadWorldSchemas_catalogSourceInvalidMissingType(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, dir+"/items.yaml",
		"world: ItemCatalog\nsource:\n  format: catalog\n  file: catalogs/items.yaml\n")

	_, err := LoadWorldSchemas(dir, nil)
	if err == nil {
		t.Fatal("expected error for catalog source with empty type")
	}
}

func TestLoadWorldSchemas_catalogSourceValid(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, dir+"/items.yaml",
		"world: ItemCatalog\nsource:\n  format: catalog\n  file: catalogs/items.yaml\n  type: Item\n  key: id\n")

	types, err := LoadWorldSchemas(dir, itemCustomTypes())
	if err != nil {
		t.Fatal(err)
	}
	if len(types) != 1 {
		t.Fatalf("expected 1 type, got %d", len(types))
	}
	wt := types[0]
	if !wt.IsCatalog {
		t.Error("IsCatalog should be true")
	}
	if wt.Source == nil || wt.Source.Format != "catalog" {
		t.Error("Source not populated on loaded world type")
	}
}
