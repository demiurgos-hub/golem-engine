package golem

import (
	"fmt"

	"github.com/demiurgos-hub/golem-engine/golem/interest"
	golemnet "github.com/demiurgos-hub/golem-engine/golem/net"
)

// sessionSpacePlan is one session's space decision for a replication pass. The
// zero value (space 0, no transition, not gated) is the single-space default.
type sessionSpacePlan struct {
	from, space SpaceID
	// transition is non-zero when this pass announces space to the client.
	transition uint64
	// gated holds incremental deltas on the ordered stream until the client
	// acknowledges the latest transition with SpaceAck.
	gated bool
}

// spacesActiveLocked reports whether any space bookkeeping can affect this
// pass. When false every session and entity is in space 0 and replication runs
// its single-space paths unchanged. Caller holds interestMu.
func (s *Server) spacesActiveLocked() bool {
	return s.reg.SpacesInUse() || len(s.sentSpace) > 0 || len(s.sessionSpaceExplicit) > 0 || len(s.spaceAckPending) > 0
}

// effectiveSessionSpaceLocked resolves a session's space: FOI anchor, then
// avatar, then explicit space, then 0. A session with an FOI whose anchor is
// missing keeps the interest manager's last space. Caller holds interestMu.
func (s *Server) effectiveSessionSpaceLocked(sessionID int64) SpaceID {
	if s.interest != nil {
		if foi, ok := s.interest.SessionFOI(sessionID); ok {
			if space, ok := s.reg.Space(foi.EntityID); ok {
				return space
			}
			return s.interest.SessionSpace(sessionID)
		}
	}
	return s.fallbackSessionSpaceLocked(sessionID)
}

// fallbackSessionSpaceLocked resolves the space of a session without an FOI:
// its avatar's space, else its explicit space, else 0. Caller holds interestMu
// (lock order: interestMu before avatarMu).
func (s *Server) fallbackSessionSpaceLocked(sessionID int64) SpaceID {
	s.avatarMu.Lock()
	avatarID, ok := s.sessionAvatar[sessionID]
	s.avatarMu.Unlock()
	if ok && avatarID != avatarSpawnPending {
		if space, ok := s.reg.Space(avatarID); ok {
			return space
		}
	}
	return s.sessionSpaceExplicit[sessionID]
}

// planSessionSpaceLocked compares a session's space for this pass with the
// announced space and starts a transition when they differ. The announced space
// changes together with the known-set decision made under the same lock, so a
// session's known set always lies inside its announced space. Caller holds
// interestMu.
func (s *Server) planSessionSpaceLocked(sessionID int64, space SpaceID) sessionSpacePlan {
	plan := sessionSpacePlan{from: s.sentSpace[sessionID], space: space}
	if plan.from != space {
		seq := s.spaceTransitionSeq[sessionID] + 1
		s.spaceTransitionSeq[sessionID] = seq
		if space == 0 {
			delete(s.sentSpace, sessionID)
		} else {
			s.sentSpace[sessionID] = space
		}
		// A newer transition supersedes any acknowledgement still pending.
		s.spaceAckPending[sessionID] = seq
		plan.transition = seq
	}
	_, plan.gated = s.spaceAckPending[sessionID]
	return plan
}

// planInterestSpacesLocked plans every FOI session from its interest diff.
// Caller holds interestMu.
func (s *Server) planInterestSpacesLocked(diffs map[int64]*interest.Diff) map[int64]sessionSpacePlan {
	plans := make(map[int64]sessionSpacePlan, len(diffs))
	for sid, diff := range diffs {
		plans[sid] = s.planSessionSpaceLocked(sid, diff.Space)
	}
	return plans
}

// planSessionWithoutFOI plans an interest-mode session that has no FOI. Such a
// session never knows entities, but still learns about space changes.
func (s *Server) planSessionWithoutFOI(sessionID int64) sessionSpacePlan {
	s.interestMu.Lock()
	defer s.interestMu.Unlock()
	return s.planSessionSpaceLocked(sessionID, s.fallbackSessionSpaceLocked(sessionID))
}

// clearSessionSpaceLocked drops all space state of a disconnected session.
// Caller holds interestMu.
func (s *Server) clearSessionSpaceLocked(sessionID int64) {
	delete(s.sessionSpaceExplicit, sessionID)
	delete(s.sentSpace, sessionID)
	delete(s.spaceTransitionSeq, sessionID)
	delete(s.spaceAckPending, sessionID)
}

// releaseSpaceGate ends stream-only delta delivery for a session once its
// latest transition is acknowledged, or right after the transition for
// sessions that never use eventual datagrams. Fields sent on the stream while
// gated are marked dirty again so the first datagrams carry their current
// values: a datagram may overtake a gated stream delta, which the client then
// discards as older.
func (s *Server) releaseSpaceGate(sessionID int64, transition uint64) bool {
	s.interestMu.Lock()
	pending, ok := s.spaceAckPending[sessionID]
	if ok && pending == transition {
		delete(s.spaceAckPending, sessionID)
	}
	s.interestMu.Unlock()
	if !ok || pending != transition {
		return false
	}
	if s.eventualTrackers != nil {
		if t := s.eventualTrackers[sessionID]; t != nil {
			t.releaseGated(func(id int64) bool {
				_, live := s.reg.Get(id)
				return live
			})
		}
	}
	return true
}

// applySpaceAck handles a client's SpaceAck on the tick goroutine.
// Acknowledgements of unknown or superseded transitions are ignored.
func (s *Server) applySpaceAck(sessionID int64, transition uint64) {
	s.releaseSpaceGate(sessionID, transition)
}

// appendSpaceWorldFrames is the world-data step of a space transition, between
// SpaceChanged and the carried entities. Per-space world data is not
// implemented yet, so it adds nothing.
func (s *Server) appendSpaceWorldFrames(sessionID int64, plan sessionSpacePlan, frames [][]byte) ([][]byte, error) {
	return frames, nil
}

// deliverSpaceTransition sends one session's replication for a pass that
// changes its announced space. The stream carries, in order: exits, then
// SpaceChanged, then world data, then the full state of every carried (Stayed)
// entity in the recipient's current view, then entered entities, then
// SpaceSettled. Carried entities are not removed and respawned; their pending
// eventual state is dropped and ownership refreshes are skipped because the
// full state already uses the current owner view. Frames are packed into as
// few logical stream frames as the reliable frame cap allows; protobuf
// concatenation keeps every envelope field and clients process them in order.
func (s *Server) deliverSpaceTransition(sessionID int64, transport golemnet.Transport, diff interest.Diff, tick *replicationTick, plan sessionSpacePlan, scope replicationScope) (eventualStateSendStats, bool, error) {
	var stats eventualStateSendStats
	scratch := tick.replicationScratch
	datagramCapable := s.usesDatagramStateUpdates() && transport == golemnet.TransportWebTransport
	frames := make([][]byte, 0, len(diff.Exited)+len(diff.Stayed)+len(diff.Entered)+2)

	for _, id := range diff.Exited {
		if datagramCapable {
			s.clearEventualEntity(sessionID, id)
		}
		data, err := s.wrappedExit(tick, id, scope)
		if err != nil {
			return stats, false, err
		}
		frames = append(frames, data)
	}
	frames = append(frames, WrapSpaceChanged(plan.space, plan.transition))
	frames, err := s.appendSpaceWorldFrames(sessionID, plan, frames)
	if err != nil {
		return stats, false, err
	}
	appendFull := func(id int64, what string) error {
		if datagramCapable {
			s.clearEventualEntity(sessionID, id)
		}
		data, ok, err := s.wrappedFullForSession(sessionID, id, scratch.spawnData, scratch.wrappedFull, scratch.wrappedPublicFull)
		if err != nil {
			return fmt.Errorf("full update for entity %d %s space %d: %w", id, what, plan.space, err)
		}
		if ok {
			frames = append(frames, data)
		}
		return nil
	}
	for _, id := range diff.Stayed {
		if err := appendFull(id, "carried into"); err != nil {
			return stats, false, err
		}
	}
	for _, id := range diff.Entered {
		if err := appendFull(id, "entering"); err != nil {
			return stats, false, err
		}
	}
	frames = append(frames, WrapSpaceSettled(plan.transition))

	sent, err := s.sendReplicationStream(sessionID, transport, golemnet.ConcatReliableFrames(frames), countAfterSend)
	if err != nil {
		if isDisconnectedSessionSend(err) {
			return stats, true, nil
		}
		return stats, false, err
	}
	stats.add(sent)
	if !datagramCapable {
		// Stream-only sessions have nothing to gate; a later SpaceAck is harmless.
		s.releaseSpaceGate(sessionID, plan.transition)
	}
	return stats, false, nil
}
