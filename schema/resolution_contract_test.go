package schema

import (
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestResolutionDirectoryContracts(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	if _, err := LoadSchemas(missing, 2, nil); err == nil {
		t.Fatal("entity directory must exist")
	}
	if _, err := LoadSchemas(t.TempDir(), 2, nil); err == nil {
		t.Fatal("entity directory must contain a schema")
	}
	if v, err := LoadCustomTypes(missing); err != nil || v != nil {
		t.Fatalf("optional types: %v %v", v, err)
	}
	if v, err := LoadCommands(missing, nil); err != nil || v != nil {
		t.Fatalf("optional commands: %v %v", v, err)
	}
	if v, err := LoadEvents(missing, nil); err != nil || v != nil {
		t.Fatalf("optional events: %v %v", v, err)
	}
	if v, err := LoadWorldSchemas(missing, nil); err != nil || v != nil {
		t.Fatalf("optional worlds: %v %v", v, err)
	}
	if v, err := LoadWorldSchemas(t.TempDir(), nil); err != nil || v == nil || len(v) != 0 {
		t.Fatalf("existing empty worlds: %v %v", v, err)
	}
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "a.yaml"), "entity: Zulu\n")
	mustWrite(t, filepath.Join(dir, "z.yaml"), "entity: Alpha\n")
	for _, name := range []string{"ignored.yml", "ignored.YAML"} {
		mustWrite(t, filepath.Join(dir, name), "not: [valid")
	}
	if err := os.Mkdir(filepath.Join(dir, "directory.yaml"), 0755); err != nil {
		t.Fatal(err)
	}
	entities, err := LoadSchemas(dir, 2, nil)
	if err != nil || len(entities) != 2 || entities[0].Name != "Zulu" || entities[1].Name != "Alpha" {
		t.Fatalf("filename ordering/filtering: %v %v", entities, err)
	}
	// Entity names deliberately do not share other loaders' uniqueness policy.
	mustWrite(t, filepath.Join(dir, "z.yaml"), "entity: Zulu\n")
	if entities, err = LoadSchemas(dir, 2, nil); err != nil || len(entities) != 2 {
		t.Fatalf("legacy duplicate entity names: %v", err)
	}
	for _, kind := range []string{"type", "command", "event", "world"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			body := kind + ": Same\ntarget: global\n"
			mustWrite(t, filepath.Join(dir, "a.yaml"), body)
			mustWrite(t, filepath.Join(dir, "b.yaml"), body)
			var err error
			switch kind {
			case "type":
				_, err = LoadCustomTypes(dir)
			case "command":
				_, err = LoadCommands(dir, nil)
			case "event":
				_, err = LoadEvents(dir, nil)
			case "world":
				_, err = LoadWorldSchemas(dir, nil)
			}
			if err == nil || !strings.Contains(err.Error(), "a.yaml and b.yaml") {
				t.Fatalf("duplicate diagnostic: %v", err)
			}
			// Validation of a.yaml must precede parsing a later malformed file.
			mustWrite(t, filepath.Join(dir, "a.yaml"), kind+": ''\n")
			mustWrite(t, filepath.Join(dir, "b.yaml"), "broken: [")
			switch kind {
			case "type":
				_, err = LoadCustomTypes(dir)
			case "command":
				_, err = LoadCommands(dir, nil)
			case "event":
				_, err = LoadEvents(dir, nil)
			case "world":
				_, err = LoadWorldSchemas(dir, nil)
			}
			if err == nil || !strings.Contains(err.Error(), "a.yaml:") {
				t.Fatalf("first failure changed: %v", err)
			}
		})
	}
}

func TestResolutionPayloadContracts(t *testing.T) {
	for _, text := range []string{"float", "{ type: float }", "{ type: float, tag: 7 }"} {
		var cmd CommandFieldDef
		var event EventFieldDef
		if err := yaml.Unmarshal([]byte(text), &cmd); err != nil {
			t.Fatal(err)
		}
		if err := yaml.Unmarshal([]byte(text), &event); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(payloadFieldDef(cmd), payloadFieldDef(event)) {
			t.Fatalf("shorthand differs: %s", text)
		}
	}
	for _, tag := range []int{-1, 0, 19000, 19999, 1 << 29} {
		fields := map[string]payloadFieldDef{"x": {Type: "float", Tag: &tag}}
		if err := validatePayloadFieldTags("event", "Hit", "global", fields); err == nil {
			t.Fatalf("illegal direct tag %d", tag)
		}
	}
	for _, tag := range []int{1, 18999, 20000, (1 << 29) - 1} {
		if err := validatePayloadFieldTags("command", "Ping", "session", map[string]payloadFieldDef{"x": {Type: "float", Tag: &tag}}); err != nil {
			t.Fatal(err)
		}
	}
	cmd := BuildCommandData(CommandSchemaFile{Command: "Move", Target: " entity ", Fields: map[string]CommandFieldDef{"x": {Type: "float"}}}, nil)
	if cmd.Target != " entity " || cmd.Fields[0].ProtoTag != 1 {
		t.Fatal("legacy command target normalization changed")
	}
	if err := ValidateCommands([]CommandData{{Name: "Odd", Target: "other"}}, nil); err != nil {
		t.Fatal(err)
	}
	ev := BuildEventData(EventSchemaFile{Event: "Hit", Target: " entity ", EntityType: "Actor", Fields: map[string]EventFieldDef{"x": {Type: "float"}}}, nil)
	if ev.Target != "entity" || ev.Fields[0].ProtoTag != 2 {
		t.Fatal("event normalization changed")
	}
	for _, kind := range []string{"command", "event"} {
		_, err := resolvePayloadFields(kind, "Payload", "session", map[string]payloadFieldDef{"x": {Type: "list<int32>"}}, nil)
		if err == nil || !strings.Contains(err.Error(), "not supported on "+kind+"s") {
			t.Fatalf("collection diagnostic: %v", err)
		}
	}
}

func TestResolutionCollectionPrecedenceAndCatalog(t *testing.T) {
	types := map[string]CustomTypeData{"int32": {Name: "int32"}}
	entity := BuildEntityData(SchemaFile{Entity: "Actor", Vars: map[string]SchemaVarDef{"values": {Type: "list<int32>", Tag: 1}}}, 2, types)
	world := BuildWorldTypeData(WorldSchemaFile{World: "Arena", Fields: map[string]WorldFieldDef{"values": {Type: "list<int32>"}}}, types)
	if entity.AllVars[0].ElemIsCustom || !world.Fields[0].ElemIsCustom {
		t.Fatal("legacy name precedence changed")
	}
	if entity.AllVars[0].ProtoType != "" {
		t.Fatal("collection fingerprint input changed")
	}
	for _, raw := range []string{"list<bytes>", "dict<string, int32>", "dict<float, int32>", "dict<bytes, int32>"} {
		if _, err := resolveCollection(raw, nil, scalarFirst); err != nil {
			t.Fatalf("legacy collection acceptance %q: %v", raw, err)
		}
	}
	item := BuildCustomTypeData(TypeSchemaFile{Type: "Item", Fields: map[string]TypeFieldDef{"id": {Type: "uint32", Tag: 2}}})
	info, err := ResolveCatalog("Catalog", &WorldSourceDef{Type: "Item", Key: "id"}, map[string]CustomTypeData{"Item": item})
	if err != nil || info.Key.GoName != "Id" || info.Collection.MapKeyGoType != "uint32" || info.Collection.CustomType.Name != "Item" {
		t.Fatalf("catalog: %#v %v", info, err)
	}
	if _, err := ResolveCatalog("Catalog", &WorldSourceDef{Type: "Item", Key: "missing"}, map[string]CustomTypeData{"Item": item}); err == nil {
		t.Fatal("missing key accepted")
	}
}

func TestResolutionLegacyTagStrictnessAndLayouts(t *testing.T) {
	for _, tag := range []int{-1, 19000, 1 << 29} {
		ct := BuildCustomTypeData(TypeSchemaFile{Type: "Item", Fields: map[string]TypeFieldDef{"id": {Type: "int32", Tag: tag}}})
		if ct.Fields[0].ProtoTag != tag {
			t.Fatal("custom tag tightened")
		}
		entity := BuildEntityData(SchemaFile{Entity: "Actor", Vars: map[string]SchemaVarDef{"id": {Type: "int32", Tag: tag}}}, 2, nil)
		if entity.AllVars[0].ProtoTag != tag+3 {
			t.Fatal("entity tag tightened")
		}
	}
	for _, dim := range []int{2, 3} {
		entity := BuildEntityData(SchemaFile{Entity: "Actor", Vars: map[string]SchemaVarDef{"tick": {Type: "string", Tag: 5}, "once": {Type: "int32", Tag: 1, Sync: "once"}}}, dim, nil)
		wire := entity.WireLayout()
		last := wire.Delta[len(wire.Delta)-1]
		if last.ProtoTag != dim+6 || last.BitIndex != dim || last.DeltaPresenceCS() != Reference || !last.Optional {
			t.Fatalf("delta: %+v", last)
		}
		if wire.State[dim+1].ProtoTag != 1000 || len(wire.State) != len(wire.Delta)+1 {
			t.Fatal("state/delta layout")
		}
	}
	legacy := EntityData{AllVars: []VarInfo{{UserTag: 1, ProtoTag: 99}}}
	if got := legacy.WireLayout().State[4].ProtoTag; got != 4 {
		t.Fatalf("legacy codec tag %d", got)
	}
	if got := legacy.ProtoWireLayout(0).State[4].ProtoTag; got != 2 {
		t.Fatalf("legacy proto tag %d", got)
	}
	for _, tag := range []int{19000, 1 << 29} {
		dir := t.TempDir()
		mustWrite(t, filepath.Join(dir, "a.yaml"), fmtWorldTag(tag))
		if _, err := LoadWorldSchemas(dir, nil); err != nil {
			t.Fatalf("world range tightened: %v", err)
		}
	}
}

func fmtWorldTag(tag int) string { return "world: Arena\ntag: " + strconv.Itoa(tag) + "\n" }

func TestResolutionScalarAndCatalogContracts(t *testing.T) {
	info, ok := LookupScalar("sint32")
	if !ok || info.PointerHelper != "Int32" || info.WireType != 0 || info.CSType != "int" {
		t.Fatalf("signed scalar metadata: %+v", info)
	}
	info.GoType = "changed"
	if got, _ := LookupScalar("sint32"); got.GoType != "int32" {
		t.Fatal("scalar lookup exposed mutable catalog state")
	}
	for _, typ := range []string{"bytes", "string"} {
		if (VarInfo{ProtoType: typ}).DeltaPresenceCS() != Reference {
			t.Fatalf("%s must remain a C# reference", typ)
		}
	}
	if _, ok := LookupScalar("Unknown"); ok {
		t.Fatal("unknown scalar accepted")
	}
	// Catalog content validation compares raw YAML keys; generated loaders may
	// subsequently coerce them to the same value. Do not merge those policies.
	path := filepath.Join(t.TempDir(), "items.yaml")
	mustWrite(t, path, "- id: 1\n  label: first\n- id: '1'\n  label: second\n")
	if err := ValidateCatalogFile(path, "id"); err != nil {
		t.Fatal(err)
	}
}
