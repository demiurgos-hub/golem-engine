package golem

import (
	"sync"
	"sync/atomic"

	"github.com/demiurgos-hub/golem-engine/golem/registry"
)

// RemovedEntity identifies an entity removed during a replication flush.
type RemovedEntity struct {
	ID       int64
	Revision uint64
}

// EntitySpaceMove reports a published entity's space change (ID, From, To)
// since the previous replication flush.
type EntitySpaceMove = registry.SpaceMove

// ReplicationFrame lists the entities a tick's replication flush touched,
// before any interest, visibility or ownership filtering. Slices are only
// valid for the duration of the observer callback. Moved holds one coalesced
// entry per entity whose space changed since the previous flush; entities
// spawned or removed in the same flush and net no-op moves are omitted.
type ReplicationFrame struct {
	Tick    uint64
	Spawned []int64
	Changed []int64
	Removed []RemovedEntity
	Moved   []EntitySpaceMove
}

type replicationObserver struct {
	fn func(ReplicationFrame)
}

type replicationObservers struct {
	mu   sync.Mutex // serializes writers; readers load the snapshot lock-free
	list atomic.Pointer[[]*replicationObserver]
}

// ObserveReplication registers fn to run once per tick after the replication
// flush, on the tick goroutine, and returns a function that unregisters it.
// Unlike OnUpdates it supports any number of observers and may be called while
// the server runs. fn sees every spawned, changed, removed and space-moved
// entity regardless of interest, visibility or owner-only filtering; inside fn, Get, All and
// entity reads such as FullUpdate, LastFlushMask and MarshalDeltaMask observe
// the state that was just flushed. fn must not block and must not retain the
// frame's slices.
func (s *Server) ObserveReplication(fn func(ReplicationFrame)) (remove func()) {
	if fn == nil {
		return func() {}
	}
	obs := &replicationObserver{fn: fn}
	s.replicationObservers.update(func(list []*replicationObserver) []*replicationObserver {
		return append(list, obs)
	})
	var once sync.Once
	return func() {
		once.Do(func() {
			s.replicationObservers.update(func(list []*replicationObserver) []*replicationObserver {
				out := make([]*replicationObserver, 0, len(list))
				for _, o := range list {
					if o != obs {
						out = append(out, o)
					}
				}
				return out
			})
		})
	}
}

func (o *replicationObservers) update(change func([]*replicationObserver) []*replicationObserver) {
	o.mu.Lock()
	defer o.mu.Unlock()
	var current []*replicationObserver
	if p := o.list.Load(); p != nil {
		current = *p
	}
	next := change(append([]*replicationObserver(nil), current...))
	o.list.Store(&next)
}

// publishReplicationFrame delivers result to replication observers. It costs a
// single atomic load when no observer is registered.
func (s *Server) publishReplicationFrame(result registry.FlushResult) {
	p := s.replicationObservers.list.Load()
	if p == nil || len(*p) == 0 {
		return
	}
	frame := ReplicationFrame{Tick: s.tick, Spawned: result.SpawnIDs, Changed: result.DeltaIDs, Moved: result.Moves}
	if len(result.Removals) > 0 {
		frame.Removed = make([]RemovedEntity, len(result.Removals))
		for i, id := range result.Removals {
			frame.Removed[i] = RemovedEntity{ID: id, Revision: result.RemovalRevisions[i]}
		}
	}
	for _, o := range *p {
		o.fn(frame)
	}
}
