package golem

import (
	"fmt"

	"github.com/demiurgos-hub/golem-engine/golem/registry"
)

// ownershipRefresh tracks a coalesced confidentiality transition for one entity
// until the next replication pass. clearOwner is the pre-change owner that may
// still hold private client state; newOwner is the final owner after coalescing.
type ownershipRefresh struct {
	clearOwner int64
	newOwner   int64
}

// sessionOwnsEntity reports whether sessionID is the current owner of entityID.
func (s *Server) sessionOwnsEntity(sessionID, entityID int64) bool {
	ownerSID, owned := s.reg.Owner(entityID)
	return owned && ownerSID == sessionID
}

// entityIsOwnerScoped reports whether e implements OwnerScopedEntity.
func entityIsOwnerScoped(e Entity) bool {
	_, ok := e.(registry.OwnerScopedEntity)
	return ok
}

// hasOwnerScopedEntities reports whether any live entity needs recipient-specific
// public payloads.
func hasOwnerScopedEntities(live []Entity) bool {
	for _, e := range live {
		if entityIsOwnerScoped(e) {
			return true
		}
	}
	return false
}

// queueOwnershipRefreshLocked records a SetOwner confidentiality transition.
// Caller must hold interestMu. Same-tick A→…→A cancels the pending refresh so
// intermediate owners never receive private state and A is not cleared.
func (s *Server) queueOwnershipRefreshLocked(entityID, oldOwner, newOwner int64) {
	if oldOwner == newOwner {
		return
	}
	if s.ownershipRefresh == nil {
		s.ownershipRefresh = make(map[int64]ownershipRefresh)
	}
	pending, ok := s.ownershipRefresh[entityID]
	if !ok {
		pending.clearOwner = oldOwner
	}
	pending.newOwner = newOwner
	if pending.clearOwner == pending.newOwner {
		delete(s.ownershipRefresh, entityID)
		return
	}
	s.ownershipRefresh[entityID] = pending
}

// snapshotOwnershipRefreshLocked copies and clears the pending ownership
// refresh queue. Caller must hold interestMu.
func (s *Server) snapshotOwnershipRefreshLocked() map[int64]ownershipRefresh {
	if len(s.ownershipRefresh) == 0 {
		return nil
	}
	out := s.ownershipRefresh
	s.ownershipRefresh = make(map[int64]ownershipRefresh)
	return out
}

// hasPendingOwnershipRefreshLocked reports whether any ownership refresh is
// queued. Caller must hold interestMu.
func (s *Server) hasPendingOwnershipRefreshLocked() bool {
	return len(s.ownershipRefresh) > 0
}

// fullUpdateForSession returns authoritative FullUpdate for the owner (and for
// entities without owner-only vars), or PublicFullUpdate for non-owners.
func (s *Server) fullUpdateForSession(e Entity, sessionID int64) ([]byte, error) {
	if scoped, ok := e.(registry.OwnerScopedEntity); ok && !s.sessionOwnsEntity(sessionID, e.EntityID()) {
		return scoped.PublicFullUpdate()
	}
	return e.FullUpdate()
}

// eventualChangeForSession adapts an authoritative eventual change for a
// recipient. ok is false when a non-owner public mask is empty (send nothing).
func (s *Server) eventualChangeForSession(sessionID int64, ch eventualStateChange) (eventualStateChange, bool) {
	e, ok := s.reg.Get(ch.id)
	if !ok {
		return ch, true
	}
	scoped, hasScope := e.(registry.OwnerScopedEntity)
	if !hasScope || s.sessionOwnsEntity(sessionID, ch.id) {
		return ch, true
	}
	if ch.full {
		return eventualStateChange{id: ch.id, full: true, public: true}, true
	}
	pubMask := scoped.PublicReplicationMask(ch.mask)
	if pubMask == 0 {
		return eventualStateChange{}, false
	}
	return eventualStateChange{id: ch.id, mask: pubMask, public: true}, true
}

// streamDeltaForSession returns the wrapped stream delta for sessionID.
// ok is false when nothing should be sent (empty public mask).
func (s *Server) streamDeltaForSession(sessionID, entityID int64, authDelta []byte, authCache, publicCache map[int64][]byte) ([]byte, bool, error) {
	e, ok := s.reg.Get(entityID)
	if !ok {
		return nil, false, nil
	}
	scoped, hasScope := e.(registry.OwnerScopedEntity)
	if !hasScope || s.sessionOwnsEntity(sessionID, entityID) {
		if authDelta == nil {
			return nil, false, nil
		}
		if wrapped, hit := authCache[entityID]; hit {
			return wrapped, true, nil
		}
		wrapped := s.listener.Wrap(authDelta)
		authCache[entityID] = wrapped
		return wrapped, true, nil
	}
	if wrapped, ok := publicCache[entityID]; ok {
		if wrapped == nil {
			return nil, false, nil
		}
		return wrapped, true, nil
	}
	deltaEnt, ok := e.(registry.ReplicationDeltaEntity)
	if !ok {
		raw, err := scoped.PublicFullUpdate()
		if err != nil {
			return nil, false, fmt.Errorf("public full for entity %d: %w", entityID, err)
		}
		wrapped := s.listener.Wrap(raw)
		publicCache[entityID] = wrapped
		return wrapped, true, nil
	}
	pubMask := scoped.PublicReplicationMask(deltaEnt.LastFlushMask())
	if pubMask == 0 {
		publicCache[entityID] = nil
		return nil, false, nil
	}
	raw, err := deltaEnt.MarshalDeltaMask(pubMask)
	if err != nil {
		return nil, false, fmt.Errorf("public delta for entity %d: %w", entityID, err)
	}
	if raw == nil {
		publicCache[entityID] = nil
		return nil, false, nil
	}
	wrapped := s.listener.Wrap(raw)
	publicCache[entityID] = wrapped
	return wrapped, true, nil
}

// wrappedFullForSession returns a wrapped full-state frame for sessionID,
// caching authoritative and public variants separately.
func (s *Server) wrappedFullForSession(
	sessionID, entityID int64,
	spawnData map[int64][]byte,
	authCache, publicCache map[int64][]byte,
) ([]byte, bool, error) {
	e, ok := s.reg.Get(entityID)
	if !ok {
		return nil, false, nil
	}
	if scoped, ok := e.(registry.OwnerScopedEntity); ok && !s.sessionOwnsEntity(sessionID, entityID) {
		if wrapped, hit := publicCache[entityID]; hit {
			return wrapped, true, nil
		}
		raw, err := scoped.PublicFullUpdate()
		if err != nil {
			return nil, false, fmt.Errorf("public full update for entity %d: %w", entityID, err)
		}
		wrapped := s.listener.Wrap(raw)
		publicCache[entityID] = wrapped
		return wrapped, true, nil
	}
	if wrapped, hit := authCache[entityID]; hit {
		return wrapped, true, nil
	}
	if raw, ok := spawnData[entityID]; ok {
		wrapped := s.listener.Wrap(raw)
		authCache[entityID] = wrapped
		return wrapped, true, nil
	}
	raw, err := e.FullUpdate()
	if err != nil {
		return nil, false, fmt.Errorf("full update for entity %d: %w", entityID, err)
	}
	wrapped := s.listener.Wrap(raw)
	authCache[entityID] = wrapped
	return wrapped, true, nil
}

// appendOwnershipRefreshFrames appends public/auth full-state frames for a
// coalesced ownership transition when the recipient still knows the entity.
// Only stayed recipients are refreshed: entered already got the correct
// owner/public full state, and exited receive EntityRemoved instead.
func (s *Server) appendOwnershipRefreshFrames(
	sessionID int64,
	pending map[int64]ownershipRefresh,
	stayed []int64,
	authCache, publicCache map[int64][]byte,
	streamFrames [][]byte,
) ([][]byte, error) {
	if len(pending) == 0 {
		return streamFrames, nil
	}
	stayedSet := int64Set(stayed)
	for entityID, ref := range pending {
		if _, ok := stayedSet[entityID]; !ok {
			continue
		}
		e, ok := s.reg.Get(entityID)
		if !ok {
			continue
		}
		scoped, ok := e.(registry.OwnerScopedEntity)
		if !ok {
			continue
		}
		if ref.clearOwner == sessionID {
			// Full public clear supersedes any pending/in-flight private deltas.
			s.clearEventualEntity(sessionID, entityID)
			if wrapped, hit := publicCache[entityID]; hit {
				streamFrames = append(streamFrames, wrapped)
			} else {
				raw, err := scoped.PublicFullUpdate()
				if err != nil {
					return streamFrames, fmt.Errorf("ownership clear public full for entity %d: %w", entityID, err)
				}
				wrapped := s.listener.Wrap(raw)
				publicCache[entityID] = wrapped
				streamFrames = append(streamFrames, wrapped)
			}
		}
		if ref.newOwner == sessionID && ref.newOwner != 0 {
			// Authoritative grant supersedes pending public/redacted deltas.
			s.clearEventualEntity(sessionID, entityID)
			if wrapped, hit := authCache[entityID]; hit {
				streamFrames = append(streamFrames, wrapped)
				continue
			}
			raw, err := e.FullUpdate()
			if err != nil {
				return streamFrames, fmt.Errorf("ownership grant full for entity %d: %w", entityID, err)
			}
			wrapped := s.listener.Wrap(raw)
			authCache[entityID] = wrapped
			streamFrames = append(streamFrames, wrapped)
		}
	}
	return streamFrames, nil
}

func int64Set(ids []int64) map[int64]struct{} {
	if len(ids) == 0 {
		return nil
	}
	out := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		out[id] = struct{}{}
	}
	return out
}

// sessionKnowsEntityLocked reports whether sessionID currently knows entityID.
// Caller must hold interestMu.
func (s *Server) sessionKnowsEntityLocked(sessionID, entityID int64) bool {
	var known map[int64]struct{}
	if s.interest != nil {
		known = s.interest.Known(sessionID)
	} else {
		known = s.broadcastKnown[sessionID]
	}
	if known == nil {
		return false
	}
	_, ok := known[entityID]
	return ok
}

// sessionKnowsEntity reports whether sessionID currently knows entityID.
func (s *Server) sessionKnowsEntity(sessionID, entityID int64) bool {
	s.interestMu.Lock()
	defer s.interestMu.Unlock()
	return s.sessionKnowsEntityLocked(sessionID, entityID)
}
