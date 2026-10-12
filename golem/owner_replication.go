package golem

import (
	"fmt"

	"github.com/demiurgos-hub/golem-engine/golem/registry"
)

type recipientView bool

const (
	authoritativeView recipientView = false
	publicView        recipientView = true
)

type fullFrameCache struct {
	authoritative map[int64][]byte
	public        map[int64][]byte
}

func (s *Server) viewForSession(sessionID int64, e Entity) recipientView {
	return recipientView(entityIsOwnerScoped(e) && !s.sessionOwnsEntity(sessionID, e.EntityID()))
}

func fullUpdateForView(e Entity, view recipientView) ([]byte, error) {
	if view == publicView {
		scoped, ok := e.(registry.OwnerScopedEntity)
		if !ok {
			return nil, fmt.Errorf("missing OwnerScopedEntity")
		}
		return scoped.PublicFullUpdate()
	}
	return e.FullUpdate()
}

// wrappedFullForView caches only immutable bytes, with separate maps for each view.
// Serialization errors retain caller-specific context at the call site.
func (s *Server) wrappedFullForView(e Entity, view recipientView, cache *fullFrameCache) ([]byte, error) {
	frames := cache.authoritative
	if view == publicView {
		frames = cache.public
	}
	if frame, ok := frames[e.EntityID()]; ok {
		return frame, nil
	}
	raw, err := fullUpdateForView(e, view)
	if err != nil {
		return nil, err
	}
	frame := s.listener.Wrap(raw)
	frames[e.EntityID()] = frame
	return frame, nil
}

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
	return fullUpdateForView(e, s.viewForSession(sessionID, e))
}

// eventualChangeForSession adapts an authoritative eventual change for a
// recipient. ok is false when a non-owner public mask is empty (send nothing).
func (s *Server) eventualChangeForSession(sessionID int64, ch eventualStateChange) (eventualStateChange, bool) {
	e, ok := s.reg.Get(ch.id)
	if !ok {
		return ch, true
	}
	if s.viewForSession(sessionID, e) == authoritativeView {
		return ch, true
	}
	if ch.full {
		return eventualStateChange{id: ch.id, full: true, public: true}, true
	}
	scoped := e.(registry.OwnerScopedEntity)
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
	if s.viewForSession(sessionID, e) == authoritativeView {
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
	scoped := e.(registry.OwnerScopedEntity)
	deltaEnt, ok := e.(registry.ReplicationDeltaEntity)
	if !ok {
		raw, err := fullUpdateForView(e, publicView)
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
	view := s.viewForSession(sessionID, e)
	cache := fullFrameCache{authoritative: authCache, public: publicCache}
	if view == authoritativeView {
		if _, hit := authCache[entityID]; !hit {
			if raw, ok := spawnData[entityID]; ok {
				authCache[entityID] = s.listener.Wrap(raw)
			}
		}
	}
	frame, err := s.wrappedFullForView(e, view, &cache)
	if err != nil {
		if view == publicView {
			return nil, false, fmt.Errorf("public full update for entity %d: %w", entityID, err)
		}
		return nil, false, fmt.Errorf("full update for entity %d: %w", entityID, err)
	}
	return frame, true, nil
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
		if !entityIsOwnerScoped(e) {
			continue
		}
		cache := fullFrameCache{authoritative: authCache, public: publicCache}
		if ref.clearOwner == sessionID {
			s.clearEventualEntity(sessionID, entityID)
			frame, err := s.wrappedFullForView(e, publicView, &cache)
			if err != nil {
				return streamFrames, fmt.Errorf("ownership clear public full for entity %d: %w", entityID, err)
			}
			streamFrames = append(streamFrames, frame)
		}
		if ref.newOwner == sessionID && ref.newOwner != 0 {
			s.clearEventualEntity(sessionID, entityID)
			frame, err := s.wrappedFullForView(e, authoritativeView, &cache)
			if err != nil {
				return streamFrames, fmt.Errorf("ownership grant full for entity %d: %w", entityID, err)
			}
			streamFrames = append(streamFrames, frame)
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
