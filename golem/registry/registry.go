package registry

import (
	"fmt"
	"sync"
	"sync/atomic"
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
	// Moves lists published entities whose space changed since the last flush,
	// one coalesced entry per entity in first-move order. Entities spawned or
	// removed in the same window and net no-op moves are omitted.
	Moves []SpaceMove
}

// Registry is a thread-safe container for all live entities in the game world.
// It tracks spawns and removals between flushes so the server can broadcast
// lifecycle events to connected clients. It also tracks entity ownership for
// command authority validation.
type Registry struct {
	mu                sync.RWMutex
	entities          map[int64]Entity
	provisional       map[int64]Entity
	owners            map[int64]int64   // entityID → ownerSessionID
	spaces            map[int64]SpaceID // entityID → space; non-zero entries only
	spaceCount        atomic.Int64      // len(spaces), readable without the mutex
	moves             []SpaceMove       // pending moves since the last flush
	moveIndex         map[int64]int     // entityID → index in moves
	spawned           []Entity
	despawned         []int64
	despawnedRevision []uint64
	// incarnation identifies each lifetime of a published or provisional
	// entity ID: an ID deleted and registered again gets a new value, so
	// callers holding an ID across callbacks can tell a replacement apart
	// without comparing Entity values (which may be non-comparable).
	incarnation     map[int64]uint64
	nextIncarnation uint64
}

// NewRegistry creates an empty entity registry.
func NewRegistry() *Registry {
	return &Registry{
		entities:    make(map[int64]Entity),
		provisional: make(map[int64]Entity),
		owners:      make(map[int64]int64),
		spaces:      make(map[int64]SpaceID),
		moveIndex:   make(map[int64]int),
		incarnation: make(map[int64]uint64),
	}
}

// Add registers an unowned entity (e.g. NPC, world object) and marks it as newly spawned.
// If the entity implements Spawner, OnSpawn is called after insertion.
// Returns an error if the ID is already taken.
func (r *Registry) Add(e Entity) error {
	if err := r.AddWithoutSpawn(e); err != nil {
		return err
	}
	NotifySpawn(e)
	return nil
}

// AddOwned registers an entity with a session owner and marks it as newly spawned.
// The ownerSessionID is used by CommandRouter to validate authority on
// entity-targeted commands. If the entity implements Spawner, OnSpawn is called
// after insertion. Returns an error if the ID is already taken.
func (r *Registry) AddOwned(e Entity, ownerSessionID int64) error {
	if err := r.AddOwnedWithoutSpawn(e, ownerSessionID); err != nil {
		return err
	}
	NotifySpawn(e)
	return nil
}

// AddWithoutSpawn registers an unowned entity and marks it spawned for the next
// flush, but does not call OnSpawn. Server uses this to register colliders
// between successful insertion and the spawn hook.
func (r *Registry) AddWithoutSpawn(e Entity) error {
	return r.insert(0, e, 0, false)
}

// AddOwnedWithoutSpawn is AddOwned without calling OnSpawn.
func (r *Registry) AddOwnedWithoutSpawn(e Entity, ownerSessionID int64) error {
	return r.insert(0, e, ownerSessionID, true)
}

// AddWithoutSpawnIn is AddWithoutSpawn with the entity placed in space.
func (r *Registry) AddWithoutSpawnIn(space SpaceID, e Entity) error {
	return r.insert(space, e, 0, false)
}

// AddOwnedWithoutSpawnIn is AddOwnedWithoutSpawn with the entity placed in space.
func (r *Registry) AddOwnedWithoutSpawnIn(space SpaceID, e Entity, ownerSessionID int64) error {
	return r.insert(space, e, ownerSessionID, true)
}

func (r *Registry) insert(space SpaceID, e Entity, ownerSessionID int64, hasOwner bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	id := e.EntityID()
	if _, exists := r.entities[id]; exists {
		return fmt.Errorf("entity %d already registered", id)
	}
	if _, exists := r.provisional[id]; exists {
		return fmt.Errorf("entity %d is being prepared", id)
	}
	r.entities[id] = e
	if hasOwner {
		r.owners[id] = ownerSessionID
	}
	r.setSpaceLocked(id, space)
	r.assignIncarnationLocked(id)
	r.spawned = append(r.spawned, e)
	return nil
}

// assignIncarnationLocked gives id a new, never reused incarnation.
func (r *Registry) assignIncarnationLocked(id int64) {
	if r.incarnation == nil {
		r.incarnation = make(map[int64]uint64)
	}
	r.nextIncarnation++
	r.incarnation[id] = r.nextIncarnation
}

// Incarnation returns the lifetime counter of a published or provisional
// entity, or (0, false) when id is unknown. It is assigned on insertion
// (AddProvisional included, kept by CommitProvisional), reassigned for every
// entity ReplaceWithoutHooks installs and dropped on deletion, so an ID that
// is deleted and registered again never reports the same incarnation.
func (r *Registry) Incarnation(id int64) (uint64, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	inc, ok := r.incarnation[id]
	return inc, ok
}

// NotifySpawn calls OnSpawn when e implements Spawner.
func NotifySpawn(e Entity) {
	if s, ok := e.(Spawner); ok {
		s.OnSpawn()
	}
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
// character). sessionID 0 clears ownership (entity becomes unowned).
// Returns false if the entity does not exist.
func (r *Registry) SetOwner(entityID, sessionID int64) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.entities[entityID] == nil && r.provisional[entityID] == nil {
		return false
	}
	if sessionID == 0 {
		delete(r.owners, entityID)
	} else {
		r.owners[entityID] = sessionID
	}
	return true
}

// DeleteEntity unregisters an entity by ID and records it for removal broadcast.
// Also cleans up any ownership, space and incarnation entry and drops a pending
// space move.
// If the entity implements Remover, OnRemove is called after deletion. No-op if
// the ID doesn't exist.
func (r *Registry) DeleteEntity(id int64) {
	r.mu.Lock()
	if _, pending := r.provisional[id]; pending {
		delete(r.provisional, id)
		delete(r.owners, id)
		delete(r.incarnation, id)
		r.setSpaceLocked(id, 0)
		r.mu.Unlock()
		return
	}
	e, exists := r.entities[id]
	if exists {
		delete(r.entities, id)
		delete(r.owners, id)
		delete(r.incarnation, id)
		r.setSpaceLocked(id, 0)
		r.dropMoveLocked(id)
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

// Get returns a published or provisional entity by ID, or (nil, false).
func (r *Registry) Get(id int64) (Entity, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.entities[id]
	if !ok {
		e, ok = r.provisional[id]
	}
	return e, ok
}

// Len returns the number of published entities, excluding provisional entries.
func (r *Registry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.entities)
}

// All returns a snapshot slice of published entities, excluding provisional entries.
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
	for _, e := range r.All() {
		if t, ok := e.(Ticker); ok {
			t.Tick(dt)
		}
	}
}

// FlushAll collects all pending lifecycle events, space moves and dirty-field
// deltas into a FlushResult, then resets the tracking state.
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
	for _, m := range r.moves {
		// A spawn already carries the final space; deletions leave tombstones.
		if m.From != m.To && !spawnedIDs[m.ID] {
			result.Moves = append(result.Moves, m)
		}
	}

	r.resetMovesLocked()
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
