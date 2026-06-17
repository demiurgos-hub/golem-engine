package schema

import (
	"reflect"
	"testing"
)

func TestBuildEntityData_collectsCustomTypeNames(t *testing.T) {
	customTypes := map[string]CustomTypeData{
		"Buff": BuildCustomTypeData(TypeSchemaFile{
			Type: "Buff",
			Fields: map[string]TypeFieldDef{
				"label": {Type: "string", Tag: 1},
			},
		}),
		"Item": BuildCustomTypeData(TypeSchemaFile{
			Type: "Item",
			Fields: map[string]TypeFieldDef{
				"id": {Type: "int32", Tag: 1},
			},
		}),
	}

	ed := BuildEntityData(SchemaFile{
		Entity: "Player",
		Vars: map[string]SchemaVarDef{
			"active_buffs": {Type: "list<Buff>", Tag: 1},
			"equipment":    {Type: "dict<string, Item>", Tag: 2},
			"health":       {Type: "int32", Tag: 3},
			"inventory":    {Type: "list<Item>", Tag: 4},
		},
	}, 2, customTypes)

	wantCustomTypeNames := []string{"Buff", "Item"}
	if !reflect.DeepEqual(ed.CustomTypeNames, wantCustomTypeNames) {
		t.Fatalf("CustomTypeNames = %v, want %v", ed.CustomTypeNames, wantCustomTypeNames)
	}

	var usedTypeNames []string
	for _, ct := range ed.UsedCustomTypes {
		usedTypeNames = append(usedTypeNames, ct.Name)
	}
	if !reflect.DeepEqual(usedTypeNames, wantCustomTypeNames) {
		t.Fatalf("UsedCustomTypes names = %v, want %v", usedTypeNames, wantCustomTypeNames)
	}
}

func TestBuildEntityData_3DOffsetsEntityVarTagsAndBits(t *testing.T) {
	ed := BuildEntityData(SchemaFile{
		Entity: "Projectile",
		Vars: map[string]SchemaVarDef{
			"health": {Type: "int32", Tag: 1},
		},
	}, 3, nil)

	if !ed.Is3D || ed.Dimensions != 3 {
		t.Fatalf("3D metadata not set: dimensions=%d is3D=%v", ed.Dimensions, ed.Is3D)
	}
	if got, want := ed.AllVars[0].ProtoTag, 5; got != want {
		t.Fatalf("ProtoTag = %d, want %d", got, want)
	}
	if got, want := ed.TickVars[0].BitIndex, 3; got != want {
		t.Fatalf("BitIndex = %d, want %d", got, want)
	}
}
