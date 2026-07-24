// Package footprint loads versioned collision footprint YAML and places shapes
// into a collision.Backend or collision3d.Backend.
//
// Footprints are collision-only geometry authored for static or placed prefabs.
// This package does not depend on the golem runtime, registry, or navigation.
//
// # Identity
//
// Unity asset GUID is the canonical footprint identity (LookupGUID). An optional
// unique alias supports handwritten Go lookups (LookupAlias). Prefab name and
// asset_path are diagnostic labels and need not be unique.
//
// # Synthetic collision IDs
//
// Each placed shape receives one synthetic negative collision ID from a shared
// or caller-supplied IDAllocator. The negative ID namespace is reserved for
// collision-only footprints; zero and positive IDs are reserved for Golem
// entity / caller-owned registrations on the same Backend. Callers must not
// overlap allocator IDs with IDs they register directly. Contact callbacks may
// include negative IDs without a matching registry entity.
//
// # Placement
//
// Placers accept translation, positive uniform scale, and exact quarter-turn
// rotation (2D around Z; 3D yaw around Y). A footprint's Dimensions must match
// the placer (2 for Placer2D, 3 for Placer3D); cross-dimension placement is
// rejected so 3D depth cannot be dropped and 2D AABBs cannot become zero-depth
// 3D boxes. For every shape the placer calls Backend.Add then Backend.Update
// with the transformed world position. AABB values in YAML are full extents.
// Shape offsets are root-local centers. Handle.Remove is nil-safe, idempotent,
// and safe for concurrent callers.
package footprint
