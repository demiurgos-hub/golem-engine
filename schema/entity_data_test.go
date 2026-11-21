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

func TestCheckReservedGeneratedNames(t *testing.T) {
	cases := []struct {
		name    string
		vars    []string
		dims    int
		wantErr string
	}{
		{name: "snake server", vars: []string{"server"}, wantErr: `var "server" generates getter Server, which clashes with generated method Server`},
		{name: "snake bind_server", vars: []string{"bind_server"}, wantErr: `var "bind_server" generates getter BindServer, which clashes with generated method BindServer`},
		{name: "literal Server", vars: []string{"Server"}, wantErr: `var "Server" generates getter Server, which clashes with generated method Server`},
		{name: "literal BindServer", vars: []string{"BindServer"}, wantErr: `var "BindServer" generates getter BindServer, which clashes with generated method BindServer`},
		{name: "camel bindServer", vars: []string{"bindServer"}, wantErr: `var "bindServer" generates getter BindServer, which clashes with generated method BindServer`},
		{name: "bump_state_revision", vars: []string{"bump_state_revision"}, wantErr: `var "bump_state_revision" generates getter BumpStateRevision, which clashes with generated method BumpStateRevision`},
		{name: "state_revision", vars: []string{"state_revision"}, wantErr: `var "state_revision" generates getter StateRevision, which clashes with generated method StateRevision`},
		{name: "type_name", vars: []string{"type_name"}, wantErr: `var "type_name" generates getter TypeName, which clashes with generated method TypeName`},
		{name: "full_update", vars: []string{"full_update"}, wantErr: `var "full_update" generates getter FullUpdate, which clashes with generated method FullUpdate`},
		{name: "setter position", vars: []string{"position"}, wantErr: `var "position" generates getter Position, which clashes with generated method Position`},
		{name: "setter only", vars: []string{"physics_rotation"}, wantErr: `var "physics_rotation" generates setter SetPhysicsRotation, which clashes with generated method SetPhysicsRotation`},
		{name: "3D variant method in 2D", vars: []string{"position3_d"}, wantErr: `var "position3_d" generates getter Position3D, which clashes with generated method Position3D`},
		{name: "entity-specific method", vars: []string{"get_synced_player"}, wantErr: `var "get_synced_player" generates getter GetSyncedPlayer, which clashes with generated method GetSyncedPlayer`},
		{name: "C# type name", vars: []string{"player_state"}, wantErr: `var "player_state" generates getter PlayerState, which clashes with generated type name PlayerState`},
		{name: "message field", vars: []string{"entity_id"}, wantErr: `var "entity_id" generates getter EntityId, which clashes with generated message field EntityId`},
		{name: "message method", vars: []string{"marshal"}, wantErr: `var "marshal" generates getter Marshal, which clashes with generated message method Marshal`},
		{name: "client method", vars: []string{"apply_delta"}, wantErr: `var "apply_delta" generates getter ApplyDelta, which clashes with generated method ApplyDelta`},
		{name: "struct field dirty", vars: []string{"dirty"}, wantErr: `var "dirty" generates field dirty, which clashes with generated struct field dirty`},
		{name: "struct field mu", vars: []string{"mu"}, wantErr: `var "mu" generates field mu, which clashes with generated struct field mu`},
		{name: "struct field last_flush_mask", vars: []string{"last_flush_mask"}, wantErr: `var "last_flush_mask" generates getter LastFlushMask, which clashes with generated method LastFlushMask`},
		{name: "TS constructor", vars: []string{"constructor"}, wantErr: `var "constructor" generates field constructor, which clashes with generated member constructor`},
		{name: "posZ in 3D", vars: []string{"posZ"}, dims: 3, wantErr: `var "posZ" generates getter PosZ, which clashes with generated member PosZ`},
		{name: "var getter vs var setter", vars: []string{"hp", "set_hp"}, wantErr: `var "set_hp" generates getter SetHp, which clashes with setter SetHp of var "hp"`},
		{name: "var case duplicate", vars: []string{"max_hp", "maxHp"}, wantErr: `var "max_hp" generates getter MaxHp, which clashes with getter MaxHp of var "maxHp"`},
		{name: "allowed health", vars: []string{"health"}},
		{name: "allowed server_id", vars: []string{"server_id"}},
		{name: "allowed id", vars: []string{"id"}},
		{name: "allowed revision_count", vars: []string{"revision_count"}},
		{name: "allowed posZ in 2D", vars: []string{"posZ"}},
		{name: "allowed several", vars: []string{"hp", "max_hp", "speed"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			vars := make(map[string]SchemaVarDef, len(tc.vars))
			for i, name := range tc.vars {
				vars[name] = SchemaVarDef{Type: "int32", Tag: i + 1}
			}
			dims := tc.dims
			if dims == 0 {
				dims = 2
			}
			err := checkReservedGeneratedNames("Player", dims, vars)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if want := `entity "Player": ` + tc.wantErr; err == nil || err.Error() != want {
				t.Fatalf("error = %v, want %q", err, want)
			}
		})
	}
}
