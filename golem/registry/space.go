package registry

import "slices"

// SpaceID identifies a separate coordinate space. Every entity belongs to one
// space; space 0 is the default and the only space a single-space game uses.
type SpaceID uint32

// SpaceMove records a published entity's space change between flushes.
type SpaceMove struct {
	ID       int64
	From, To SpaceID
}

// SpacedEntity pairs a published entity with its space.
type SpacedEntity struct {
	Entity Entity
	Space  SpaceID
}

// Space returns the space of a published or provisional entity, or (0, false)
// when the ID is unknown.
func (r *Registry) Space(id int64) (SpaceID, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.entities[id] == nil && r.provisional[id] == nil {
		return 0, false
	}
	return r.spaces[id], true
}

// SetSpace moves a published entity to space and records the move for the
// next FlushAll. Moves coalesce per entity: the flush reports the space held
// at the previous flush and the latest space. Setting the current space is a
// no-op. Returns false when the entity is unknown or still provisional.
func (r *Registry) SetSpace(id int64, space SpaceID) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.entities[id] == nil {
		return false
	}
	from := r.spaces[id]
	if from == space {
		return true
	}
	r.setSpaceLocked(id, space)
	if i, ok := r.moveIndex[id]; ok {
		r.moves[i].To = space
	} else {
		r.moveIndex[id] = len(r.moves)
		r.moves = append(r.moves, SpaceMove{ID: id, From: from, To: space})
	}
	return true
}

// SpacesInUse reports whether any published or provisional entity is outside
// space 0. It is a single atomic load and takes no lock.
func (r *Registry) SpacesInUse() bool {
	return r.spaceCount.Load() != 0
}

// AppendSpaced appends every published entity with its space to dst under one
// read lock and returns the extended slice. Provisional entries are excluded.
func (r *Registry) AppendSpaced(dst []SpacedEntity) []SpacedEntity {
	r.mu.RLock()
	defer r.mu.RUnlock()
	dst = slices.Grow(dst, len(r.entities))
	for id, e := range r.entities {
		dst = append(dst, SpacedEntity{Entity: e, Space: r.spaces[id]})
	}
	return dst
}

// setSpaceLocked stores space for id, keeping only non-zero entries.
func (r *Registry) setSpaceLocked(id int64, space SpaceID) {
	if space == 0 {
		if _, ok := r.spaces[id]; !ok {
			return
		}
		delete(r.spaces, id)
	} else {
		r.spaces[id] = space
	}
	r.spaceCount.Store(int64(len(r.spaces)))
}

// dropMoveLocked tombstones a pending move so FlushAll skips it.
func (r *Registry) dropMoveLocked(id int64) {
	if i, ok := r.moveIndex[id]; ok {
		r.moves[i].To = r.moves[i].From
		delete(r.moveIndex, id)
	}
}

func (r *Registry) resetMovesLocked() {
	if len(r.moves) == 0 {
		return
	}
	r.moves = r.moves[:0]
	clear(r.moveIndex)
}
