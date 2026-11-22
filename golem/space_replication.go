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
	// orphanRevisions holds the entities a session knew when RemoveFOI ran
	// and their revisions (0 = live revision), for the pass that exits them:
	// a transition of a session without an FOI, or the first pass after
	// AssignFOI (takeFOIOrphansLocked).
	orphanRevisions map[int64]uint64
}

// spacesActiveLocked reports whether any space bookkeeping can affect this
// pass. When false every session and entity is in space 0 and replication runs
// its single-space paths unchanged. Caller holds interestMu.
func (s *Server) spacesActiveLocked() bool {
	return s.reg.SpacesInUse() || len(s.sentSpace) > 0 || len(s.sessionSpaceExplicit) > 0 || len(s.sessionSpaceKept) > 0 || len(s.spaceAckPending) > 0
}

// effectiveSessionSpaceLocked resolves a session's space: FOI anchor, then
// avatar, then explicit space, then its deleted avatar's last space, then 0.
// A session with an FOI whose anchor is missing keeps the interest manager's
// last space. Only published entities count, as in replication: an anchor or
// avatar that is still provisional (inside CreateEntityWithSetup) may yet be
// discarded, and must not route a space event to its session. Caller holds
// interestMu.
func (s *Server) effectiveSessionSpaceLocked(sessionID int64) SpaceID {
	if s.interest != nil {
		if foi, ok := s.interest.SessionFOI(sessionID); ok {
			if space, ok := s.reg.PublishedSpace(foi.EntityID); ok {
				return space
			}
			return s.interest.SessionSpace(sessionID)
		}
	}
	return s.fallbackSessionSpaceLocked(sessionID)
}

// fallbackSessionSpaceLocked resolves the space of a session without an FOI:
// its avatar's space, else its explicit space, else the last space of its
// deleted avatar (sessionSpaceKept), else 0. Caller holds interestMu (lock
// order: interestMu before avatarMu).
func (s *Server) fallbackSessionSpaceLocked(sessionID int64) SpaceID {
	s.avatarMu.Lock()
	avatarID, ok := s.sessionAvatar[sessionID]
	s.avatarMu.Unlock()
	if ok && avatarID != avatarSpawnPending {
		if space, ok := s.reg.PublishedSpace(avatarID); ok {
			return space
		}
	}
	if space, ok := s.sessionSpaceExplicit[sessionID]; ok {
		return space
	}
	return s.sessionSpaceKept[sessionID]
}

// planSessionSpaceLocked compares a session's space for this pass with the
// announced space and starts a transition when they differ. The announced space
// changes together with the known-set decision made under the same lock, so a
// session's known set always lies inside its announced space. Until the
// transition frames are queued, spaceAnnouncing keeps the old space so session
// lists used for world pushes and space events still match what the client was
// told. Caller holds interestMu.
func (s *Server) planSessionSpaceLocked(sessionID int64, space SpaceID) sessionSpacePlan {
	plan := sessionSpacePlan{from: s.sentSpace[sessionID], space: space}
	if plan.from != space {
		seq := s.spaceTransitionSeq[sessionID] + 1
		s.spaceTransitionSeq[sessionID] = seq
		if _, unsent := s.spaceAnnouncing[sessionID]; !unsent {
			s.spaceAnnouncing[sessionID] = plan.from
		}
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

// announcedSpaceLocked returns the space the client was last told about: the
// old space while a planned transition has not been queued yet, otherwise
// sentSpace. Caller holds interestMu.
func (s *Server) announcedSpaceLocked(sessionID int64) SpaceID {
	if space, unsent := s.spaceAnnouncing[sessionID]; unsent {
		return space
	}
	return s.sentSpace[sessionID]
}

// finishSpaceAnnouncement drops the old announced space of a session once its
// transition frames are queued, or once the transition will never be sent.
func (s *Server) finishSpaceAnnouncement(sessionID int64) {
	s.interestMu.Lock()
	delete(s.spaceAnnouncing, sessionID)
	s.interestMu.Unlock()
}

// planSessionWithoutFOI plans an interest-mode session that has no FOI. Such a
// session never knows entities, but still learns about space changes. A plan
// with a transition takes over the entities the session knew when its FOI was
// removed (RemoveFOI) as orphanRevisions, so the transition removes them;
// without a transition they stay frozen on the client.
func (s *Server) planSessionWithoutFOI(sessionID int64) sessionSpacePlan {
	s.interestMu.Lock()
	defer s.interestMu.Unlock()
	plan := s.planSessionSpaceLocked(sessionID, s.fallbackSessionSpaceLocked(sessionID))
	if plan.transition != 0 {
		plan.orphanRevisions = s.foiOrphanKnown[sessionID]
		delete(s.foiOrphanKnown, sessionID)
	}
	return plan
}

// clearSessionSpaceLocked drops all space state of a disconnected session.
// Caller holds interestMu.
func (s *Server) clearSessionSpaceLocked(sessionID int64) {
	delete(s.sessionSpaceExplicit, sessionID)
	delete(s.sessionSpaceKept, sessionID)
	delete(s.sentSpace, sessionID)
	delete(s.spaceTransitionSeq, sessionID)
	delete(s.spaceAckPending, sessionID)
	delete(s.spaceAnnouncing, sessionID)
	delete(s.foiOrphanKnown, sessionID)
}

// releaseSpaceGate ends stream-only delta and event delivery for a session
// once its latest transition is acknowledged, or right after the transition
// for sessions without datagram lanes. The session's event transition tag
// stays, so its datagram events keep naming the acknowledged transition. Fields sent on the stream while gated
// are marked dirty again so the first datagrams carry their current values: a
// datagram may overtake a gated stream delta, which the client then discards
// as older. Tick goroutine only.
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
	// Stays on the stream while a PrepareSpaceChange still waits.
	s.refreshEventLane(sessionID)
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

// storeSpaceAck records a client's SpaceAck for the next drainMessages. Only
// the most recent acknowledgement per session is kept (clients acknowledge
// transitions in order), so the inbox cannot overflow and an acknowledgement
// is never dropped like an ordinary queued message.
func (s *Server) storeSpaceAck(sessionID int64, transition uint64) {
	s.spaceAckMu.Lock()
	if s.spaceAckInbox == nil {
		s.spaceAckInbox = make(map[int64]uint64)
	}
	s.spaceAckInbox[sessionID] = transition
	s.spaceAckQueued.Store(true)
	s.spaceAckMu.Unlock()
}

// applySpaceAcks applies every SpaceAck stored since the previous call, on the
// tick goroutine. Acks of sessions that disconnected meanwhile find no pending
// transition and are ignored. Without stored acks it takes no lock.
func (s *Server) applySpaceAcks() {
	if !s.spaceAckQueued.Load() {
		return
	}
	s.spaceAckMu.Lock()
	inbox := s.spaceAckInbox
	s.spaceAckInbox = nil
	s.spaceAckQueued.Store(false)
	s.spaceAckMu.Unlock()
	for sessionID, transition := range inbox {
		s.applySpaceAck(sessionID, transition)
	}
}

// beginSpaceTransitionEvents prepares the server events of the sessions whose
// plans carry a transition: they are held until the transition frames are
// queued (so events sent from OnUpdates follow SpaceSettled), then use the
// ordered stream until the transition is acknowledged, and their datagram
// records carry the new transition afterwards. Call without interestMu.
func (s *Server) beginSpaceTransitionEvents(plans map[int64]sessionSpacePlan) {
	var held []int64
	for sid, plan := range plans {
		if plan.transition != 0 {
			held = append(held, sid)
		}
	}
	s.holdSessionEvents(held...)
	for _, sid := range held {
		s.listener.SetSessionEventTransition(sid, plans[sid].transition, true)
	}
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
//
// Before anything is queued, a WebTransport session's datagram-lane server
// events switch to the stream until the transition is acknowledged, and its
// datagram event records carry the new transition from then on. Once the
// frames are queued the session counts as announced in the new space and the
// events it held since its space changed are flushed behind SpaceSettled.
// Exits of orphaned entities (plan.orphanRevisions) that are no longer
// registered use their recorded revisions.
func (s *Server) deliverSpaceTransition(sessionID int64, transport golemnet.Transport, diff interest.Diff, tick *replicationTick, plan sessionSpacePlan, scope replicationScope) (eventualStateSendStats, bool, error) {
	webTransport := transport == golemnet.TransportWebTransport
	if webTransport {
		s.listener.SetSessionEventTransition(sessionID, plan.transition, true)
	}
	stats, disconnected, err := s.queueSpaceTransition(sessionID, transport, diff, tick, plan, scope)
	if err != nil || disconnected {
		return stats, disconnected, err
	}
	// Same FIFO stream: events held since the space changed land after
	// SpaceSettled.
	s.releaseTransitionEvents(sessionID)
	if !webTransport {
		// Without datagram lanes neither deltas nor events can overtake the
		// stream; a later SpaceAck is harmless. WebTransport sessions keep the
		// gate even on the stream state lane, because their events may still
		// use datagram lanes.
		s.releaseSpaceGate(sessionID, plan.transition)
	}
	return stats, false, nil
}

// queueSpaceTransition builds and queues one session's transition frames and
// then ends its announcement of the old space, on every exit. The enqueue and
// the end of the announcement happen under announceMu held for writing, so a
// world push or space event from any goroutine (which holds it for reading
// across its recipient selection and enqueue) either precedes the transition
// frames, chosen for the old space, or follows them, chosen for the new one.
//
// No game code runs under the lock. Entity and world frames are serialized
// before it is taken; the world frames are then valid only if worldStamp is
// unchanged under the lock. Otherwise a value was set meanwhile, and a push of
// it may already have skipped this session as still in its old space, so the
// frames are built again.
func (s *Server) queueSpaceTransition(sessionID int64, transport golemnet.Transport, diff interest.Diff, tick *replicationTick, plan sessionSpacePlan, scope replicationScope) (stats eventualStateSendStats, disconnected bool, err error) {
	locked := false
	defer func() {
		if !locked {
			s.announceMu.Lock()
		}
		s.finishSpaceAnnouncement(sessionID)
		s.announceMu.Unlock()
	}()
	scratch := tick.replicationScratch
	datagramCapable := s.usesDatagramStateUpdates() && transport == golemnet.TransportWebTransport
	exits := make([][]byte, 0, len(diff.Exited))
	for _, id := range diff.Exited {
		if datagramCapable {
			s.clearEventualEntity(sessionID, id)
		}
		data, err := s.wrappedExitWithFallback(tick, id, scope, plan.orphanRevisions)
		if err != nil {
			return stats, false, err
		}
		exits = append(exits, data)
	}
	fulls := make([][]byte, 0, len(diff.Stayed)+len(diff.Entered))
	appendFull := func(id int64, what string) error {
		if datagramCapable {
			s.clearEventualEntity(sessionID, id)
		}
		data, ok, err := s.wrappedFullForSession(sessionID, id, scratch.spawnData, scratch.wrappedFull, scratch.wrappedPublicFull)
		if err != nil {
			return fmt.Errorf("full update for entity %d %s space %d: %w", id, what, plan.space, err)
		}
		if ok {
			fulls = append(fulls, data)
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

	var worldFrames [][]byte
	for attempt := 1; ; attempt++ {
		stamp := s.worldStamp()
		worldFrames, err = s.appendSpaceWorldFrames(sessionID, plan, worldFrames[:0])
		if err != nil {
			return stats, false, err
		}
		if s.testHookWorldFramesBuilt != nil {
			s.testHookWorldFramesBuilt()
		}
		s.announceMu.Lock()
		if s.worldStamp() == stamp {
			locked = true
			break
		}
		s.announceMu.Unlock()
		if attempt == worldSerializeAttempts {
			return stats, false, errWorldDataUnstable(fmt.Sprintf("the transition of session %d to space %d", sessionID, plan.space))
		}
	}
	frames := make([][]byte, 0, len(exits)+len(worldFrames)+len(fulls)+2)
	frames = append(frames, exits...)
	frames = append(frames, WrapSpaceChanged(plan.space, plan.transition))
	frames = append(frames, worldFrames...)
	frames = append(frames, fulls...)
	frames = append(frames, WrapSpaceSettled(plan.transition))
	sent, err := s.sendReplicationStream(sessionID, transport, golemnet.ConcatReliableFrames(frames), countAfterSend)
	if err != nil {
		if isDisconnectedSessionSend(err) {
			return stats, true, nil
		}
		return stats, false, err
	}
	stats.add(sent)
	return stats, false, nil
}

// wrappedExitWithFallback is wrappedExit for a transition's exits. The exit of
// an entity that is no longer registered and was not removed this tick uses
// its revision from fallback, so clients do not discard the removal as older
// than the state they hold.
func (s *Server) wrappedExitWithFallback(tick *replicationTick, id int64, scope replicationScope, fallback map[int64]uint64) ([]byte, error) {
	revision, ok := fallback[id]
	if !ok {
		return s.wrappedExit(tick, id, scope)
	}
	if _, removed := tick.wrappedRemovals[id]; removed {
		return s.wrappedExit(tick, id, scope)
	}
	if _, live := s.reg.Get(id); live {
		return s.wrappedExit(tick, id, scope)
	}
	reason := removalVisibilityExit
	if scope == replicationFOI {
		reason = removalFOIExit
	}
	data, err := s.serializeRemoval(id, revision, reason)
	if err != nil {
		return nil, err
	}
	return s.listener.Wrap(data), nil
}
