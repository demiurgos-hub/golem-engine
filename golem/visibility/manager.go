// Package visibility provides a lower-level, non-thread-safe policy manager for
// named visibility groups that gate entity replication.
//
// Empty entity group means the entity is public. Server owns concurrency safety;
// this package does not import golem, networking, registry, or interest code.
package visibility

// Manager owns entityID→group and sessionID→groups membership exactly once.
// Not thread-safe; the owning Server must serialize all access.
type Manager struct {
	entityGroup   map[int64]string              // entityID → group name (absent/empty = public)
	sessionGroups map[int64]map[string]struct{} // sessionID → set of group names
	groupedCount  int                           // number of entities with a non-empty group
	generation    uint64                        // bumped on every policy mutation
}

// NewManager creates an empty visibility policy manager.
func NewManager() *Manager {
	return &Manager{
		entityGroup:   make(map[int64]string),
		sessionGroups: make(map[int64]map[string]struct{}),
	}
}

// Generation returns a monotonic counter bumped on every policy mutation.
// Callers use it to detect concurrent policy changes across unlocked work.
func (m *Manager) Generation() uint64 {
	return m.generation
}

func (m *Manager) bump() {
	m.generation++
}

// SetEntityGroup assigns entityID to group. An empty group clears the assignment
// and makes the entity public. Returns the previous group name (empty if public).
func (m *Manager) SetEntityGroup(entityID int64, group string) (previous string) {
	previous = m.entityGroup[entityID]
	if group == "" {
		if previous != "" {
			delete(m.entityGroup, entityID)
			m.groupedCount--
			m.bump()
		}
		return previous
	}
	if previous == group {
		return previous
	}
	if previous == "" {
		m.groupedCount++
	}
	m.entityGroup[entityID] = group
	m.bump()
	return previous
}

// EntityGroup returns the group assigned to entityID, or "" if public.
func (m *Manager) EntityGroup(entityID int64) string {
	return m.entityGroup[entityID]
}

// JoinGroup adds sessionID to the named group. Empty group names are ignored.
func (m *Manager) JoinGroup(sessionID int64, group string) {
	if group == "" {
		return
	}
	groups, ok := m.sessionGroups[sessionID]
	if !ok {
		groups = make(map[string]struct{})
		m.sessionGroups[sessionID] = groups
	}
	if _, exists := groups[group]; exists {
		return
	}
	groups[group] = struct{}{}
	m.bump()
}

// LeaveGroup removes sessionID from the named group.
func (m *Manager) LeaveGroup(sessionID int64, group string) {
	groups, ok := m.sessionGroups[sessionID]
	if !ok {
		return
	}
	if _, exists := groups[group]; !exists {
		return
	}
	delete(groups, group)
	if len(groups) == 0 {
		delete(m.sessionGroups, sessionID)
	}
	m.bump()
}

// RemoveSession clears all group memberships for sessionID.
func (m *Manager) RemoveSession(sessionID int64) {
	if _, ok := m.sessionGroups[sessionID]; !ok {
		return
	}
	delete(m.sessionGroups, sessionID)
	m.bump()
}

// RemoveEntity clears any group assignment for entityID.
func (m *Manager) RemoveEntity(entityID int64) {
	if prev, ok := m.entityGroup[entityID]; ok && prev != "" {
		delete(m.entityGroup, entityID)
		m.groupedCount--
		m.bump()
		return
	}
	if _, ok := m.entityGroup[entityID]; ok {
		delete(m.entityGroup, entityID)
		m.bump()
	}
}

// Allows reports whether sessionID may receive entityID under the current policy.
// Public entities (empty/absent group) are always allowed. Grouped entities require
// membership in that entity's group.
func (m *Manager) Allows(sessionID, entityID int64) bool {
	group, ok := m.entityGroup[entityID]
	if !ok || group == "" {
		return true
	}
	groups := m.sessionGroups[sessionID]
	if groups == nil {
		return false
	}
	_, member := groups[group]
	return member
}

// HasGroupedEntities reports whether any live policy assigns a non-empty group.
// Used by Server to choose the blind broadcast fast path when false.
func (m *Manager) HasGroupedEntities() bool {
	return m.groupedCount > 0
}

// InGroup reports whether sessionID is a member of group.
func (m *Manager) InGroup(sessionID int64, group string) bool {
	if group == "" {
		return false
	}
	groups := m.sessionGroups[sessionID]
	if groups == nil {
		return false
	}
	_, ok := groups[group]
	return ok
}
