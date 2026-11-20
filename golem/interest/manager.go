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
	Entered []int64          // entity IDs that just became visible
	Stayed  []int64          // entity IDs that remain visible (may or may not be dirty)
	Exited  []int64          // entity IDs that just left visibility
	Space   registry.SpaceID // the session's space for this tick; Entered and Stayed lie inside it
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

// Manager tracks per-session fields of interest, maintains one spatial hash
// grid per space, and computes per-session visibility diffs each tick.
// Generated 3D entities are projected onto X/Z so PosY remains vertical.
//
// Entities in different spaces never see each other: a session's diff only
// considers the grid and globals of the session's space, which is its FOI
// anchor's space (the last resolved space while the anchor is missing). While
// every entity and session stays in space 0, UpdateGrid and ComputeDiffs run
// the single-space loops without any space bookkeeping.
//
// Not thread-safe; all methods must be called from the game tick goroutine.
type Manager struct {
	grid           *Grid                                   // space 0 grid
	grids          map[registry.SpaceID]*Grid              // grids of spaces other than 0, created on demand
	fois           map[int64]*FOI                          // sessionID → FOI
	known          map[int64]map[int64]struct{}            // sessionID → set of currently known entity IDs
	globals        map[int64]struct{}                      // space-0 entity IDs flagged as global
	globalsBySpace map[registry.SpaceID]map[int64]struct{} // globals of spaces other than 0
	entitySpace    map[int64]registry.SpaceID              // non-zero space of each tracked entity or global
	sessionSpace   map[int64]registry.SpaceID              // non-zero last resolved space of FOI sessions
	spaced         bool                                    // the last UpdateGrid left entities outside space 0
	spacedBuf      []registry.SpacedEntity                 // reused AppendSpaced scratch
	removedGlobals map[int64]struct{}                      // globals deleted since last UpdateGrid (reused)
	tracked        map[int64]struct{}                      // entity IDs currently in a grid
	alive          map[int64]struct{}                      // reused tick-local set of entities seen during UpdateGrid
	diffs          map[int64]*Diff                         // reused per-session diff storage; valid until next ComputeDiffs call
	diffScratch    map[int64]*diffScratch
}

// NewManager creates a Manager backed by spatial hash grids with the given cell size.
func NewManager(cellSize float64) *Manager {
	return &Manager{
		grid:           NewGrid(cellSize),
		grids:          make(map[registry.SpaceID]*Grid),
		fois:           make(map[int64]*FOI),
		known:          make(map[int64]map[int64]struct{}),
		globals:        make(map[int64]struct{}),
		globalsBySpace: make(map[registry.SpaceID]map[int64]struct{}),
		entitySpace:    make(map[int64]registry.SpaceID),
		sessionSpace:   make(map[int64]registry.SpaceID),
		removedGlobals: make(map[int64]struct{}),
		tracked:        make(map[int64]struct{}),
		alive:          make(map[int64]struct{}),
		diffs:          make(map[int64]*Diff),
		diffScratch:    make(map[int64]*diffScratch),
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

// RemoveFOI removes a session's field of interest and clears its known set
// and space.
func (m *Manager) RemoveFOI(sessionID int64) {
	delete(m.fois, sessionID)
	delete(m.known, sessionID)
	delete(m.diffs, sessionID)
	delete(m.diffScratch, sessionID)
	delete(m.sessionSpace, sessionID)
}

// HasFOI reports whether a session has an assigned field of interest.
func (m *Manager) HasFOI(sessionID int64) bool {
	_, ok := m.fois[sessionID]
	return ok
}

// SessionFOI returns a copy of the session's field of interest.
func (m *Manager) SessionFOI(sessionID int64) (FOI, bool) {
	foi, ok := m.fois[sessionID]
	if !ok {
		return FOI{}, false
	}
	return *foi, true
}

// SessionSpace returns the space of a session with an FOI as of the last
// ComputeDiffs call (or SetSessionSpace), and 0 for sessions without an FOI.
func (m *Manager) SessionSpace(sessionID int64) registry.SpaceID {
	return m.sessionSpace[sessionID]
}

// SetSessionSpace sets the space a session with an FOI keeps while its anchor
// is missing from every grid and global set. ComputeDiffs replaces it with the
// anchor's space whenever the anchor resolves. It is a no-op for sessions
// without an FOI.
func (m *Manager) SetSessionSpace(sessionID int64, space registry.SpaceID) {
	if _, ok := m.fois[sessionID]; !ok {
		return
	}
	m.setSessionSpace(sessionID, space)
}

func (m *Manager) setSessionSpace(sessionID int64, space registry.SpaceID) {
	if space == 0 {
		delete(m.sessionSpace, sessionID)
		return
	}
	m.sessionSpace[sessionID] = space
}

// Known returns the set of entity IDs currently known to a session.
// Returns nil if the session has no FOI.
func (m *Manager) Known(sessionID int64) map[int64]struct{} {
	return m.known[sessionID]
}

// UpdateGrid synchronises the spatial grids with the current registry state.
// Inserts new entities, removes stale ones, and moves existing ones, including
// between the grids of different spaces. Globals that leave the registry are
// recorded in removedGlobals for the next ComputeDiffs pass so known sessions
// emit Exited even when the FOI anchor is not on a spatial grid. While every
// entity is in space 0 it runs the single-space loop without space lookups.
func (m *Manager) UpdateGrid(reg *registry.Registry) {
	if m.spaced || reg.SpacesInUse() {
		m.updateGridSpaced(reg)
		return
	}
	all := reg.All()
	if reg.SpacesInUse() {
		// An entity left space 0 after the check; its space must not be lost.
		m.updateGridSpaced(reg)
		return
	}

	alive := m.alive
	clear(alive)
	clear(m.removedGlobals)
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
		m.grid.Insert(id, x, y)
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
			m.removedGlobals[id] = struct{}{}
			delete(m.globals, id)
		}
	}
}

// updateGridSpaced is UpdateGrid with per-entity spaces. It reads every entity
// with its space under one registry lock, moves entities between grids and
// global sets when their space changes, and drops empty non-zero grids. Once
// every entity is back in space 0 the next UpdateGrid uses the fast loop.
func (m *Manager) updateGridSpaced(reg *registry.Registry) {
	m.spacedBuf = reg.AppendSpaced(m.spacedBuf[:0])

	alive := m.alive
	clear(alive)
	clear(m.removedGlobals)
	for i := range m.spacedBuf {
		e, space := m.spacedBuf[i].Entity, m.spacedBuf[i].Space
		id := e.EntityID()
		alive[id] = struct{}{}
		prev := m.entitySpace[id]
		_, wasTracked := m.tracked[id]

		if e.IsGlobal() {
			if wasTracked {
				m.removeFromGrid(id, prev)
				delete(m.tracked, id)
			} else if prev != space {
				m.removeGlobal(id, prev)
			}
			m.addGlobal(id, space)
			m.setEntitySpace(id, space)
			continue
		}

		if wasTracked {
			if prev != space {
				m.removeFromGrid(id, prev)
			}
		} else {
			m.removeGlobal(id, prev)
		}
		m.setEntitySpace(id, space)
		x, y := interestPosition(e)
		m.gridFor(space).Insert(id, x, y)
		m.tracked[id] = struct{}{}
	}

	for id := range m.tracked {
		if _, ok := alive[id]; !ok {
			m.removeFromGrid(id, m.entitySpace[id])
			delete(m.tracked, id)
			delete(m.entitySpace, id)
		}
	}
	for id := range m.globals {
		if _, ok := alive[id]; !ok {
			m.removedGlobals[id] = struct{}{}
			delete(m.globals, id)
		}
	}
	for space, set := range m.globalsBySpace {
		for id := range set {
			if _, ok := alive[id]; !ok {
				m.removedGlobals[id] = struct{}{}
				delete(set, id)
				delete(m.entitySpace, id)
			}
		}
		if len(set) == 0 {
			delete(m.globalsBySpace, space)
		}
	}
	for space, g := range m.grids {
		if len(g.positions) == 0 {
			delete(m.grids, space)
		}
	}
	m.spaced = len(m.entitySpace) > 0
}

// gridFor returns the grid of space, creating it on first use.
func (m *Manager) gridFor(space registry.SpaceID) *Grid {
	if space == 0 {
		return m.grid
	}
	g := m.grids[space]
	if g == nil {
		g = NewGrid(m.grid.cellSize)
		m.grids[space] = g
	}
	return g
}

// gridIn returns the grid of space, or nil when no entity has used it.
func (m *Manager) gridIn(space registry.SpaceID) *Grid {
	if space == 0 {
		return m.grid
	}
	return m.grids[space]
}

func (m *Manager) removeFromGrid(id int64, space registry.SpaceID) {
	if g := m.gridIn(space); g != nil {
		g.Remove(id)
	}
}

func (m *Manager) addGlobal(id int64, space registry.SpaceID) {
	if space == 0 {
		m.globals[id] = struct{}{}
		return
	}
	set := m.globalsBySpace[space]
	if set == nil {
		set = make(map[int64]struct{})
		m.globalsBySpace[space] = set
	}
	set[id] = struct{}{}
}

func (m *Manager) removeGlobal(id int64, space registry.SpaceID) {
	if space == 0 {
		delete(m.globals, id)
		return
	}
	if set := m.globalsBySpace[space]; set != nil {
		delete(set, id)
		if len(set) == 0 {
			delete(m.globalsBySpace, space)
		}
	}
}

// globalsIn returns the global set of space; nil when it has none.
func (m *Manager) globalsIn(space registry.SpaceID) map[int64]struct{} {
	if space == 0 {
		return m.globals
	}
	return m.globalsBySpace[space]
}

func (m *Manager) setEntitySpace(id int64, space registry.SpaceID) {
	if space == 0 {
		delete(m.entitySpace, id)
		return
	}
	m.entitySpace[id] = space
}

// anchorSpace resolves the space of an FOI anchor that is on a grid or global.
func (m *Manager) anchorSpace(id int64) (registry.SpaceID, bool) {
	if _, ok := m.tracked[id]; ok {
		return m.entitySpace[id], true
	}
	if _, ok := m.globals[id]; ok {
		return 0, true
	}
	// entitySpace holds only tracked entities and globals.
	space, ok := m.entitySpace[id]
	return space, ok
}

// ComputeDiffs returns the per-session interest Diff for every session that has
// an assigned FOI. It also updates the internal known sets. The returned Diff
// values are reused between calls, so callers must consume them before the
// next ComputeDiffs invocation.
//
// Equivalent to ComputeDiffsFiltered(nil): every spatially/global-visible
// entity is allowed.
func (m *Manager) ComputeDiffs() map[int64]*Diff {
	return m.ComputeDiffsFiltered(nil)
}

// ComputeDiffsFiltered is ComputeDiffs with an optional allow predicate applied
// before Entered/Stayed. When allow is nil, all candidates are allowed. When a
// previously known entity becomes disallowed, it is forced into Exited even if
// it remains inside the FOI. The same predicate applies to global entities:
// grouped globals bypass distance but still require allow(sessionID, entityID).
//
// Known sets are updated to match the filtered decisions. Returned Diff values
// are reused until the next ComputeDiffs / ComputeDiffsFiltered call.
//
// Each session only considers its space (Diff.Space): the space of its FOI
// anchor when the anchor is on a grid or global, otherwise the space it last
// resolved (see SetSessionSpace). Known entities outside that space exit, so
// after every call a session's known set lies inside Diff.Space.
func (m *Manager) ComputeDiffsFiltered(allow func(sessionID, entityID int64) bool) map[int64]*Diff {
	diffs := m.diffs
	clear(diffs)
	spaced := m.spaced || len(m.sessionSpace) > 0

	for sessionID, foi := range m.fois {
		knownSet := m.known[sessionID]
		scratch := m.diffFor(sessionID)
		diff := &scratch.diff

		grid, globals := m.grid, m.globals
		if spaced {
			space := m.sessionSpace[sessionID]
			if anchorSpace, ok := m.anchorSpace(foi.EntityID); ok {
				space = anchorSpace
				m.setSessionSpace(sessionID, space)
			}
			diff.Space = space
			grid, globals = m.gridIn(space), m.globalsIn(space)
		}

		var (
			foiPos pos
			ok     bool
		)
		if grid != nil {
			foiPos, ok = grid.positions[foi.EntityID]
		}
		if !ok {
			// The anchor is missing or global: no spatial diff, globals only.
			if spaced {
				m.exitOtherSpaces(diff.Space, knownSet, diff)
			}
			m.appendGlobalsFiltered(sessionID, globals, knownSet, diff, allow)
			diffs[sessionID] = diff
			continue
		}

		candidates := grid.QueryInto(foiPos.x, foiPos.y, foi.Radius+foi.Margin, scratch.queryBuf)
		scratch.queryBuf = candidates
		candidateMarks := scratch.candidateMarks
		candidateGeneration := scratch.nextCandidateGeneration()

		enterR2 := foi.Radius * foi.Radius
		exitR2 := (foi.Radius + foi.Margin) * (foi.Radius + foi.Margin)

		for _, id := range candidates {
			candidateMarks[id] = candidateGeneration
			p := grid.positions[id]
			dx := p.x - foiPos.x
			dy := p.y - foiPos.y
			d2 := dx*dx + dy*dy

			if _, wasKnown := knownSet[id]; wasKnown {
				if d2 <= exitR2 && entityAllowed(allow, sessionID, id) {
					diff.Stayed = append(diff.Stayed, id)
				} else {
					diff.Exited = append(diff.Exited, id)
					delete(knownSet, id)
				}
			} else {
				if d2 <= enterR2 && entityAllowed(allow, sessionID, id) {
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
			// Globals of this space are handled below; known globals of
			// another space are never candidates and exit here.
			if _, isGlobal := globals[id]; isGlobal {
				continue
			}
			stamp, inCandidates := candidateMarks[id]
			if !inCandidates || stamp != candidateGeneration {
				diff.Exited = append(diff.Exited, id)
				delete(knownSet, id)
			}
		}

		m.appendGlobalsFiltered(sessionID, globals, knownSet, diff, allow)
		diffs[sessionID] = diff
	}

	return diffs
}

// exitOtherSpaces exits known live entities whose space is not space. Deleted
// entities keep their existing exit paths.
func (m *Manager) exitOtherSpaces(space registry.SpaceID, knownSet map[int64]struct{}, diff *Diff) {
	for id := range knownSet {
		if _, alive := m.alive[id]; alive && m.entitySpace[id] != space {
			diff.Exited = append(diff.Exited, id)
			delete(knownSet, id)
		}
	}
}

func entityAllowed(allow func(sessionID, entityID int64) bool, sessionID, entityID int64) bool {
	if allow == nil {
		return true
	}
	return allow(sessionID, entityID)
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
	scratch.diff.Space = 0
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

// appendGlobalsFiltered adds the session space's global entities to the diff
// subject to allow. Newly allowed globals are added to Entered; already-known
// allowed globals go to Stayed. Known globals that are disallowed exit.
// Globals recorded in removedGlobals (deleted since the previous UpdateGrid)
// emit Exited once when still present in knownSet.
func (m *Manager) appendGlobalsFiltered(sessionID int64, globals, knownSet map[int64]struct{}, diff *Diff, allow func(sessionID, entityID int64) bool) {
	for id := range globals {
		if !entityAllowed(allow, sessionID, id) {
			if _, wasKnown := knownSet[id]; wasKnown {
				diff.Exited = append(diff.Exited, id)
				delete(knownSet, id)
			}
			continue
		}
		if _, wasKnown := knownSet[id]; wasKnown {
			diff.Stayed = append(diff.Stayed, id)
		} else {
			diff.Entered = append(diff.Entered, id)
			knownSet[id] = struct{}{}
		}
	}

	for id := range m.removedGlobals {
		if _, wasKnown := knownSet[id]; wasKnown {
			diff.Exited = append(diff.Exited, id)
			delete(knownSet, id)
		}
	}
}

// IsGlobal reports whether the given entity ID is flagged as global in any space.
func (m *Manager) IsGlobal(entityID int64) bool {
	if _, ok := m.globals[entityID]; ok {
		return true
	}
	_, ok := m.globalsBySpace[m.entitySpace[entityID]][entityID]
	return ok
}
