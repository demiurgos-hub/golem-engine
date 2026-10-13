package footprint

import (
	"fmt"
	"sync/atomic"
)

type removalBackend interface{ Remove(int64) }

type placementHandle struct {
	backend removalBackend
	ids     []int64
	removed atomic.Bool
}

func (h *placementHandle) IDs() []int64 {
	if h == nil || len(h.ids) == 0 {
		return nil
	}
	out := make([]int64, len(h.ids))
	copy(out, h.ids)
	return out
}

func (h *placementHandle) Remove() {
	if h == nil || !h.removed.CompareAndSwap(false, true) {
		return
	}
	if h.backend == nil {
		return
	}
	for _, id := range h.ids {
		h.backend.Remove(id)
	}
}

// registerPrepared is called only after all dimension-specific preparation.
// Ownership is recorded after Add and before Update, matching the backend call
// order. Backend panics and allocator uniqueness remain caller responsibilities.
func registerPrepared[T any](h *placementHandle, alloc IDAllocator, items []T, add func(int64, T), update func(int64, T)) error {
	if alloc == nil {
		alloc = defaultIDs
	}
	for _, item := range items {
		id := alloc.Next()
		if id >= 0 {
			h.Remove()
			return fmt.Errorf("footprint: allocator must return negative IDs, got %d", id)
		}
		add(id, item)
		h.ids = append(h.ids, id)
		update(id, item)
	}
	return nil
}
