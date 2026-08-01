package schema

import (
	"fmt"
	"math"
)

// ColliderDef is the YAML model for an optional per-entity collider block.
// Exactly one shape field must be set, matching simulation.dimensions:
// 2D: aabb or circle; 3D: aabb3d or sphere.
type ColliderDef struct {
	AABB    []float64 `yaml:"aabb,omitempty"`
	Circle  *float64  `yaml:"circle,omitempty"`
	AABB3D  []float64 `yaml:"aabb3d,omitempty"`
	Sphere  *float64  `yaml:"sphere,omitempty"`
	Layer   string    `yaml:"layer"`
	Trigger bool      `yaml:"trigger"`
}

// ColliderKind identifies which shape a resolved collider uses.
type ColliderKind string

const (
	ColliderKindAABB   ColliderKind = "aabb"
	ColliderKindCircle ColliderKind = "circle"
	ColliderKindAABB3D ColliderKind = "aabb3d"
	ColliderKindSphere ColliderKind = "sphere"
)

// ColliderData is the template-ready collider resolved from entity YAML.
// It is bake-time / runtime metadata only — not part of the wire protocol.
type ColliderData struct {
	Kind    ColliderKind
	Layer   string
	Trigger bool
	W       float64 // aabb / aabb3d width
	H       float64 // aabb / aabb3d height
	D       float64 // aabb3d depth
	R       float64 // circle / sphere radius
}

// IsAABB reports whether the collider is a 2D axis-aligned box.
func (c *ColliderData) IsAABB() bool { return c != nil && c.Kind == ColliderKindAABB }

// IsCircle reports whether the collider is a 2D circle.
func (c *ColliderData) IsCircle() bool { return c != nil && c.Kind == ColliderKindCircle }

// IsAABB3D reports whether the collider is a 3D axis-aligned box.
func (c *ColliderData) IsAABB3D() bool { return c != nil && c.Kind == ColliderKindAABB3D }

// IsSphere reports whether the collider is a 3D sphere.
func (c *ColliderData) IsSphere() bool { return c != nil && c.Kind == ColliderKindSphere }

// MaxCollisionLayers is the maximum number of named layers (matches the
// CollisionLayers / CollisionLayers3D bit index limit).
const MaxCollisionLayers = 32

// CollisionPair is one symmetric collides entry from golem.yaml.
// Self-pairs (A == B) are valid and important — e.g. Player/Player so players
// collide with each other. SetCollides treats every pair as symmetric.
type CollisionPair struct {
	A string
	B string
}

// CollisionData is the template-ready named layer matrix from golem.yaml.
// Present only when the optional top-level collision: section is set.
type CollisionData struct {
	Layers   []string
	Collides []CollisionPair
}

// ResolveCollider validates and converts a YAML collider for the given
// simulation dimensions. Returns (nil, nil) when def is nil.
func ResolveCollider(entity string, def *ColliderDef, dimensions int) (*ColliderData, error) {
	if def == nil {
		return nil, nil
	}
	if dimensions != 2 && dimensions != 3 {
		return nil, fmt.Errorf("entity %q: dimensions must be 2 or 3, got %d", entity, dimensions)
	}
	if def.Layer == "" {
		return nil, fmt.Errorf("entity %q: collider.layer must be non-empty", entity)
	}

	n2D := 0
	n3D := 0
	if len(def.AABB) > 0 {
		n2D++
	}
	if def.Circle != nil {
		n2D++
	}
	if len(def.AABB3D) > 0 {
		n3D++
	}
	if def.Sphere != nil {
		n3D++
	}
	total := n2D + n3D
	if total == 0 {
		return nil, fmt.Errorf("entity %q: collider must declare exactly one shape", entity)
	}
	if total > 1 {
		return nil, fmt.Errorf("entity %q: collider must declare exactly one shape", entity)
	}

	out := &ColliderData{Layer: def.Layer, Trigger: def.Trigger}

	if dimensions == 2 {
		if n3D > 0 {
			return nil, fmt.Errorf("entity %q: collider uses a 3D shape but simulation.dimensions is 2", entity)
		}
		if len(def.AABB) > 0 {
			if len(def.AABB) != 2 {
				return nil, fmt.Errorf("entity %q: collider.aabb must be [width, height]", entity)
			}
			if err := requirePositiveFinite(entity, "collider.aabb[0] (width)", def.AABB[0]); err != nil {
				return nil, err
			}
			if err := requirePositiveFinite(entity, "collider.aabb[1] (height)", def.AABB[1]); err != nil {
				return nil, err
			}
			out.Kind = ColliderKindAABB
			out.W = def.AABB[0]
			out.H = def.AABB[1]
			return out, nil
		}
		if err := requirePositiveFinite(entity, "collider.circle", *def.Circle); err != nil {
			return nil, err
		}
		out.Kind = ColliderKindCircle
		out.R = *def.Circle
		return out, nil
	}

	// dimensions == 3
	if n2D > 0 {
		return nil, fmt.Errorf("entity %q: collider uses a 2D shape but simulation.dimensions is 3", entity)
	}
	if len(def.AABB3D) > 0 {
		if len(def.AABB3D) != 3 {
			return nil, fmt.Errorf("entity %q: collider.aabb3d must be [width, height, depth]", entity)
		}
		if err := requirePositiveFinite(entity, "collider.aabb3d[0] (width)", def.AABB3D[0]); err != nil {
			return nil, err
		}
		if err := requirePositiveFinite(entity, "collider.aabb3d[1] (height)", def.AABB3D[1]); err != nil {
			return nil, err
		}
		if err := requirePositiveFinite(entity, "collider.aabb3d[2] (depth)", def.AABB3D[2]); err != nil {
			return nil, err
		}
		out.Kind = ColliderKindAABB3D
		out.W = def.AABB3D[0]
		out.H = def.AABB3D[1]
		out.D = def.AABB3D[2]
		return out, nil
	}
	if err := requirePositiveFinite(entity, "collider.sphere", *def.Sphere); err != nil {
		return nil, err
	}
	out.Kind = ColliderKindSphere
	out.R = *def.Sphere
	return out, nil
}

func requirePositiveFinite(entity, field string, v float64) error {
	if math.IsNaN(v) || math.IsInf(v, 0) || v <= 0 {
		return fmt.Errorf("entity %q: %s must be a positive finite number, got %v", entity, field, v)
	}
	return nil
}

// ResolveCollisionConfig validates the optional golem.yaml collision section.
// Returns (nil, nil) when cfg is nil. LoadConfig stores the result on
// Config.ResolvedCollision; Bake reads that field rather than resolving again.
//
// Self-collision pairs such as [Player, Player] are retained — they enable
// same-layer interactions (e.g. players blocking each other). Duplicate
// unordered pairs are rejected, including listing both [A, B] and [B, A].
func ResolveCollisionConfig(cfg *CollisionConfig) (*CollisionData, error) {
	if cfg == nil {
		return nil, nil
	}
	if len(cfg.Layers) == 0 {
		return nil, fmt.Errorf("collision.layers must declare at least one layer")
	}
	if len(cfg.Layers) > MaxCollisionLayers {
		return nil, fmt.Errorf("collision.layers: at most %d layers, got %d", MaxCollisionLayers, len(cfg.Layers))
	}
	seen := make(map[string]struct{}, len(cfg.Layers))
	layers := make([]string, 0, len(cfg.Layers))
	for i, name := range cfg.Layers {
		if name == "" {
			return nil, fmt.Errorf("collision.layers[%d] must be non-empty", i)
		}
		if _, ok := seen[name]; ok {
			return nil, fmt.Errorf("collision.layers: duplicate layer %q", name)
		}
		seen[name] = struct{}{}
		layers = append(layers, name)
	}

	seenPairs := make(map[string]struct{}, len(cfg.Collides))
	pairs := make([]CollisionPair, 0, len(cfg.Collides))
	for i, pair := range cfg.Collides {
		if len(pair) != 2 {
			return nil, fmt.Errorf("collision.collides[%d] must be a pair of two layer names", i)
		}
		a, b := pair[0], pair[1]
		if a == "" || b == "" {
			return nil, fmt.Errorf("collision.collides[%d] layer names must be non-empty", i)
		}
		if _, ok := seen[a]; !ok {
			return nil, fmt.Errorf("collision.collides[%d]: unknown layer %q", i, a)
		}
		if _, ok := seen[b]; !ok {
			return nil, fmt.Errorf("collision.collides[%d]: unknown layer %q", i, b)
		}
		key := unorderedPairKey(a, b)
		if _, ok := seenPairs[key]; ok {
			return nil, fmt.Errorf("collision.collides: duplicate unordered pair %q/%q", a, b)
		}
		seenPairs[key] = struct{}{}
		pairs = append(pairs, CollisionPair{A: a, B: b})
	}
	return &CollisionData{Layers: layers, Collides: pairs}, nil
}

// unorderedPairKey canonicalizes a collides entry so [A,B] and [B,A] collide.
// Self-pairs (A == B) use a single name twice and remain unique as one entry.
func unorderedPairKey(a, b string) string {
	if a > b {
		a, b = b, a
	}
	return a + "\x00" + b
}

// ValidateEntityColliders ensures entity colliders reference declared layers and
// that a collider cannot appear without a project collision: section.
// A collision section with no entity colliders is allowed.
func ValidateEntityColliders(entities []EntityData, collision *CollisionData) error {
	hasCollider := false
	for _, e := range entities {
		if e.Collider != nil {
			hasCollider = true
			break
		}
	}
	if hasCollider && collision == nil {
		return fmt.Errorf("entity collider declared but golem.yaml has no collision: section")
	}
	if collision == nil {
		return nil
	}
	layers := make(map[string]struct{}, len(collision.Layers))
	for _, name := range collision.Layers {
		layers[name] = struct{}{}
	}
	for _, e := range entities {
		if e.Collider == nil {
			continue
		}
		if _, ok := layers[e.Collider.Layer]; !ok {
			return fmt.Errorf("entity %q: collider.layer %q is not declared in collision.layers", e.Name, e.Collider.Layer)
		}
	}
	return nil
}
