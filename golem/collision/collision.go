package collision

// Vec2 is a 2D vector used in collision contacts.
// Named fields are more ergonomic than [2]float64 for public API callers.
type Vec2 struct{ X, Y float64 }

// Shape is a sealed interface for collision shapes.
// Only Circle and AABB (defined in this package) satisfy it.
type Shape interface{ shapeTag() }

// Circle is a circular collision shape centered on the entity's position.
type Circle struct{ R float64 }

// AABB is an axis-aligned bounding box centered on the entity's position.
type AABB struct{ W, H float64 }

func (Circle) shapeTag() {}
func (AABB) shapeTag()   {}

// Contact describes a detected collision between two entities.
// Normal is the push direction for entity A (unit vector); Depth is the penetration distance.
type Contact struct {
	A, B   int64
	Normal Vec2
	Depth  float64
}

// SpatialQuery is an optional interface backends may implement to support
// one-shot overlap queries without registering a persistent shape.
// layerMask matches entities where (entity.layer & layerMask) != 0.
type SpatialQuery interface {
	// OverlapBox returns the IDs of all registered entities whose shapes
	// overlap the axis-aligned box centred at (cx, cy) with half-extents (hw, hh).
	OverlapBox(cx, cy, hw, hh float64, layerMask uint32) []int64

	// OverlapCircle returns the IDs of all registered entities whose shapes
	// overlap the circle centred at (cx, cy) with the given radius.
	OverlapCircle(cx, cy, radius float64, layerMask uint32) []int64
}

// RaycastHit is the result of a single cast query that intersected a registered entity.
// Fraction is the normalised hit distance along the cast segment: 0 at the origin, 1 at the end.
type RaycastHit struct {
	EntityID int64
	Point    Vec2    // world-space contact point
	Normal   Vec2    // surface normal pointing away from the hit shape, toward the cast origin
	Fraction float64 // distance along the segment, in [0,1]
}

// CastQuery is an optional interface backends may implement to support segment
// and swept-shape queries. All *All variants return hits sorted by Fraction
// (closest first). layerMask uses the same bitmask convention as SpatialQuery.
type CastQuery interface {
	// Raycast returns the first entity intersected by the segment (x1,y1)→(x2,y2).
	Raycast(x1, y1, x2, y2 float64, layerMask uint32) (RaycastHit, bool)
	// RaycastAll returns all entities intersected by the segment, sorted by Fraction.
	RaycastAll(x1, y1, x2, y2 float64, layerMask uint32) []RaycastHit

	// BoxCast sweeps an AABB with half-extents (hw, hh) from (ox, oy) by the
	// displacement (dx, dy) and returns the first entity hit.
	BoxCast(ox, oy, hw, hh, dx, dy float64, layerMask uint32) (RaycastHit, bool)
	// BoxCastAll returns all entities hit by the box sweep, sorted by Fraction.
	BoxCastAll(ox, oy, hw, hh, dx, dy float64, layerMask uint32) []RaycastHit

	// CircleCast sweeps a circle with the given radius from (ox, oy) by the
	// displacement (dx, dy) and returns the first entity hit.
	CircleCast(ox, oy, radius, dx, dy float64, layerMask uint32) (RaycastHit, bool)
	// CircleCastAll returns all entities hit by the circle sweep, sorted by Fraction.
	CircleCastAll(ox, oy, radius, dx, dy float64, layerMask uint32) []RaycastHit
}

// Backend is implemented by collision backends (built-in, resolv, cp, etc.).
// All methods are called from the game tick goroutine; implementations need not be thread-safe.
type Backend interface {
	// Add registers a collision shape for the given entity.
	// layer is a bitmask of the entity's own layer(s); mask is the set of layers it collides with.
	// When trigger is true the shape reports overlaps but does not push entities apart.
	Add(entityID int64, shape Shape, layer, mask uint32, trigger bool)

	// Remove unregisters the collision shape for the given entity.
	Remove(entityID int64)

	// Set replaces the collision shape, layer, mask, and trigger flag for a
	// registered entity. The entity's current position is preserved. No-op if
	// entityID is not registered; call Add first to register it.
	Set(entityID int64, shape Shape, layer, mask uint32, trigger bool)

	// Update synchronises the entity's position in the backend.
	// Must be called each tick before Step to keep shape positions current.
	Update(entityID int64, x, y float64)

	// Step runs collision detection for this frame and returns all detected contacts.
	// For physics-simulating backends (cp) Step also advances the simulation by dt.
	// Detection-only backends (resolv) may ignore dt.
	Step(dt float64) []Contact

	// ReadBack calls fn for each entity whose position was modified by the backend.
	// Used by physics-simulating backends (cp) to sync corrected positions back to entities.
	// Detection-only backends (resolv) implement this as a no-op.
	ReadBack(fn func(entityID int64, x, y float64))
}
