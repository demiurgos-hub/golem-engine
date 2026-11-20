package golem

import (
	"errors"
	"fmt"

	"github.com/demiurgos-hub/golem-engine/golem/registry"
)

// SpaceID identifies a separate coordinate space. Every entity belongs to one
// space; space 0 is the default and the only space a single-space game uses.
type SpaceID = registry.SpaceID

// EntitySpace returns the space of a published or provisional entity, or
// (0, false) when the entity is unknown.
func (s *Server) EntitySpace(id int64) (SpaceID, bool) {
	return s.reg.Space(id)
}

// SetEntitySpace moves a published entity to space. It is the low-level
// mover for plain entities: it changes only the entity's space and reports the
// change once in the next ReplicationFrame.Moved. Several moves of one entity
// between flushes coalesce, and moving to the current space is a no-op.
//
// Call before Run or on the tick goroutine, outside physics callbacks, active
// frames and restoration; use Post from a contact callback. Entities attached
// to physics or navigation, or being deleted, are rejected.
func (s *Server) SetEntitySpace(id int64, space SpaceID) error {
	s.lifeMu.Lock()
	stopping := s.life == lifecycleStopping || s.life == lifecycleStopped
	s.lifeMu.Unlock()
	if stopping {
		return ErrServerNotRunning
	}
	if ps := s.physics; ps != nil {
		if err := ps.mutable(); err != nil {
			return fmt.Errorf("golem: SetEntitySpace: %w", err)
		}
		if ps.agents[id] != nil {
			return fmt.Errorf("golem: SetEntitySpace: entity %d is attached to physics", id)
		}
	}
	if n := s.navigation; n != nil {
		if n.restoring || n.restoreErr != nil || n.frame != nil {
			return errors.New("golem: SetEntitySpace is unavailable during navigation frames or restoration")
		}
		if n.deleting[id] {
			return fmt.Errorf("golem: SetEntitySpace: entity %d is being deleted", id)
		}
		if n.agents[id] != nil {
			return fmt.Errorf("golem: SetEntitySpace: entity %d is attached to navigation", id)
		}
	}
	if !s.reg.SetSpace(id, space) {
		return fmt.Errorf("golem: SetEntitySpace: entity %d is not published", id)
	}
	return nil
}
