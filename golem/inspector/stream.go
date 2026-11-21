package inspector

import (
	"slices"
	"time"

	"github.com/demiurgos-hub/golem-engine/golem"
	"github.com/demiurgos-hub/golem-engine/golem/inspector/wire"
	"github.com/demiurgos-hub/golem-engine/golem/registry"
)

// stream is the agent's tick-goroutine state. Only onFrame and its helpers
// touch it, so it needs no locking.
type stream struct {
	agent *Agent

	live      bool
	cooldown  bool // queue overflowed; wait for it to drain before resnapshotting
	epoch     uint64
	seq       uint64
	pending   map[int64]*pendingEntity
	removals  []wire.Removal
	moves     map[int64]uint32 // latest space per moved entity in the window
	lastFlush time.Time
	warned    map[uint32]bool
}

// spaceMoveSize bounds one encoded SpaceMove inside a Batch.
const spaceMoveSize = 20

type pendingEntity struct {
	mask    uint64
	full    bool // send FullUpdate instead of a masked delta
	spawned bool // spawned in the current window; a removal cancels it
}

func (s *stream) onFrame(f golem.ReplicationFrame) {
	a := s.agent
	if !a.observing.Load() {
		if s.live || s.cooldown {
			s.reset()
		}
		return
	}
	if s.cooldown {
		if len(a.out) > cap(a.out)/2 {
			return
		}
		s.cooldown = false
		a.restart.Store(true)
	}
	restart := a.restart.Swap(false)
	if !s.live || restart {
		s.snapshot(f.Tick)
		return
	}
	s.accumulate(f)
	if time.Since(s.lastFlush) >= time.Duration(float64(time.Second)/a.opts.RateHz) {
		s.flush(f.Tick)
	}
}

func (s *stream) reset() {
	s.live = false
	s.cooldown = false
	clear(s.pending)
	clear(s.moves)
	s.removals = s.removals[:0]
}

// push enqueues frames as one queue item without blocking. On overflow the stream is abandoned
// and restarted with a new epoch once the writer catches up, so receivers see
// a clean resync instead of a sequence gap.
func (s *stream) push(frames [][]byte) bool {
	select {
	case s.agent.out <- frames:
		return true
	default:
		s.reset()
		s.cooldown = true
		return false
	}
}

func (s *stream) nextSeq() uint64 {
	s.seq++
	return s.seq
}

func (s *stream) snapshot(tick uint64) {
	a := s.agent
	s.reset()
	if s.pending == nil {
		s.pending = map[int64]*pendingEntity{}
		s.moves = map[int64]uint32{}
	}
	s.epoch = a.epoch.Add(1)
	s.seq = 0

	entities := a.srv.All()
	states := make([]wire.EntityState, 0, len(entities))
	for _, e := range entities {
		if st, ok := s.entityState(e); ok {
			states = append(states, st)
		}
	}
	slices.SortFunc(states, func(x, y wire.EntityState) int { return cmpInt64(x.ID, y.ID) })

	// The whole snapshot is one queue item so a large world cannot overflow
	// the queue on its own.
	frames := [][]byte{wire.Encode(&wire.SnapshotBegin{Epoch: s.epoch, Seq: s.nextSeq(), Tick: tick, EntityCount: uint64(len(states))})}
	chunk := &wire.SnapshotChunk{Epoch: s.epoch}
	size := 0
	emit := func() {
		chunk.Seq = s.nextSeq()
		frames = append(frames, wire.Encode(chunk))
		chunk = &wire.SnapshotChunk{Epoch: s.epoch}
		size = 0
	}
	for _, st := range states {
		if size > 0 && size+st.EncodedSize() > a.opts.MaxFrameBytes {
			emit()
		}
		chunk.Entities = append(chunk.Entities, st)
		size += st.EncodedSize()
	}
	if len(chunk.Entities) > 0 || len(states) == 0 {
		emit()
	}
	frames = append(frames,
		wire.Encode(&wire.SnapshotEnd{Epoch: s.epoch, Seq: s.nextSeq()}),
		wire.Encode(&wire.ObserveStatus{State: wire.ObserveReady, EntityCount: uint64(len(states)), Epoch: s.epoch}))
	if !s.push(frames) {
		return
	}
	s.live = true
	s.lastFlush = time.Now()
}

func (s *stream) accumulate(f golem.ReplicationFrame) {
	a := s.agent
	for _, id := range f.Spawned {
		s.pending[id] = &pendingEntity{full: true, spawned: true}
	}
	for _, id := range f.Changed {
		p := s.pending[id]
		if p == nil {
			p = &pendingEntity{}
			s.pending[id] = p
		}
		if p.full {
			continue
		}
		e, ok := a.srv.Get(id)
		if !ok {
			continue
		}
		if d, ok := e.(registry.ReplicationDeltaEntity); ok {
			p.mask |= d.LastFlushMask()
		} else {
			p.full = true
		}
	}
	for _, m := range f.Moved {
		s.moves[m.ID] = uint32(m.To)
	}
	for _, r := range f.Removed {
		delete(s.moves, r.ID)
		if p := s.pending[r.ID]; p != nil && p.spawned {
			delete(s.pending, r.ID)
			continue
		}
		delete(s.pending, r.ID)
		s.removals = append(s.removals, wire.Removal{ID: r.ID, Revision: r.Revision})
	}
}

func (s *stream) flush(tick uint64) {
	a := s.agent
	s.lastFlush = time.Now()
	if len(s.pending) == 0 && len(s.removals) == 0 && len(s.moves) == 0 {
		return
	}
	ids := make([]int64, 0, len(s.pending))
	for id := range s.pending {
		ids = append(ids, id)
	}
	slices.Sort(ids)

	batch := &wire.Batch{Epoch: s.epoch, Tick: tick}
	size := 0
	var frames [][]byte
	emit := func() {
		batch.Seq = s.nextSeq()
		frames = append(frames, wire.Encode(batch))
		batch = &wire.Batch{Epoch: s.epoch, Tick: tick}
		size = 0
	}
	for _, id := range ids {
		p := s.pending[id]
		e, ok := a.srv.Get(id)
		if !ok {
			continue
		}
		if size >= a.opts.MaxFrameBytes {
			emit()
		}
		if p.full {
			if st, ok := s.entityState(e); ok {
				batch.Spawns = append(batch.Spawns, st)
				size += st.EncodedSize()
			}
			continue
		}
		if d, ok := s.entityDelta(e, p.mask); ok {
			batch.Deltas = append(batch.Deltas, d)
			size += d.EncodedSize()
			// A delta and its space move travel in the same batch, so viewers
			// never apply the new position in the old space.
			if space, moved := s.moves[id]; moved {
				batch.Moves = append(batch.Moves, wire.SpaceMove{ID: id, Space: space})
				size += spaceMoveSize
				delete(s.moves, id)
			}
		}
	}
	batch.Removals = append(batch.Removals, s.removals...)
	// Full states already carry the entity's current space; moves that rode
	// with a delta were sent above.
	moved := make([]int64, 0, len(s.moves))
	for id := range s.moves {
		if p := s.pending[id]; p == nil || !p.full {
			moved = append(moved, id)
		}
	}
	slices.Sort(moved)
	for _, id := range moved {
		if size >= a.opts.MaxFrameBytes {
			emit()
		}
		batch.Moves = append(batch.Moves, wire.SpaceMove{ID: id, Space: s.moves[id]})
		size += spaceMoveSize
	}
	clear(s.pending)
	clear(s.moves)
	s.removals = s.removals[:0]
	if len(batch.Spawns) > 0 || len(batch.Deltas) > 0 || len(batch.Removals) > 0 || len(batch.Moves) > 0 {
		emit()
	}
	if len(frames) > 0 {
		s.push(frames)
	}
}

func (s *stream) entityState(e golem.Entity) (wire.EntityState, bool) {
	data, err := e.FullUpdate()
	if err != nil || len(data) == 0 {
		return wire.EntityState{}, false
	}
	tag, payload, err := wire.UnwrapUpdate(data)
	if err != nil {
		return wire.EntityState{}, false
	}
	ent := s.agent.schema.EntityByTag(tag)
	if ent == nil {
		s.warnUnknown(tag, e)
		return wire.EntityState{}, false
	}
	space, _ := s.agent.srv.EntitySpace(e.EntityID())
	return wire.EntityState{ID: e.EntityID(), TypeTag: ent.StateTag, State: payload, Space: uint32(space)}, true
}

func (s *stream) entityDelta(e golem.Entity, mask uint64) (wire.EntityDelta, bool) {
	d, ok := e.(registry.ReplicationDeltaEntity)
	if !ok || mask == 0 {
		return wire.EntityDelta{}, false
	}
	data, err := d.MarshalDeltaMask(mask)
	if err != nil || len(data) == 0 {
		return wire.EntityDelta{}, false
	}
	tag, payload, err := wire.UnwrapUpdate(data)
	if err != nil {
		return wire.EntityDelta{}, false
	}
	ent := s.agent.schema.EntityByTag(tag)
	if ent == nil {
		s.warnUnknown(tag, e)
		return wire.EntityDelta{}, false
	}
	var rev uint64
	if r, ok := e.(registry.StateRevisioner); ok {
		rev = r.StateRevision()
	}
	return wire.EntityDelta{ID: e.EntityID(), TypeTag: ent.StateTag, Revision: rev, Mask: mask, Delta: payload}, true
}

func (s *stream) warnUnknown(tag uint32, e golem.Entity) {
	if s.warned == nil {
		s.warned = map[uint32]bool{}
	}
	if !s.warned[tag] {
		s.warned[tag] = true
		s.agent.opts.Logger.Printf("golem/inspector: entity type %q (EntityUpdate tag %d) is not in the inspector schema; skipping it", e.TypeName(), tag)
	}
}

func cmpInt64(a, b int64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}
