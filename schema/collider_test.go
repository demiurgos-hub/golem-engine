package schema

import (
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func floatPtr(v float64) *float64 { return &v }

func TestResolveCollider_2DShapes(t *testing.T) {
	aabb, err := ResolveCollider("Player", &ColliderDef{
		AABB:  []float64{1.5, 2.5},
		Layer: "Player",
	}, 2)
	if err != nil {
		t.Fatalf("aabb: %v", err)
	}
	if !aabb.IsAABB() || aabb.W != 1.5 || aabb.H != 2.5 || aabb.Layer != "Player" || aabb.Trigger {
		t.Fatalf("unexpected aabb: %+v", aabb)
	}

	circle, err := ResolveCollider("Coin", &ColliderDef{
		Circle:  floatPtr(0.5),
		Layer:   "Pickup",
		Trigger: true,
	}, 2)
	if err != nil {
		t.Fatalf("circle: %v", err)
	}
	if !circle.IsCircle() || circle.R != 0.5 || !circle.Trigger {
		t.Fatalf("unexpected circle: %+v", circle)
	}
}

func TestResolveCollider_3DShapes(t *testing.T) {
	box, err := ResolveCollider("Crate", &ColliderDef{
		AABB3D: []float64{1, 2, 3},
		Layer:  "Prop",
	}, 3)
	if err != nil {
		t.Fatalf("aabb3d: %v", err)
	}
	if !box.IsAABB3D() || box.W != 1 || box.H != 2 || box.D != 3 {
		t.Fatalf("unexpected aabb3d: %+v", box)
	}

	sphere, err := ResolveCollider("Ball", &ColliderDef{
		Sphere: floatPtr(4),
		Layer:  "Ball",
	}, 3)
	if err != nil {
		t.Fatalf("sphere: %v", err)
	}
	if !sphere.IsSphere() || sphere.R != 4 {
		t.Fatalf("unexpected sphere: %+v", sphere)
	}
}

func TestResolveCollider_rejectsDimensionMismatches(t *testing.T) {
	if _, err := ResolveCollider("P", &ColliderDef{AABB: []float64{1, 1}, Layer: "A"}, 3); err == nil {
		t.Fatal("expected 2D shape rejected in 3D")
	}
	if _, err := ResolveCollider("P", &ColliderDef{Sphere: floatPtr(1), Layer: "A"}, 2); err == nil {
		t.Fatal("expected 3D shape rejected in 2D")
	}
	if _, err := ResolveCollider("P", &ColliderDef{Circle: floatPtr(1), AABB3D: []float64{1, 1, 1}, Layer: "A"}, 2); err == nil {
		t.Fatal("expected multiple shapes rejected")
	}
}

func TestResolveCollider_rejectsMalformed(t *testing.T) {
	cases := []struct {
		name string
		def  *ColliderDef
		dim  int
		sub  string
	}{
		{"empty layer", &ColliderDef{Circle: floatPtr(1)}, 2, "layer"},
		{"no shape", &ColliderDef{Layer: "A"}, 2, "exactly one shape"},
		{"two 2d shapes", &ColliderDef{AABB: []float64{1, 1}, Circle: floatPtr(1), Layer: "A"}, 2, "exactly one shape"},
		{"bad aabb len", &ColliderDef{AABB: []float64{1}, Layer: "A"}, 2, "aabb"},
		{"bad aabb3d len", &ColliderDef{AABB3D: []float64{1, 2}, Layer: "A"}, 3, "aabb3d"},
		{"non-positive circle", &ColliderDef{Circle: floatPtr(0), Layer: "A"}, 2, "positive"},
		{"nan sphere", &ColliderDef{Sphere: floatPtr(math.NaN()), Layer: "A"}, 3, "positive"},
		{"inf width", &ColliderDef{AABB: []float64{math.Inf(1), 1}, Layer: "A"}, 2, "positive"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ResolveCollider("E", tc.def, tc.dim)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tc.sub)) {
				t.Fatalf("error %q should mention %q", err.Error(), tc.sub)
			}
		})
	}
}

func TestResolveCollisionConfig_validatesMatrix(t *testing.T) {
	got, err := ResolveCollisionConfig(&CollisionConfig{
		Layers:   []string{"Player", "Monster"},
		Collides: [][]string{{"Player", "Monster"}, {"Player", "Player"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Layers) != 2 || got.Collides[0] != (CollisionPair{A: "Player", B: "Monster"}) {
		t.Fatalf("unexpected: %+v", got)
	}
	// Self-collision pairs are valid and important (same-layer interactions).
	if got.Collides[1] != (CollisionPair{A: "Player", B: "Player"}) {
		t.Fatalf("self-collision pair not retained: %+v", got.Collides)
	}
}

func TestResolveCollisionConfig_rejectsDuplicateUnorderedPairs(t *testing.T) {
	_, err := ResolveCollisionConfig(&CollisionConfig{
		Layers:   []string{"A", "B"},
		Collides: [][]string{{"A", "B"}, {"B", "A"}},
	})
	if err == nil {
		t.Fatal("expected duplicate unordered pair error")
	}
	if !strings.Contains(err.Error(), "duplicate unordered pair") {
		t.Fatalf("error = %q, want duplicate unordered pair", err.Error())
	}

	_, err = ResolveCollisionConfig(&CollisionConfig{
		Layers:   []string{"A"},
		Collides: [][]string{{"A", "A"}, {"A", "A"}},
	})
	if err == nil {
		t.Fatal("expected duplicate self-pair error")
	}
}

func TestResolveCollisionConfig_rejectsMoreThan32Layers(t *testing.T) {
	layers := make([]string, MaxCollisionLayers+1)
	for i := range layers {
		layers[i] = "Layer" + strconv.Itoa(i)
	}
	_, err := ResolveCollisionConfig(&CollisionConfig{Layers: layers})
	if err == nil {
		t.Fatal("expected max layers error")
	}
	if !strings.Contains(err.Error(), "at most 32") {
		t.Fatalf("error = %q, want at most 32", err.Error())
	}
}

func TestResolveCollisionConfig_rejectsMalformed(t *testing.T) {
	cases := []struct {
		name string
		cfg  *CollisionConfig
		sub  string
	}{
		{"empty layers", &CollisionConfig{}, "at least one"},
		{"empty name", &CollisionConfig{Layers: []string{""}}, "non-empty"},
		{"duplicate", &CollisionConfig{Layers: []string{"A", "A"}}, "duplicate"},
		{"bad pair len", &CollisionConfig{Layers: []string{"A"}, Collides: [][]string{{"A"}}}, "pair"},
		{"unknown ref", &CollisionConfig{Layers: []string{"A"}, Collides: [][]string{{"A", "B"}}}, "unknown"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ResolveCollisionConfig(tc.cfg)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tc.sub)) {
				t.Fatalf("error %q should mention %q", err.Error(), tc.sub)
			}
		})
	}
}

func TestLoadConfig_storesResolvedCollision(t *testing.T) {
	dir := t.TempDir()
	yaml := "" +
		"collision:\n  layers: [Player]\n  collides: [[Player, Player]]\n" +
		"proto:\n  out: entities.proto\n"
	if err := os.WriteFile(filepath.Join(dir, "golem.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ResolvedCollision == nil || len(cfg.ResolvedCollision.Layers) != 1 {
		t.Fatalf("ResolvedCollision = %+v", cfg.ResolvedCollision)
	}
	if len(cfg.ResolvedCollision.Collides) != 1 || cfg.ResolvedCollision.Collides[0].A != "Player" {
		t.Fatalf("ResolvedCollision.Collides = %+v", cfg.ResolvedCollision.Collides)
	}
}

func TestValidateEntityColliders(t *testing.T) {
	collision, err := ResolveCollisionConfig(&CollisionConfig{
		Layers:   []string{"Player", "Monster"},
		Collides: [][]string{{"Player", "Monster"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	entities := []EntityData{{
		Name:     "Player",
		Collider: &ColliderData{Kind: ColliderKindCircle, R: 1, Layer: "Player"},
	}}
	if err := ValidateEntityColliders(entities, collision); err != nil {
		t.Fatal(err)
	}
	if err := ValidateEntityColliders(entities, nil); err == nil {
		t.Fatal("collider without collision section must fail")
	}
	if err := ValidateEntityColliders(nil, collision); err != nil {
		t.Fatalf("collision without colliders should be allowed: %v", err)
	}
	bad := []EntityData{{
		Name:     "Ghost",
		Collider: &ColliderData{Kind: ColliderKindCircle, R: 1, Layer: "Missing"},
	}}
	if err := ValidateEntityColliders(bad, collision); err == nil {
		t.Fatal("unknown layer must fail")
	}
}

func TestBuildEntityData_includesCollider(t *testing.T) {
	ed := BuildEntityData(SchemaFile{
		Entity: "Player",
		Collider: &ColliderDef{
			Circle: floatPtr(0.75),
			Layer:  "Player",
		},
		Vars: map[string]SchemaVarDef{
			"health": {Type: "int32", Tag: 1},
		},
	}, 2, nil)
	if ed.Collider == nil || !ed.Collider.IsCircle() || ed.Collider.R != 0.75 {
		t.Fatalf("Collider = %+v", ed.Collider)
	}
}

func TestLoadConfig_collisionSection(t *testing.T) {
	dir := t.TempDir()
	yaml := "" +
		"simulation:\n  dimensions: 2\n" +
		"collision:\n  layers: [Player, Monster]\n  collides: [[Player, Monster]]\n" +
		"proto:\n  out: entities.proto\n"
	if err := os.WriteFile(filepath.Join(dir, "golem.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Collision == nil || len(cfg.Collision.Layers) != 2 {
		t.Fatalf("Collision = %+v", cfg.Collision)
	}
}

func TestLoadConfig_rejectsBadCollision(t *testing.T) {
	dir := t.TempDir()
	yaml := "" +
		"collision:\n  layers: [A, A]\n" +
		"proto:\n  out: entities.proto\n"
	if err := os.WriteFile(filepath.Join(dir, "golem.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(dir); err == nil {
		t.Fatal("expected duplicate layer error")
	}
}
