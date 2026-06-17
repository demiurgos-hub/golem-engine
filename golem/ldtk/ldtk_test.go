package ldtk_test

import (
	"encoding/json"
	"testing"

	"golem-engine/golem/ldtk"
)

func ldtkJSON(m map[string]any) []byte {
	b, _ := json.Marshal(m)
	return b
}

func TestParse_basicProject(t *testing.T) {
	data := ldtkJSON(map[string]any{
		"jsonVersion": "1.5.3",
		"bgColor":     "#1a1a2e",
		"levels":      []any{},
		"worlds":      []any{},
		"defs":        map[string]any{},
	})
	p, err := ldtk.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if p.JSONVersion != "1.5.3" {
		t.Errorf("got version %q, want 1.5.3", p.JSONVersion)
	}
}

func TestParse_invalidJSON(t *testing.T) {
	_, err := ldtk.Parse([]byte("{bad"))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestProject_AllLevels_singleWorld(t *testing.T) {
	data := ldtkJSON(map[string]any{
		"levels": []any{
			map[string]any{"identifier": "Level_0", "uid": 0},
			map[string]any{"identifier": "Level_1", "uid": 1},
		},
		"worlds": []any{},
	})
	p, _ := ldtk.Parse(data)
	levels := p.AllLevels()
	if len(levels) != 2 {
		t.Fatalf("got %d levels, want 2", len(levels))
	}
}

func TestProject_AllLevels_multiWorld(t *testing.T) {
	data := ldtkJSON(map[string]any{
		"levels": []any{},
		"worlds": []any{
			map[string]any{
				"identifier": "World_A",
				"levels": []any{
					map[string]any{"identifier": "A_0", "uid": 10},
				},
			},
			map[string]any{
				"identifier": "World_B",
				"levels": []any{
					map[string]any{"identifier": "B_0", "uid": 20},
					map[string]any{"identifier": "B_1", "uid": 21},
				},
			},
		},
	})
	p, _ := ldtk.Parse(data)
	levels := p.AllLevels()
	if len(levels) != 3 {
		t.Fatalf("got %d levels, want 3", len(levels))
	}
}

func TestProject_LevelByIdentifier(t *testing.T) {
	data := ldtkJSON(map[string]any{
		"levels": []any{
			map[string]any{"identifier": "Hub", "uid": 5},
		},
		"worlds": []any{},
	})
	p, _ := ldtk.Parse(data)

	l := p.LevelByIdentifier("Hub")
	if l == nil {
		t.Fatal("LevelByIdentifier returned nil for existing level")
	}
	if l.UID != 5 {
		t.Errorf("uid = %d, want 5", l.UID)
	}

	if p.LevelByIdentifier("Missing") != nil {
		t.Error("expected nil for missing level identifier")
	}
}

func TestLevel_LayerByIdentifier(t *testing.T) {
	data := ldtkJSON(map[string]any{
		"levels": []any{
			map[string]any{
				"identifier": "Level_0",
				"uid":        0,
				"layerInstances": []any{
					map[string]any{"__identifier": "Tiles", "__type": "Tiles"},
					map[string]any{"__identifier": "Entities", "__type": "Entities"},
				},
			},
		},
		"worlds": []any{},
	})
	p, _ := ldtk.Parse(data)
	l := p.LevelByIdentifier("Level_0")
	layer := l.LayerByIdentifier("Entities")
	if layer == nil {
		t.Fatal("LayerByIdentifier returned nil for existing layer")
	}
	if layer.Type != "Entities" {
		t.Errorf("type = %q, want Entities", layer.Type)
	}
	if l.LayerByIdentifier("Missing") != nil {
		t.Error("expected nil for missing layer")
	}
}

func TestLevel_FieldByIdentifier(t *testing.T) {
	data := ldtkJSON(map[string]any{
		"levels": []any{
			map[string]any{
				"identifier": "Level_0",
				"uid":        0,
				"fieldInstances": []any{
					map[string]any{"__identifier": "gravity", "__type": "Float", "__value": 9.8},
					map[string]any{"__identifier": "zone_name", "__type": "String", "__value": "Overworld"},
					map[string]any{"__identifier": "pvp_enabled", "__type": "Bool", "__value": true},
				},
			},
		},
		"worlds": []any{},
	})
	p, _ := ldtk.Parse(data)
	l := p.LevelByIdentifier("Level_0")

	f := l.FieldByIdentifier("gravity")
	if f == nil {
		t.Fatal("expected gravity field")
	}
	if f.FloatValue() != 9.8 {
		t.Errorf("gravity = %v, want 9.8", f.FloatValue())
	}

	if f2 := l.FieldByIdentifier("zone_name"); f2 == nil || f2.StringValue() != "Overworld" {
		t.Errorf("zone_name unexpected: %v", f2)
	}

	if f3 := l.FieldByIdentifier("pvp_enabled"); f3 == nil || !f3.BoolValue() {
		t.Errorf("pvp_enabled unexpected: %v", f3)
	}

	if l.FieldByIdentifier("missing") != nil {
		t.Error("expected nil for missing field")
	}
}

func TestFieldInstance_IntValue(t *testing.T) {
	data := ldtkJSON(map[string]any{
		"levels": []any{
			map[string]any{
				"identifier":     "Level_0",
				"uid":            0,
				"fieldInstances": []any{map[string]any{"__identifier": "count", "__type": "Int", "__value": float64(7)}},
			},
		},
		"worlds": []any{},
	})
	p, _ := ldtk.Parse(data)
	f := p.LevelByIdentifier("Level_0").FieldByIdentifier("count")
	if f.IntValue() != 7 {
		t.Errorf("IntValue = %d, want 7", f.IntValue())
	}
}
