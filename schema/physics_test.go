package schema

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadPhysicsTagKeepsAuthoredNameAndTag(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "body.yaml"), []byte("entity: Body\nvars:\n  awake:\n    tag: 7\n    type: bool\n    physics: active\n"), 0600); err != nil {
		t.Fatal(err)
	}
	entities, err := LoadSchemas(dir, 3, nil)
	if err != nil {
		t.Fatal(err)
	}
	e := entities[0]
	if len(e.AllVars) != 1 || e.Physics.Active.SnakeName != "awake" || e.Physics.Active.UserTag != 7 || e.Physics.Active.ProtoTag != 11 {
		t.Fatalf("explicit field changed: %+v", e)
	}
}

func physicsTestEntity() EntityData {
	var vars []VarInfo
	roles := []string{"orientation_x", "orientation_y", "orientation_z", "orientation_w", "velocity_x", "velocity_y", "velocity_z", "angular_velocity_x", "angular_velocity_y", "angular_velocity_z", "active"}
	for i, role := range roles {
		kind := "float32"
		if role == "active" {
			kind = "bool"
		}
		vars = append(vars, VarInfo{SnakeName: role, GoName: SnakeToPascal(role), GoType: kind, Sync: "tick", Physics: role, BitIndex: i + 3, OwnerOnly: true})
	}
	return EntityData{Name: "Actor", Dimensions: 3, AllVars: vars, TickVars: append([]VarInfo(nil), vars...)}
}

func TestPhysicsBindings(t *testing.T) {
	e := physicsTestEntity()
	b, err := ResolvePhysicsBindings(e)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Rotation) != 4 || len(b.LinearVelocity) != 3 || len(b.AngularVelocity) != 3 || b.Active == nil || !b.Rotation[0].OwnerOnly || b.Rotation[0].BitIndex != 3 {
		t.Fatalf("bad binding: %+v", b)
	}
	e.AllVars = nil
	if b, err := ResolvePhysicsBindings(e); b != nil || err != nil {
		t.Fatalf("ordinary entity: %v %v", b, err)
	}
}

func TestPhysicsBindingRejections(t *testing.T) {
	cases := []struct {
		name, want string
		change     func(*EntityData)
	}{
		{"dimension", "dimensions", func(e *EntityData) { e.Dimensions = 2 }},
		{"legacy", "legacy collider", func(e *EntityData) { e.Collider = &ColliderData{} }},
		{"type", "requires float32", func(e *EntityData) { e.AllVars[0].GoType = "float64" }},
		{"once", "sync: tick", func(e *EntityData) { e.AllVars[0].Sync = "once" }},
		{"unknown", "unknown physics", func(e *EntityData) { e.AllVars[0].Physics = "rotation" }},
		{"duplicate", "duplicate physics", func(e *EntityData) { e.AllVars[1].Physics = "orientation_x" }},
		{"partial", "every component", func(e *EntityData) { e.AllVars[0].Physics = "" }},
		{"velocity_pair", "both linear and angular", func(e *EntityData) {
			for i := 7; i < 10; i++ {
				e.AllVars[i].Physics = ""
			}
		}},
		{"reserved", "reserved metadata", func(e *EntityData) { e.AllVars[0].GoName = "PosX" }},
		{"conflict", "conflicts", func(e *EntityData) { e.AllVars[0].GoName = "PhysicsRotation" }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := physicsTestEntity()
			c.change(&e)
			_, err := ResolvePhysicsBindings(e)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("want %q: %v", c.want, err)
			}
		})
	}
}
