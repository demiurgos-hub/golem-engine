package schema

import (
	"fmt"
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

func TestBuildEntityData_ownerVisibility(t *testing.T) {
	ed := BuildEntityData(SchemaFile{
		Entity: "Player",
		Vars: map[string]SchemaVarDef{
			"health":  {Type: "int32", Tag: 1},
			"secret":  {Type: "int32", Tag: 2, Visibility: "owner"},
			"public":  {Type: "string", Tag: 3, Visibility: "all"},
			"default": {Type: "int32", Tag: 4},
			"token":   {Type: "string", Tag: 5, Sync: "once", Visibility: "owner"},
		},
	}, 2, nil)

	if !ed.HasOwnerVars() {
		t.Fatal("HasOwnerVars = false, want true")
	}
	byName := map[string]VarInfo{}
	for _, v := range ed.AllVars {
		byName[v.SnakeName] = v
	}
	for _, name := range []string{"health", "public", "default"} {
		if byName[name].OwnerOnly {
			t.Fatalf("%s: OwnerOnly = true, want false", name)
		}
	}
	for _, name := range []string{"secret", "token"} {
		if !byName[name].OwnerOnly {
			t.Fatalf("%s: OwnerOnly = false, want true", name)
		}
	}
}

func TestNormalizeVarVisibility(t *testing.T) {
	cases := []struct {
		vis       string
		ownerOnly bool
		wantErr   bool
	}{
		{vis: "", ownerOnly: false},
		{vis: "all", ownerOnly: false},
		{vis: "owner", ownerOnly: true},
		{vis: "party", wantErr: true},
		{vis: "private", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.vis, func(t *testing.T) {
			got, err := normalizeVarVisibility("Player", "secret", tc.vis)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.ownerOnly {
				t.Fatalf("ownerOnly = %v, want %v", got, tc.ownerOnly)
			}
		})
	}
}

func TestCheckReservedGeneratedMethodVars(t *testing.T) {
	cases := []struct {
		name    string
		varName string
		wantErr bool
	}{
		{name: "snake server", varName: "server", wantErr: true},
		{name: "snake bind_server", varName: "bind_server", wantErr: true},
		{name: "literal Server", varName: "Server", wantErr: true},
		{name: "literal BindServer", varName: "BindServer", wantErr: true},
		{name: "camel bindServer", varName: "bindServer", wantErr: true},
		{name: "allowed health", varName: "health", wantErr: false},
		{name: "allowed server_id", varName: "server_id", wantErr: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkReservedGeneratedMethodVars("Player", map[string]SchemaVarDef{
				tc.varName: {Type: "int32", Tag: 1},
			})
			if tc.wantErr && err == nil {
				t.Fatal("expected rejection")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.wantErr {
				want := fmt.Sprintf(`entity "Player": var name %q is reserved because it collides with generated Server/BindServer methods`, tc.varName)
				if err.Error() != want {
					t.Fatalf("error = %q, want %q", err.Error(), want)
				}
			}
		})
	}
}
