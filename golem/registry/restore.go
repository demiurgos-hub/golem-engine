package registry

import "fmt"

// RestoreEntry pairs an entity with its optional command owner for atomic recovery.
type RestoreEntry struct {
	Entity Entity
	Owner  int64
}

// ReplaceWithoutHooks validates and replaces every entity in one critical section.
// It queues complete spawns and removals for the next flush without invoking
// OnSpawn/OnRemove. Recovery hosts must quiesce gameplay and connected clients,
// restore dependent systems, and dispatch their explicit restoration hooks after
// the complete world is ready. Invalid entries leave the registry unchanged.
func (r *Registry) ReplaceWithoutHooks(entries []RestoreEntry) error {
	entities := make(map[int64]Entity, len(entries))
	owners := make(map[int64]int64)
	spawned := make([]Entity, 0, len(entries))
	for _, entry := range entries {
		if entry.Entity == nil {
			return fmt.Errorf("registry: nil restored entity")
		}
		id := entry.Entity.EntityID()
		if id == 0 || entities[id] != nil {
			return fmt.Errorf("registry: invalid or duplicate restored entity %d", id)
		}
		entities[id] = entry.Entity
		spawned = append(spawned, entry.Entity)
		if entry.Owner != 0 {
			owners[id] = entry.Owner
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.provisional) != 0 {
		return fmt.Errorf("registry: cannot replace entities during provisional setup")
	}
	var removed []int64
	var revisions []uint64
	for id, e := range r.entities {
		if entities[id] == nil {
			removed = append(removed, id)
			revisions = append(revisions, removalRevision(e))
		}
	}
	r.entities, r.owners, r.spawned = entities, owners, spawned
	r.despawned, r.despawnedRevision = removed, revisions
	return nil
}
