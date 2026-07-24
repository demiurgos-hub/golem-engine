package footprint

import "sync/atomic"

// IDAllocator yields unique synthetic collision IDs for placed footprint shapes.
//
// Contract:
//   - Next must return a strictly negative ID (< 0). Positive and zero IDs are
//     reserved for Golem entity / caller-owned collision registrations.
//   - The negative ID namespace is reserved for collision-only footprints and
//     other non-entity colliders. Callers that also register shapes directly on
//     the same Backend must not reuse IDs issued by an allocator (and must not
//     hand the allocator IDs that are already registered).
//   - IDs must be unique among all shapes registered on the same Backend for the
//     lifetime of those registrations.
//   - Default Golem entity IDs remain positive; contact callbacks may include
//     negative IDs without a matching registry entity.
type IDAllocator interface {
	Next() int64
}

// AtomicAllocator is a concurrency-safe descending negative ID allocator.
// The first Next call returns -1, then -2, and so on. It is safe to share one
// allocator across placers that target the same Backend.
type AtomicAllocator struct {
	next atomic.Int64 // starts at 0; Add(-1) returns -1, -2, ...
}

// NewAtomicAllocator creates an allocator that issues -1, -2, ... uniquely.
func NewAtomicAllocator() *AtomicAllocator {
	return &AtomicAllocator{}
}

// Next returns the next unique negative collision ID.
func (a *AtomicAllocator) Next() int64 {
	return a.next.Add(-1)
}

// defaultIDs is shared across placers that do not supply their own allocator.
// Safe for concurrent Next calls. Collision backends themselves remain
// tick-goroutine-only and are not synchronized by this package; callers that
// also register positive/entity IDs on the same Backend must keep those IDs
// outside this descending negative sequence.
var defaultIDs = NewAtomicAllocator()
