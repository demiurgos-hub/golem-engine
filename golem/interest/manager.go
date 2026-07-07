package interest

import (
	"github.com/demiurgos-hub/golem-engine/golem/registry"
)

// FOI describes a circular field of interest anchored to an entity. 2D
// entities use the X/Y plane; 3D entities use the X/Z ground plane.
type FOI struct {
	EntityID int64   // the anchor entity whose position centres the FOI
	Radius   float64 // entities within this distance enter the FOI
	Margin   float64 // entities must exceed Radius+Margin to exit (hysteresis)
}

// Diff holds the per-session interest changes computed by a single tick.
type Diff struct {
	Entered []int64 // entity IDs that just became visible
	Stayed  []int64 // entity IDs that remain visible (may or may not be dirty)
	Exited  []int64 // entity IDs that just left visibility
}

const (
	candidateMarksCompactionFactor  = 4
	candidateMarksCompactionMinSize = 256
)

type diffScratch struct {
	diff                Diff
	queryBuf            []int64
	candidateMarks      map[int64]uint64
	candidateGeneration uint64
}

// Manager tracks per-session fields of interest, maintains a spatial hash
// grid of all entities, and computes per-session visibility diffs each tick.
// Generated 3D entities are projected onto X/Z so PosY remains vertical.
// Not thread-safe; all methods must be called from the game tick goroutine.
type Manager struct {
	grid        *Grid
	fois        map[int64]*FOI               // sessionID → FOI
	known       map[int64]map[int64]struct{} // sessionID → set of currently known entity IDs
	globals     map[int64]struct{}           // entity IDs flagged as global
	tracked     map[int64]struct{}           // entity IDs currently in the grid
	alive       map[int64]struct{}           // reused tick-local set of entities seen during UpdateGrid
	diffs       map[int64]*Diff              // reused per-session diff storage; valid until next ComputeDiffs call
	diffScratch map[int64]*diffScratch
}

// NewManager creates a Manager backed by a spatial hash grid with the given cell size.
func NewManager(cellSize float64) *Manager {
	return &Manager{
		grid:        NewGrid(cellSize),
		fois:        make(map[int64]*FOI),
		known:       make(map[int64]map[int64]struct{}),
		globals:     make(map[int64]struct{}),
		tracked:     make(map[int64]struct{}),
		alive:       make(map[int64]struct{}),
		diffs:       make(map[int64]*Diff),
		diffScratch: make(map[int64]*diffScratch),
	}
}

// interestPosition returns the 2D plane coordinates used for field-of-interest
// checks. 3D entities are projected onto X/Z to match Unity-style Y-up worlds.
func interestPosition(e registry.Entity) (float64, float64) {
	if spatial, ok := e.(registry.Spatial3DEntity); ok {
		x, _, z := spatial.Position3D()
		return float64(x), float64(z)
	}
	x, y := e.Position()
	return float64(x), float64(y)
}

// AssignFOI associates a session with a circular field of interest centred on
// the given entity. The margin provides hysteresis: entities must exceed
// radius+margin to exit the FOI, preventing boundary flicker.
func (m *Manager) AssignFOI(sessionID, entityID int64, radius, margin float64) {
	m.fois[sessionID] = &FOI{
		EntityID: entityID,
		Radius:   radius,
		Margin:   margin,
	}
	if _, ok := m.known[sessionID]; !ok {
		m.known[sessionID] = make(map[int64]struct{})
	}
}

// RemoveFOI removes a session's field of interest and clears its known set.
func (m *Manager) RemoveFOI(sessionID int64) {
	delete(m.fois, sessionID)
	delete(m.known, sessionID)
	delete(m.diffs, sessionID)
	delete(m.diffScratch, sessionID)
}

// HasFOI reports whether a session has an assigned field of interest.
func (m *Manager) HasFOI(sessionID int64) bool {
	_, ok := m.fois[sessionID]
	return ok
}

// Known returns the set of entity IDs currently known to a session.
// Returns nil if the session has no FOI.
func (m *Manager) Known(sessionID int64) map[int64]struct{} {
	return m.known[sessionID]
}

// UpdateGrid synchronises the spatial grid with the current registry state.
// Inserts new entities, removes stale ones, and moves existing ones.
func (m *Manager) UpdateGrid(reg *registry.Registry) {
	all := reg.All()

	alive := m.alive
	clear(alive)
	for _, e := range all {
		id := e.EntityID()
		alive[id] = struct{}{}

		if e.IsGlobal() {
			m.globals[id] = struct{}{}
			m.grid.Remove(id)
			delete(m.tracked, id)
			continue
		}

		delete(m.globals, id)
		x, y := interestPosition(e)
		if m.grid.Has(id) {
			m.grid.Move(id, x, y)
		} else {
			m.grid.Insert(id, x, y)
		}
		m.tracked[id] = struct{}{}
	}

	for id := range m.tracked {
		if _, ok := alive[id]; !ok {
			m.grid.Remove(id)
			delete(m.tracked, id)
		}
	}
	for id := range m.globals {
		if _, ok := alive[id]; !ok {
			delete(m.globals, id)
		}
	}
}

// ComputeDiffs returns the per-session interest Diff for every session that has
// an assigned FOI. It also updates the internal known sets. The returned Diff
// values are reused between calls, so callers must consume them before the
// next ComputeDiffs invocation.
func (m *Manager) ComputeDiffs() map[int64]*Diff {
	diffs := m.diffs
	clear(diffs)

	for sessionID, foi := range m.fois {
		knownSet := m.known[sessionID]
		scratch := m.diffFor(sessionID)
		diff := &scratch.diff

		foiPos, ok := m.grid.positions[foi.EntityID]
		if !ok {
			if _, isGlobal := m.globals[foi.EntityID]; isGlobal {
				// anchor entity removed or global-only; can't compute spatial diff
			}
			m.appendGlobals(knownSet, diff)
			diffs[sessionID] = diff
			continue
		}

		candidates := m.grid.QueryInto(foiPos.x, foiPos.y, foi.Radius+foi.Margin, scratch.queryBuf)
		scratch.queryBuf = candidates
		candidateMarks := scratch.candidateMarks
		candidateGeneration := scratch.nextCandidateGeneration()

		enterR2 := foi.Radius * foi.Radius
		exitR2 := (foi.Radius + foi.Margin) * (foi.Radius + foi.Margin)

		for _, id := range candidates {
			candidateMarks[id] = candidateGeneration
			p := m.grid.positions[id]
			dx := p.x - foiPos.x
			dy := p.y - foiPos.y
			d2 := dx*dx + dy*dy

			if _, wasKnown := knownSet[id]; wasKnown {
				if d2 <= exitR2 {
					diff.Stayed = append(diff.Stayed, id)
				} else {
					diff.Exited = append(diff.Exited, id)
					delete(knownSet, id)
				}
			} else {
				if d2 <= enterR2 {
					diff.Entered = append(diff.Entered, id)
					knownSet[id] = struct{}{}
				}
			}
		}

		if scratch.shouldCompactCandidateMarks(len(candidates), len(knownSet)) {
			scratch.compactCandidateMarks(candidates, candidateGeneration)
			candidateMarks = scratch.candidateMarks
		}

		for id := range knownSet {
			if _, isGlobal := m.globals[id]; isGlobal {
				continue
			}
			stamp, inCandidates := candidateMarks[id]
			if !inCandidates || stamp != candidateGeneration {
				diff.Exited = append(diff.Exited, id)
				delete(knownSet, id)
			}
		}

		m.appendGlobals(knownSet, diff)
		diffs[sessionID] = diff
	}

	return diffs
}

// diffFor returns the reusable per-session scratch for sessionID, resetting all
// per-tick state. Its Diff field is only valid until the next ComputeDiffs call.
func (m *Manager) diffFor(sessionID int64) *diffScratch {
	scratch, ok := m.diffScratch[sessionID]
	if !ok {
		scratch = &diffScratch{candidateMarks: make(map[int64]uint64)}
		m.diffScratch[sessionID] = scratch
	}
	scratch.diff.Entered = scratch.diff.Entered[:0]
	scratch.diff.Stayed = scratch.diff.Stayed[:0]
	scratch.diff.Exited = scratch.diff.Exited[:0]
	scratch.queryBuf = scratch.queryBuf[:0]
	m.diffs[sessionID] = &scratch.diff
	return scratch
}

// nextCandidateGeneration advances the scratch generation used for candidate
// membership stamps. uint64 wraps after roughly 29 billion years at 20 tps, so
// no explicit overflow handling is needed.
func (s *diffScratch) nextCandidateGeneration() uint64 {
	s.candidateGeneration++
	return s.candidateGeneration
}

// shouldCompactCandidateMarks reports whether the historical candidate mark map
// has grown much larger than the current working set and should be rebuilt.
func (s *diffScratch) shouldCompactCandidateMarks(candidateCount, knownCount int) bool {
	activeSize := candidateCount
	if knownCount > activeSize {
		activeSize = knownCount
	}
	if activeSize < 1 {
		activeSize = 1
	}
	return len(s.candidateMarks) > candidateMarksCompactionMinSize &&
		len(s.candidateMarks) > activeSize*candidateMarksCompactionFactor
}

// compactCandidateMarks rebuilds the candidate mark map so long-lived sessions
// do not retain IDs from old churn-heavy visibility sets indefinitely.
func (s *diffScratch) compactCandidateMarks(candidates []int64, generation uint64) {
	marks := make(map[int64]uint64, len(candidates))
	for _, id := range candidates {
		marks[id] = generation
	}
	s.candidateMarks = marks
}

// appendGlobals adds global entities to the diff. Newly seen globals are
// added to Entered; already-known globals go to Stayed. Globals that no
// longer exist are moved to Exited.
func (m *Manager) appendGlobals(knownSet map[int64]struct{}, diff *Diff) {
	for id := range m.globals {
		if _, wasKnown := knownSet[id]; wasKnown {
			diff.Stayed = append(diff.Stayed, id)
		} else {
			diff.Entered = append(diff.Entered, id)
			knownSet[id] = struct{}{}
		}
	}

	for id := range knownSet {
		if _, isGlobal := m.globals[id]; !isGlobal {
			continue
		}
		if _, stillExists := m.globals[id]; !stillExists {
			diff.Exited = append(diff.Exited, id)
			delete(knownSet, id)
		}
	}
}

// IsGlobal reports whether the given entity ID is flagged as global.
func (m *Manager) IsGlobal(entityID int64) bool {
	_, ok := m.globals[entityID]
	return ok
}
