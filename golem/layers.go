package golem

import (
	"github.com/demiurgos-hub/golem-engine/golem/collision"
	"github.com/demiurgos-hub/golem-engine/golem/collision3d"
)

// NewCollisionLayers creates an empty CollisionLayers registry.
// Call Bind to attach a backend, then Define to register named layers, then
// SetCollides to record which layer pairs interact. After that, use Add, Set,
// and Remove instead of calling the backend directly — layer bits and masks are
// derived automatically from the collision matrix.
//
// Layer, Mask, and MaskFor remain available for spatial queries such as
// OverlapBox and Raycast.
func NewCollisionLayers() *collision.Layers {
	return collision.NewLayers()
}

// NewCollisionLayers3D creates an empty CollisionLayers3D registry for 3D
// backends. Call Bind, Define, and SetCollides as with NewCollisionLayers, then
// use Add/Set/Remove with CollisionShape3D values.
func NewCollisionLayers3D() *collision3d.Layers {
	return collision3d.NewLayers()
}
