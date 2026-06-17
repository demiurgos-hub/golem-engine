// Package collision3d defines pure-Go 3D collision interfaces and primitive shapes.
package collision3d

// Vec3 is a 3D vector used by collision queries and contacts.
type Vec3 struct{ X, Y, Z float64 }

// Shape is a sealed interface for 3D collision shapes.
// Only Sphere and AABB satisfy it.
type Shape interface{ shapeTag() }

// Sphere is a spherical collision shape centered on the entity's position.
type Sphere struct{ R float64 }

// AABB is an axis-aligned box centered on the entity's position.
type AABB struct{ W, H, D float64 }

func (Sphere) shapeTag() {}
func (AABB) shapeTag()   {}

// Contact describes a detected 3D collision between two entities.
// Normal is the push direction for entity A; Depth is the penetration distance.
type Contact struct {
	A, B   int64
	Normal Vec3
	Depth  float64
}

// SpatialQuery is an optional interface backends may implement to support
// one-shot 3D overlap queries without registering a persistent shape.
type SpatialQuery interface {
	// OverlapBox returns IDs whose shapes overlap the axis-aligned box centered
	// at (cx, cy, cz) with half-extents (hw, hh, hd).
	OverlapBox(cx, cy, cz, hw, hh, hd float64, layerMask uint32) []int64

	// OverlapSphere returns IDs whose shapes overlap the sphere centered at
	// (cx, cy, cz) with the given radius.
	OverlapSphere(cx, cy, cz, radius float64, layerMask uint32) []int64
}

// RaycastHit is the result of a single 3D raycast query.
type RaycastHit struct {
	EntityID int64
	Point    Vec3
	Normal   Vec3
	Fraction float64
}

// CastQuery is an optional interface backends may implement to support 3D raycasts.
type CastQuery interface {
	// Raycast returns the first entity intersected by the segment from from to to.
	Raycast(from, to Vec3, layerMask uint32) (RaycastHit, bool)
	// RaycastAll returns all entities intersected by the segment, sorted by Fraction.
	RaycastAll(from, to Vec3, layerMask uint32) []RaycastHit
}

// Backend is implemented by 3D collision backends.
// All methods are called from the game tick goroutine; implementations need not be thread-safe.
type Backend interface {
	Add(entityID int64, shape Shape, layer, mask uint32, trigger bool)
	Remove(entityID int64)
	Set(entityID int64, shape Shape, layer, mask uint32, trigger bool)
	Update(entityID int64, x, y, z float64)
	Step(dt float64) []Contact
	ReadBack(fn func(entityID int64, x, y, z float64))
}
