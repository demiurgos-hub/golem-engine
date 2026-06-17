package golem

import "golem.collision"

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
