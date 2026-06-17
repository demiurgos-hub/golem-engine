package registry

import (
	"fmt"
	"sync"
)

// FlushResult holds the per-tick output of FlushAll, categorised by lifecycle event.
// SpawnIDs[i] and DeltaIDs[i] correspond to Spawns[i] and Deltas[i] respectively,
// allowing the interest system to map serialized data back to entity IDs.
type FlushResult struct {
	SpawnIDs         []int64  // entity IDs corresponding 1:1 with Spawns
	Spawns           [][]byte // FullUpdate for entities added since last flush
	DeltaIDs         []int64  // entity IDs corresponding 1:1 with Deltas
	Deltas           [][]byte // FlushUpdate for dirty pre-existing entities
	Removals         []int64  // IDs removed since last flush
	RemovalRevisions []uint64 // removal revisions corresponding 1:1 with Removals
}

// Registry is a thread-safe container for all live entities in the game world.
// It tracks spawns and removals between flushes so the server can broadcast
// lifecycle events to connected clients. It also tracks entity ownership for
// command authority validation.
type Registry struct {
	mu                sync.RWMutex
	entities          map[int64]Entity
	owners            map[int64]int64 // entityID → ownerSessionID
	spawned           []Entity
	despawned         []int64
	despawnedRevision []uint64
}

// NewRegistry creates an empty entity registry.
func NewRegistry() *Registry {
	return &Registry{
		entities: make(map[int64]Entity),
		owners:   make(map[int64]int64),
	}
}

// Add registers an unowned entity (e.g. NPC, world object) and marks it as newly spawned.
// If the entity implements Spawner, OnSpawn is called after insertion.
// Returns an error if the ID is already taken.
func (r *Registry) Add(e Entity) error {
	r.mu.Lock()
	id := e.EntityID()
	if _, exists := r.entities[id]; exists {
		r.mu.Unlock()
		return fmt.Errorf("entity %d already registered", id)
	}
	r.entities[id] = e
	r.spawned = append(r.spawned, e)
	r.mu.Unlock()

	if s, ok := e.(Spawner); ok {
		s.OnSpawn()
	}
	return nil
}

// AddOwned registers an entity with a session owner and marks it as newly spawned.
// The ownerSessionID is used by CommandRouter to validate authority on
// entity-targeted commands. If the entity implements Spawner, OnSpawn is called
// after insertion. Returns an error if the ID is already taken.
func (r *Registry) AddOwned(e Entity, ownerSessionID int64) error {
	r.mu.Lock()
	id := e.EntityID()
	if _, exists := r.entities[id]; exists {
		r.mu.Unlock()
		return fmt.Errorf("entity %d already registered", id)
	}
	r.entities[id] = e
	r.owners[id] = ownerSessionID
	r.spawned = append(r.spawned, e)
	r.mu.Unlock()

	if s, ok := e.(Spawner); ok {
		s.OnSpawn()
	}
	return nil
}

// Owner returns the session ID that owns the given entity.
// Returns (0, false) if the entity doesn't exist or has no owner.
func (r *Registry) Owner(entityID int64) (sessionID int64, owned bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	sid, ok := r.owners[entityID]
	return sid, ok
}

// SetOwner updates the owning session of an existing entity. Useful for
// transferring command authority on reconnect (new session ID for the same
// character). Returns false if the entity does not exist.
func (r *Registry) SetOwner(entityID, sessionID int64) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.entities[entityID]; !exists {
		return false
	}
	r.owners[entityID] = sessionID
	return true
}

// DeleteEntity unregisters an entity by ID and records it for removal broadcast.
// Also cleans up any ownership entry. If the entity implements Remover,
// OnRemove is called after deletion. No-op if the ID doesn't exist.
func (r *Registry) DeleteEntity(id int64) {
	r.mu.Lock()
	e, exists := r.entities[id]
	if exists {
		delete(r.entities, id)
		delete(r.owners, id)
		r.despawned = append(r.despawned, id)
		r.despawnedRevision = append(r.despawnedRevision, removalRevision(e))
	}
	r.mu.Unlock()

	if exists {
		if rm, ok := e.(Remover); ok {
			rm.OnRemove()
		}
	}
}

// Get returns the entity with the given ID, or (nil, false) if not found.
func (r *Registry) Get(id int64) (Entity, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.entities[id]
	return e, ok
}

// Len returns the number of registered entities.
func (r *Registry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.entities)
}

// All returns a snapshot slice of every registered entity.
// Safe to call from the tick goroutine for interest-management grid updates.
func (r *Registry) All() []Entity {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Entity, 0, len(r.entities))
	for _, e := range r.entities {
		out = append(out, e)
	}
	return out
}

// TickAll calls Tick(dt) on every entity that implements Ticker.
// The entity map is snapshotted under a read-lock; individual Tick calls
// run outside the lock so they may safely call Add/Remove on the registry.
func (r *Registry) TickAll(dt float64) {
	r.mu.RLock()
	snapshot := make([]Entity, 0, len(r.entities))
	for _, e := range r.entities {
		snapshot = append(snapshot, e)
	}
	r.mu.RUnlock()

	for _, e := range snapshot {
		if t, ok := e.(Ticker); ok {
			t.Tick(dt)
		}
	}
}

// FlushAll collects all pending lifecycle events and dirty-field deltas into
// a FlushResult, then resets the tracking state.
// Spawned entities get FullUpdate (skipping FlushUpdate to avoid redundancy).
// Pre-existing entities get FlushUpdate as before.
func (r *Registry) FlushAll() (FlushResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	var result FlushResult

	spawnedIDs := make(map[int64]bool, len(r.spawned))
	for _, e := range r.spawned {
		id := e.EntityID()
		spawnedIDs[id] = true
		data, err := e.FullUpdate()
		if err != nil {
			return result, fmt.Errorf("spawning entity %d (%s): %w", id, e.TypeName(), err)
		}
		result.SpawnIDs = append(result.SpawnIDs, id)
		result.Spawns = append(result.Spawns, data)
	}

	for _, e := range r.entities {
		id := e.EntityID()
		if spawnedIDs[id] {
			continue
		}
		data, err := e.FlushUpdate()
		if err != nil {
			return result, fmt.Errorf("flushing entity %d (%s): %w", id, e.TypeName(), err)
		}
		if data != nil {
			result.DeltaIDs = append(result.DeltaIDs, id)
			result.Deltas = append(result.Deltas, data)
		}
	}

	result.Removals = r.despawned
	result.RemovalRevisions = r.despawnedRevision

	r.spawned = r.spawned[:0]
	r.despawned = r.despawned[:0]
	r.despawnedRevision = r.despawnedRevision[:0]

	return result, nil
}

func removalRevision(e Entity) uint64 {
	if r, ok := e.(StateRevisioner); ok {
		return r.StateRevision() + 1
	}
	return 1
}

// SnapshotAll returns a full-state update for every registered entity.
// Used to send the entire world state to a newly connected client.
func (r *Registry) SnapshotAll() ([][]byte, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	updates := make([][]byte, 0, len(r.entities))
	for _, e := range r.entities {
		data, err := e.FullUpdate()
		if err != nil {
			return nil, fmt.Errorf("snapshotting entity %d (%s): %w", e.EntityID(), e.TypeName(), err)
		}
		updates = append(updates, data)
	}
	return updates, nil
}
