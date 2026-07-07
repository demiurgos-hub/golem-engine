package tiled_test

import (
	"encoding/json"
	"testing"

	"github.com/demiurgos-hub/golem-engine/golem/tiled"
)

func tiledJSON(m map[string]any) []byte {
	b, _ := json.Marshal(m)
	return b
}

func TestParse_basicMap(t *testing.T) {
	data := tiledJSON(map[string]any{
		"width": 30, "height": 20,
		"tilewidth": 16, "tileheight": 16,
		"layers":   []any{},
		"tilesets": []any{},
	})
	m, err := tiled.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if m.Width != 30 || m.Height != 20 {
		t.Errorf("got %dx%d, want 30x20", m.Width, m.Height)
	}
	if m.TileWidth != 16 || m.TileHeight != 16 {
		t.Errorf("got tilesize %dx%d, want 16x16", m.TileWidth, m.TileHeight)
	}
}

func TestParse_invalidJSON(t *testing.T) {
	_, err := tiled.Parse([]byte("{bad json"))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestMap_LayerByName_found(t *testing.T) {
	data := tiledJSON(map[string]any{
		"layers": []any{
			map[string]any{"id": 1, "name": "Collision", "type": "tilelayer"},
			map[string]any{"id": 2, "name": "Objects", "type": "objectgroup"},
		},
	})
	m, _ := tiled.Parse(data)
	l := m.LayerByName("Collision")
	if l == nil {
		t.Fatal("LayerByName returned nil for existing layer")
	}
	if l.Type != "tilelayer" {
		t.Errorf("got type %q, want tilelayer", l.Type)
	}
}

func TestMap_LayerByName_notFound(t *testing.T) {
	data := tiledJSON(map[string]any{"layers": []any{}})
	m, _ := tiled.Parse(data)
	if m.LayerByName("Missing") != nil {
		t.Error("expected nil for missing layer")
	}
}

func TestMap_PropertyByName(t *testing.T) {
	data := tiledJSON(map[string]any{
		"properties": []any{
			map[string]any{"name": "gravity", "type": "float", "value": 9.8},
			map[string]any{"name": "zone_name", "type": "string", "value": "Plains"},
			map[string]any{"name": "pvp_enabled", "type": "bool", "value": true},
		},
	})
	m, _ := tiled.Parse(data)

	p, ok := m.PropertyByName("gravity")
	if !ok {
		t.Fatal("expected gravity property")
	}
	if got := p.FloatValue(); got != 9.8 {
		t.Errorf("gravity = %v, want 9.8", got)
	}

	if p2, ok := m.PropertyByName("zone_name"); !ok || p2.StringValue() != "Plains" {
		t.Errorf("zone_name = %v ok=%v, want Plains true", p2, ok)
	}

	if p3, ok := m.PropertyByName("pvp_enabled"); !ok || !p3.BoolValue() {
		t.Errorf("pvp_enabled = %v ok=%v, want true true", p3, ok)
	}

	if _, ok := m.PropertyByName("missing"); ok {
		t.Error("expected not found for missing property")
	}
}

func TestProperty_IntValue(t *testing.T) {
	data := tiledJSON(map[string]any{
		"properties": []any{
			map[string]any{"name": "count", "type": "int", "value": float64(42)},
		},
	})
	m, _ := tiled.Parse(data)
	p, _ := m.PropertyByName("count")
	if p.IntValue() != 42 {
		t.Errorf("IntValue = %d, want 42", p.IntValue())
	}
}
