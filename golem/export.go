package golem

import (
	"golem-engine/golem/collision3d"
	"golem-engine/golem/interest"
	golemnet "golem-engine/golem/net"
	"golem-engine/golem/registry"
	"golem-engine/golem/snapshot"
	"golem-engine/golem/world"

	"golem.collision"
	"golem.nav"
)

// Re-export registry entity interfaces, net, interest, and world types so
// generated game code and consumers can depend on a single golem import path
// (see golem_import in golem.yaml). Live entity storage is internal to golem.Server
// (CreateEntity, Get, Remove, …); use package golem/registry for low-level embedding.

type (
	Entity = registry.Entity

	Ticker  = registry.Ticker
	Spawner = registry.Spawner
	Remover = registry.Remover

	// PositionWriter is satisfied by any entity with SetPosition(x, y float32).
	// All generated Synced* types implement it. Used by collision backends to
	// write physics-corrected positions back to entities each tick.
	PositionWriter = registry.PositionWriter

	// Spatial3DEntity is satisfied by generated 3D entities with Position3D.
	Spatial3DEntity = registry.Spatial3DEntity

	// Position3DWriter is satisfied by generated 3D entities with SetPosition3D.
	Position3DWriter = registry.Position3DWriter

	// EntityIDSetter is satisfied by any entity with SetEntityID(int64).
	// All generated Synced* types implement it. Used by CreateEntity to assign
	// an auto-incremented ID when the entity is constructed without one.
	EntityIDSetter = registry.EntityIDSetter

	Session         = golemnet.Session
	Transport       = golemnet.Transport
	CertificateHash = golemnet.CertificateHash

	InterestManager = interest.Manager
	InterestFOI     = interest.FOI
	InterestDiff    = interest.Diff

	WorldData  = world.Data
	WorldStore = world.Store

	// SnapshotRecord is the decoded state of one entity from a snapshot file.
	// Pass records from snapshot.Load to the generated RestoreEntity helper.
	SnapshotRecord = snapshot.Record

	// CollisionSpatialQuery is an optional interface backends may implement to
	// support one-shot OverlapBox / OverlapCircle queries.
	CollisionSpatialQuery = collision.SpatialQuery
	// CollisionCastQuery is an optional interface backends may implement to
	// support Raycast, BoxCast, and CircleCast queries.
	CollisionCastQuery = collision.CastQuery
	// CollisionRaycastHit is the result of a cast query: entity ID, world-space
	// contact point and normal, and normalised fraction along the cast segment.
	CollisionRaycastHit = collision.RaycastHit
	// CollisionBackend is the interface implemented by collision backends
	// (resolv, cp, …). Pass one to Server.SetCollisionBackend.
	CollisionBackend = collision.Backend
	// CollisionContact describes a single detected collision between two entities.
	CollisionContact = collision.Contact
	// CollisionShape is the sealed interface for collision shape descriptors
	// (CollisionCircle, CollisionAABB).
	CollisionShape = collision.Shape
	// CollisionCircle is a circular collision shape.
	CollisionCircle = collision.Circle
	// CollisionAABB is an axis-aligned bounding-box collision shape.
	CollisionAABB = collision.AABB
	// CollisionVec2 is a 2D vector used in collision contacts.
	CollisionVec2 = collision.Vec2
	// CollisionLayers maps named layers to bit indices and maintains a symmetric
	// collision matrix. Use NewCollisionLayers to create one; call Define then
	// SetCollides to configure it; then pass Layer/Mask/MaskFor results to
	// backend.Add, backend.Set, and spatial query methods.
	CollisionLayers = collision.Layers

	// CollisionSpatialQuery3D is an optional interface backends may implement to
	// support OverlapBox3D / OverlapSphere queries.
	CollisionSpatialQuery3D = collision3d.SpatialQuery
	// CollisionCastQuery3D is an optional interface backends may implement to
	// support Raycast3D queries.
	CollisionCastQuery3D = collision3d.CastQuery
	// CollisionRaycastHit3D is the result of a 3D cast query.
	CollisionRaycastHit3D = collision3d.RaycastHit
	// CollisionBackend3D is the interface implemented by 3D collision backends.
	CollisionBackend3D = collision3d.Backend
	// CollisionContact3D describes a detected 3D collision between two entities.
	CollisionContact3D = collision3d.Contact
	// CollisionShape3D is the sealed interface for 3D collision shapes.
	CollisionShape3D = collision3d.Shape
	// CollisionSphere is a spherical 3D collision shape.
	CollisionSphere = collision3d.Sphere
	// CollisionAABB3D is an axis-aligned 3D box collision shape.
	CollisionAABB3D = collision3d.AABB
	// CollisionVec3 is a 3D vector used in collision contacts and queries.
	CollisionVec3 = collision3d.Vec3
	// CollisionSimple3DBackend is the pure-Go detection-only 3D collision backend.
	CollisionSimple3DBackend = collision3d.SimpleBackend

	// NavBackend is the interface implemented by nav backends.
	// Build one from your map data at startup and pass it to Server.SetNavBackend.
	NavBackend = nav.Backend
	// NavDynamicBackend is an optional nav.Backend extension for backends that
	// support runtime walkability updates. Check for it with a type assertion or
	// use Server.SetNavWalkable, which does so internally.
	NavDynamicBackend = nav.DynamicBackend
	// NavPoint is a world-space coordinate on a nav path, as returned by
	// Server.FindPath.
	NavPoint = nav.Point
)

const (
	TransportWebSocket    = golemnet.TransportWebSocket
	TransportWebTransport = golemnet.TransportWebTransport
)

// ErrSessionNotFound reports that a targeted session disconnected before a send
// operation could deliver the frame.
var ErrSessionNotFound = golemnet.ErrSessionNotFound

// ErrUnreliableNotSupported reports that the active transport has no datagram lane.
var ErrUnreliableNotSupported = golemnet.ErrUnreliableNotSupported

// ErrReliableDatagramsNotSupported reports that the active transport has no reliable datagram lanes.
var ErrReliableDatagramsNotSupported = golemnet.ErrReliableDatagramsNotSupported

// NewCollisionSimple3DBackend creates a pure-Go detection-only 3D collision backend.
func NewCollisionSimple3DBackend() *collision3d.SimpleBackend {
	return collision3d.NewSimpleBackend()
}
