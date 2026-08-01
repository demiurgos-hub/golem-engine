package golem

import (
	"fmt"
)

// avatarSpawnPending marks a session slot reserved while SpawnAvatar is creating
// the entity / FOI. Entity IDs from the server counter are always positive.
const avatarSpawnPending int64 = -1

// AvatarOptions configures optional FOI assignment when spawning a session avatar.
type AvatarOptions struct {
	// FOIRadius is the interest radius centred on the avatar entity.
	// When > 0, SpawnAvatar assigns FOI (requires CellSize > 0). When 0, no FOI
	// is assigned. Negative values are rejected.
	FOIRadius float64
	// FOIMargin is the hysteresis margin passed to AssignFOI when FOIRadius > 0.
	FOIMargin float64
}

// SpawnAvatar registers e as sess's avatar: CreateEntity(e, sess.ID), optional FOI
// when opts.FOIRadius > 0, and session↔entity avatar indexes.
//
// Duplicate calls for a session that already has a live avatar return an error;
// a prior avatar is not replaced. Partial failures roll back the entity, FOI, and
// any reserved index slot where possible. FOI updates go through AssignFOI /
// RemoveFOI (interestMu); avatar indexes use avatarMu. Lock order when both are
// needed: interestMu before avatarMu.
func (s *Server) SpawnAvatar(sess *Session, e Entity, opts AvatarOptions) error {
	if sess == nil {
		return fmt.Errorf("golem: SpawnAvatar requires a non-nil session")
	}
	if opts.FOIRadius < 0 {
		return fmt.Errorf("golem: SpawnAvatar FOIRadius must be >= 0")
	}

	if err := s.reserveAvatarSlot(sess.ID); err != nil {
		return err
	}

	if err := s.CreateEntity(e, sess.ID); err != nil {
		s.releaseAvatarReservation(sess.ID)
		return err
	}
	entityID := e.EntityID()

	foiAssigned := false
	if opts.FOIRadius > 0 {
		if s.interest == nil {
			s.reg.DeleteEntity(entityID)
			s.releaseAvatarReservation(sess.ID)
			return fmt.Errorf("golem: SpawnAvatar with FOIRadius > 0 requires interest management (CellSize > 0)")
		}
		s.AssignFOI(sess.ID, entityID, opts.FOIRadius, opts.FOIMargin)
		foiAssigned = true
	}

	if err := s.commitAvatar(sess.ID, entityID); err != nil {
		if foiAssigned {
			s.RemoveFOI(sess.ID)
		}
		s.reg.DeleteEntity(entityID)
		s.releaseAvatarReservation(sess.ID)
		return err
	}
	return nil
}

// Avatar returns the avatar entity bound to sessionID, if any and still registered.
func (s *Server) Avatar(sessionID int64) (Entity, bool) {
	s.avatarMu.Lock()
	entityID, ok := s.sessionAvatar[sessionID]
	s.avatarMu.Unlock()
	if !ok || entityID == avatarSpawnPending {
		return nil, false
	}
	return s.reg.Get(entityID)
}

// AvatarOf returns the avatar for sessionID asserted to type T.
// Returns false when there is no avatar or the live entity is not of type T.
func AvatarOf[T Entity](s *Server, sessionID int64) (T, bool) {
	var zero T
	e, ok := s.Avatar(sessionID)
	if !ok {
		return zero, false
	}
	typed, ok := e.(T)
	if !ok {
		return zero, false
	}
	return typed, true
}

// reserveAvatarSlot claims sessionID in the avatar index, rejecting a live avatar
// or an in-progress SpawnAvatar. Stale indexes (entity already gone) are cleared.
func (s *Server) reserveAvatarSlot(sessionID int64) error {
	for {
		s.avatarMu.Lock()
		existingID, ok := s.sessionAvatar[sessionID]
		if !ok {
			s.sessionAvatar[sessionID] = avatarSpawnPending
			s.avatarMu.Unlock()
			return nil
		}
		if existingID == avatarSpawnPending {
			s.avatarMu.Unlock()
			return fmt.Errorf("golem: session %d avatar spawn already in progress", sessionID)
		}
		s.avatarMu.Unlock()

		if _, alive := s.reg.Get(existingID); alive {
			return fmt.Errorf("golem: session %d already has avatar entity %d", sessionID, existingID)
		}

		// Clear stale mapping, then retry (another SpawnAvatar may have won).
		s.avatarMu.Lock()
		if id, still := s.sessionAvatar[sessionID]; still && id == existingID {
			delete(s.sessionAvatar, sessionID)
			delete(s.avatarSession, existingID)
		}
		s.avatarMu.Unlock()
	}
}

// releaseAvatarReservation clears a pending spawn reservation for sessionID.
func (s *Server) releaseAvatarReservation(sessionID int64) {
	s.avatarMu.Lock()
	defer s.avatarMu.Unlock()
	if id, ok := s.sessionAvatar[sessionID]; ok && id == avatarSpawnPending {
		delete(s.sessionAvatar, sessionID)
	}
}

// commitAvatar binds sessionID to entityID after a successful spawn.
func (s *Server) commitAvatar(sessionID, entityID int64) error {
	s.avatarMu.Lock()
	defer s.avatarMu.Unlock()
	id, ok := s.sessionAvatar[sessionID]
	if !ok || id != avatarSpawnPending {
		return fmt.Errorf("golem: session %d avatar reservation lost", sessionID)
	}
	if other, taken := s.avatarSession[entityID]; taken && other != sessionID {
		return fmt.Errorf("golem: entity %d is already the avatar for session %d", entityID, other)
	}
	s.sessionAvatar[sessionID] = entityID
	s.avatarSession[entityID] = sessionID
	return nil
}

// clearAvatarByEntity removes avatar index entries for entityID in O(1).
// Safe under concurrent SpawnAvatar / Avatar / disconnect; does not touch the registry.
func (s *Server) clearAvatarByEntity(entityID int64) {
	s.avatarMu.Lock()
	defer s.avatarMu.Unlock()
	sessionID, ok := s.avatarSession[entityID]
	if !ok {
		return
	}
	delete(s.avatarSession, entityID)
	if id, ok := s.sessionAvatar[sessionID]; ok && id == entityID {
		delete(s.sessionAvatar, sessionID)
	}
}

// takeAvatarBySession removes and returns the avatar entity ID for sessionID.
func (s *Server) takeAvatarBySession(sessionID int64) (entityID int64, ok bool) {
	s.avatarMu.Lock()
	defer s.avatarMu.Unlock()
	entityID, ok = s.sessionAvatar[sessionID]
	if !ok || entityID == avatarSpawnPending {
		return 0, false
	}
	delete(s.sessionAvatar, sessionID)
	delete(s.avatarSession, entityID)
	return entityID, true
}

// cleanupAvatarOnDisconnect runs after the user OnDisconnect hook: RemoveFOI,
// then delete any avatar still bound to the session (including a replacement
// spawned inside the callback) and clear indexes. Lock order: interestMu
// (via RemoveFOI) before avatarMu (via takeAvatarBySession). Does not hold
// either mutex across registry hooks.
func (s *Server) cleanupAvatarOnDisconnect(sessionID int64) {
	if s.interest != nil {
		s.RemoveFOI(sessionID)
	}

	entityID, had := s.takeAvatarBySession(sessionID)
	if had {
		// Indexes already cleared; delete via registry to avoid redundant clear.
		s.reg.DeleteEntity(entityID)
	}
}
