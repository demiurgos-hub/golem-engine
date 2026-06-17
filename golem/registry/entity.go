package registry

// Entity is the interface that all generated synced entity types implement.
// It provides identity, type information, spatial position, and serialized
// state access for use by the registry, tick loop, and interest management.
type Entity interface {
	EntityID() int64
	TypeName() string

	// Position returns the entity's current 2D world-space coordinates.
	// Used by the interest system for 2D spatial queries and as the fallback
	// projection for entities that do not implement Spatial3DEntity.
	Position() (x, y float32)

	// IsGlobal reports whether this entity is always replicated to every
	// client regardless of field-of-interest distance checks.
	IsGlobal() bool

	// FlushUpdate returns a serialized EntityUpdate proto containing only the
	// fields that changed since the last flush. Returns nil if nothing changed.
	FlushUpdate() ([]byte, error)

	// FullUpdate returns a serialized EntityUpdate proto containing a complete
	// state snapshot. Used when a client needs the current world state.
	FullUpdate() ([]byte, error)
}

// StateRevisioner is optionally implemented by entities that expose a monotonic
// state revision for stale update rejection on clients.
type StateRevisioner interface {
	StateRevision() uint64
}

// ReplicationDeltaEntity is optionally implemented by generated entities that
// can remarshal selected current fields for ACK-aware eventual replication.
type ReplicationDeltaEntity interface {
	LastFlushMask() uint64
	MarshalDeltaMask(mask uint64) ([]byte, error)
}

// CompactReplicationDeltaEntity is optionally implemented by generated entities
// that can marshal selected current fields without EntityUpdate wrappers.
type CompactReplicationDeltaEntity interface {
	LastFlushMask() uint64
	MarshalCompactDeltaMask(mask uint64) ([]byte, error)
}

// Ticker is optionally implemented by entities that need per-tick update logic.
// The registry dispatches Tick for every Ticker before the user's OnTick callback.
type Ticker interface {
	Tick(dt float64)
}

// Spawner is optionally implemented by entities that need to run logic
// when added to the registry.
type Spawner interface {
	OnSpawn()
}

// Remover is optionally implemented by entities that need to run logic
// when removed from the registry.
type Remover interface {
	OnRemove()
}

// PositionWriter is optionally implemented by entities whose position can be
// overwritten by an external system such as a physics or collision backend.
// All generated Synced* types implement this automatically via SetPosition.
type PositionWriter interface {
	SetPosition(x, y float32)
}

// Spatial3DEntity is optionally implemented by entities that expose a 3D
// position. Generated 3D Synced* types implement it automatically. The Y axis
// is vertical; systems that need a horizontal projection use X/Z.
type Spatial3DEntity interface {
	Position3D() (x, y, z float32)
}

// Position3DWriter is optionally implemented by entities whose 3D position can
// be overwritten by an external system such as a 3D collision backend.
type Position3DWriter interface {
	SetPosition3D(x, y, z float32)
}

// EntityIDSetter is optionally implemented by entities that allow their ID to
// be assigned after construction. All generated Synced* types implement this.
// CreateEntity calls SetEntityID when an entity is constructed with no ID (0).
type EntityIDSetter interface {
	SetEntityID(int64)
}
