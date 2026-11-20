package codegen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/demiurgos-hub/golem-engine/golem/inspector/wire"
	"github.com/demiurgos-hub/golem-engine/schema"
)

func loadInspectorFixture(t *testing.T, dims int) ([]schema.EntityData, schema.ProtoTemplateData) {
	t.Helper()
	dir := t.TempDir()
	writeConsumerFile(t, dir, "types/item.yaml", "type: Item\nfields:\n  name: {tag: 1, type: string}\n  qty: {tag: 2, type: int32}\n")
	writeConsumerFile(t, dir, "entities/actor.yaml", `entity: Actor
vars:
  hp: {tag: 1, type: int32, sync: tick}
  name: {tag: 2, type: string, sync: once}
  gold: {tag: 3, type: uint32, sync: tick, visibility: owner}
  tags: {tag: 4, type: "list<string>", sync: tick}
  stats: {tag: 5, type: "dict<string, int32>", sync: tick}
  bag: {tag: 6, type: "list<Item>", sync: tick}
`)
	writeConsumerFile(t, dir, "entities/marker.yaml", "entity: Marker\nglobal: true\nvars:\n  label: {tag: 1, type: string, sync: tick}\n")
	types, err := schema.LoadCustomTypes(filepath.Join(dir, "types"))
	if err != nil {
		t.Fatal(err)
	}
	typeMap := map[string]schema.CustomTypeData{}
	for _, ct := range types {
		typeMap[ct.Name] = ct
	}
	entities, err := schema.LoadSchemas(filepath.Join(dir, "entities"), dims, typeMap)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &schema.Config{}
	cfg.Simulation.Dimensions = dims
	proto, err := schema.ResolveProtoData(cfg, entities, nil, nil, types, nil)
	if err != nil {
		t.Fatal(err)
	}
	return entities, proto
}

func fieldByName(t *testing.T, e *wire.Entity, name string) wire.Field {
	t.Helper()
	for _, f := range e.Fields {
		if f.Name == name {
			return f
		}
	}
	t.Fatalf("entity %s has no field %q", e.Name, name)
	return wire.Field{}
}

func TestBuildInspectorSchema(t *testing.T) {
	for _, dims := range []int{2, 3} {
		entities, proto := loadInspectorFixture(t, dims)
		s, err := buildInspectorSchema(entities, proto)
		if err != nil {
			t.Fatalf("dims %d: %v", dims, err)
		}
		if s.Dimensions != uint32(dims) || s.Fingerprint != ComputeSchemaFingerprint(entities) {
			t.Fatalf("dims %d: header %+v", dims, s)
		}
		actor := s.EntityByTag(1)
		if actor == nil || actor.Name != "Actor" || actor.DeltaTag != 2 {
			t.Fatalf("dims %d: actor %+v", dims, actor)
		}
		marker := s.EntityByTag(3)
		if marker == nil || !marker.Global || s.RemovedTag != 5 {
			t.Fatalf("dims %d: marker %+v removed %d", dims, marker, s.RemovedTag)
		}
		offset := uint32(dims + 1)
		posBits := int32(dims)
		check := func(name string, want wire.Field) {
			t.Helper()
			got := fieldByName(t, actor, name)
			want.Name = name
			if got != want {
				t.Errorf("dims %d: %s = %+v, want %+v", dims, name, got, want)
			}
		}
		check("entity_id", wire.Field{Tag: 1, Bit: -1, Type: "int64", Role: wire.RoleIdentity})
		check("pos_x", wire.Field{Tag: 2, Bit: 0, Type: "float", Role: wire.RolePosition})
		check("revision", wire.Field{Tag: 1000, Bit: -1, Type: "uint64", Role: wire.RoleRevision})
		check("hp", wire.Field{Tag: 1 + offset, Bit: posBits, Sync: "tick", Type: "int32", Role: wire.RoleVariable})
		check("name", wire.Field{Tag: 2 + offset, Bit: -1, Sync: "once", Type: "string", Role: wire.RoleVariable})
		check("gold", wire.Field{Tag: 3 + offset, Bit: posBits + 1, Sync: "tick", OwnerOnly: true, Type: "uint32", Role: wire.RoleVariable})
		check("tags", wire.Field{Tag: 4 + offset, Bit: posBits + 2, Sync: "tick", Kind: wire.KindList, Type: "string", Role: wire.RoleVariable})
		check("stats", wire.Field{Tag: 5 + offset, Bit: posBits + 3, Sync: "tick", Kind: wire.KindMap, Type: "int32", KeyType: "string", Role: wire.RoleVariable})
		check("bag", wire.Field{Tag: 6 + offset, Bit: posBits + 4, Sync: "tick", Kind: wire.KindList, Type: "Item", Role: wire.RoleVariable})
		hasZ := false
		for _, f := range actor.Fields {
			hasZ = hasZ || f.Name == "pos_z"
		}
		if hasZ != (dims == 3) || actor.Is3D != (dims == 3) {
			t.Errorf("dims %d: pos_z present=%v is3d=%v", dims, hasZ, actor.Is3D)
		}
		if len(s.Types) != 1 || s.Types[0].Name != "Item" || len(s.Types[0].Fields) != 2 {
			t.Errorf("dims %d: types %+v", dims, s.Types)
		}
	}
}

func TestGenerateGoInspectorSchemaFile(t *testing.T) {
	entities, proto := loadInspectorFixture(t, 3)
	out := t.TempDir()
	if err := generateGoInspectorSchema(out, "github.com/demiurgos-hub/golem-engine/golem", "generated", entities, proto); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(out, "inspector_schema.go"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	for _, want := range []string{
		`import "github.com/demiurgos-hub/golem-engine/golem/inspector/wire"`,
		"func InspectorSchema() *wire.Schema {",
		`{Name: "gold", Tag: 7, Bit: 4, Sync: "tick", OwnerOnly: true, Type: "uint32", Role: "variable"}`,
		`{Name: "stats", Tag: 9, Bit: 6, Sync: "tick", Kind: wire.KindMap, Type: "int32", KeyType: "string", Role: "variable"}`,
		`{Name: "Item", Fields: []wire.Field{`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("inspector_schema.go missing %q\n%s", want, src)
		}
	}
}
