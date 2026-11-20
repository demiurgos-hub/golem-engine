package golem

import (
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
// Replication treats the entity as part of its new space from the next pass:
// sessions in the old space see it exit and sessions in the new space see it
// enter, except for sessions that move along with it (see MoveToSpace).
//
// Call before Run or on the tick goroutine, outside physics callbacks, active
// frames and restoration; use Post from a contact callback. Entities attached
// to physics or navigation, or being deleted, are rejected.
func (s *Server) SetEntitySpace(id int64, space SpaceID) error {
	return s.MoveToSpace(SpaceTransfer{To: space, Entities: []int64{id}})
}

// SpaceTransfer describes entities that move to one space together, such as a
// player's avatar with the crate it carries.
type SpaceTransfer struct {
	// To is the destination space.
	To SpaceID
	// Entities lists the published entities to move. Duplicates are rejected.
	Entities []int64
}

// MoveToSpace moves every entity of t to t.To in one step. All entities are
// validated first, with the same rules as SetEntitySpace; when any is rejected
// nothing moves. Moves are reported in the next ReplicationFrame.Moved.
//
// A session whose avatar or FOI anchor moves follows it to the new space on the
// next replication pass. The entities it already knew that moved with it are
// carried: the client keeps them and receives their full state inside the
// space transition instead of a removal and respawn.
func (s *Server) MoveToSpace(t SpaceTransfer) error {
	const op = "MoveToSpace"
	if len(t.Entities) == 1 {
		// Keep SetEntitySpace's historical diagnostics.
		if err := s.checkSpaceMutable("SetEntitySpace"); err != nil {
			return err
		}
		return s.applySpaceMove("SetEntitySpace", t)
	}
	if err := s.checkSpaceMutable(op); err != nil {
		return err
	}
	seen := make(map[int64]struct{}, len(t.Entities))
	for _, id := range t.Entities {
		if _, dup := seen[id]; dup {
			return fmt.Errorf("golem: %s: entity %d is listed twice", op, id)
		}
		seen[id] = struct{}{}
	}
	return s.applySpaceMove(op, t)
}

// checkSpaceMutable rejects space changes while stopping, during physics steps
// and callbacks, navigation frames and restoration.
func (s *Server) checkSpaceMutable(op string) error {
	s.lifeMu.Lock()
	stopping := s.life == lifecycleStopping || s.life == lifecycleStopped
	s.lifeMu.Unlock()
	if stopping {
		return ErrServerNotRunning
	}
	if ps := s.physics; ps != nil {
		if err := ps.mutable(); err != nil {
			return fmt.Errorf("golem: %s: %w", op, err)
		}
	}
	if n := s.navigation; n != nil && (n.restoring || n.restoreErr != nil || n.frame != nil) {
		return fmt.Errorf("golem: %s is unavailable during navigation frames or restoration", op)
	}
	return nil
}

// applySpaceMove validates every entity of t and then moves them together.
// interestMu makes the batch atomic for session-space readers.
func (s *Server) applySpaceMove(op string, t SpaceTransfer) error {
	for _, id := range t.Entities {
		if ps := s.physics; ps != nil && ps.agents[id] != nil {
			return fmt.Errorf("golem: %s: entity %d is attached to physics", op, id)
		}
		if n := s.navigation; n != nil {
			if n.deleting[id] {
				return fmt.Errorf("golem: %s: entity %d is being deleted", op, id)
			}
			if n.agents[id] != nil {
				return fmt.Errorf("golem: %s: entity %d is attached to navigation", op, id)
			}
		}
	}
	s.interestMu.Lock()
	bad, moved := s.reg.SetSpaces(t.Entities, t.To)
	s.interestMu.Unlock()
	if !moved {
		return fmt.Errorf("golem: %s: entity %d is not published", op, bad)
	}
	return nil
}

// SetSessionSpace sets the space of a session that has neither an FOI anchor
// nor an avatar, such as a spectator or a client in a menu. The session's
// effective space comes from, in order: its FOI anchor, its avatar, this
// explicit space, and finally space 0. Space 0 clears the explicit space. A
// session with an FOI whose anchor is missing keeps its last space; the
// avatar or explicit space only seeds it when the FOI is assigned.
//
// The change is announced to the client on the next replication pass as a
// space transition. Concurrency-safe; the setting is dropped on disconnect.
func (s *Server) SetSessionSpace(sessionID int64, space SpaceID) {
	s.interestMu.Lock()
	defer s.interestMu.Unlock()
	if space == 0 {
		delete(s.sessionSpaceExplicit, sessionID)
		return
	}
	s.sessionSpaceExplicit[sessionID] = space
}

// SessionSpace returns the session's effective space: the space of its FOI
// anchor, else of its avatar, else the space set with SetSessionSpace, else 0.
// The client learns about a change on the next replication pass; until then
// SessionsInSpace still lists the session in its announced space.
func (s *Server) SessionSpace(sessionID int64) SpaceID {
	s.interestMu.Lock()
	defer s.interestMu.Unlock()
	return s.effectiveSessionSpaceLocked(sessionID)
}

// SessionsInSpace returns the connected sessions whose announced space is
// space, that is, the space whose entities they currently replicate. Every
// session starts in space 0 and changes space only through a replication
// space transition, so this matches what each client has been told.
func (s *Server) SessionsInSpace(space SpaceID) []int64 {
	all := s.listener.SessionIDs()
	s.interestMu.Lock()
	defer s.interestMu.Unlock()
	var out []int64
	for _, sid := range all {
		if s.sentSpace[sid] == space {
			out = append(out, sid)
		}
	}
	return out
}

// Space is a lightweight handle to one space of a Server. Handles are cheap;
// two handles for the same ID behave identically.
type Space struct {
	srv *Server
	id  SpaceID
}

// Space returns a handle to space id. The space does not need any entity.
func (s *Server) Space(id SpaceID) *Space {
	return &Space{srv: s, id: id}
}

// ID returns the space's identifier.
func (sp *Space) ID() SpaceID { return sp.id }

// Sessions returns the connected sessions announced in this space; see
// Server.SessionsInSpace.
func (sp *Space) Sessions() []int64 { return sp.srv.SessionsInSpace(sp.id) }

// BroadcastServerEvent sends one marshalled ServerEvent to every session
// announced in this space, on the chosen delivery lane with the same stream
// fallback as Server.BroadcastServerEvent. Sessions in other spaces receive
// nothing. Sessions that disconnect meanwhile are skipped.
func (sp *Space) BroadcastServerEvent(delivery EventDelivery, event []byte) error {
	sessions := sp.Sessions()
	if len(sessions) == 0 {
		return nil
	}
	return sp.srv.listener.SendEventTo(sessions, delivery, event, WrapServerEvent(event))
}
