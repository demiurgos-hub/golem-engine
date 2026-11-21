package golem

import "slices"

// Server events and space switches.
//
// Events sent to a session before the replication pass that plans its space
// transition (from Post callbacks, OnTick, OnUpdates, or another goroutine)
// would otherwise reach the client before SpaceChanged and play in the old
// space. Every API that can change a session's effective space therefore
// makes the session hold its events (golem/net Listener.HoldSessionEvents)
// when the change leaves the effective space different from sentSpace. The
// held events are flushed right after the session's transition frames are
// queued (deliverSpaceTransition), so they follow SpaceSettled on the stream,
// while the stream-only flag keeps later events behind the transition until
// the SpaceAck. At the end of every replication pass the remaining holds are
// released: a session whose switch is still unplanned (it changed space after
// planning, for example from OnUpdates or a replication observer) is kept
// once more until the next pass plans it; every other session (including
// marked sessions that did not switch in the end and disconnected ones) is
// flushed, so no session holds events across more than one extra pass.
//
// Holds are taken after interestMu is released (house style: never call the
// listener under interestMu). Single-space games never hold: the effective
// space and sentSpace are always 0.
//
// Not covered: CreateEntityIn of an FOI anchor that was missing, in another
// space than the session's last one. Its transition is planned on the next
// pass, and interest plans hold before OnUpdates (beginSpaceTransitionEvents),
// but events from that tick's Post callbacks and OnTick are not held. Space
// changes made off the tick goroutine while a pass releases its holds may be
// flushed one pass early.
//
// Space-scoped events (Space.BroadcastServerEvent) are sent to the sessions
// whose effective space is the event's space, so a held event belongs to the
// space its session is moving to. One gap remains: a session that changes
// space twice before one replication pass plans it (to A, then to B) still
// receives the events sent to A in between, delivered after it lands in B.

// switchPendingLocked reports whether a session's effective space differs
// from the space its known set follows, so that its next replication pass
// plans a transition. Caller holds interestMu.
func (s *Server) switchPendingLocked(sessionID int64) bool {
	return s.effectiveSessionSpaceLocked(sessionID) != s.sentSpace[sessionID]
}

// appendMovedSessionSwitchesLocked appends to dst the sessions that follow one
// of the moved entities (as avatar or FOI anchor) and whose effective space
// now differs from sentSpace. Caller holds interestMu (avatarMu nests inside).
func (s *Server) appendMovedSessionSwitchesLocked(dst []int64, moved []int64) []int64 {
	var candidates []int64
	for _, id := range moved {
		s.avatarMu.Lock()
		sid, ok := s.avatarSession[id]
		s.avatarMu.Unlock()
		if ok {
			candidates = append(candidates, sid)
		}
		if s.interest != nil {
			candidates = s.interest.AppendAnchoredSessions(candidates, id)
		}
	}
	for _, sid := range candidates {
		if !slices.Contains(dst, sid) && s.switchPendingLocked(sid) {
			dst = append(dst, sid)
		}
	}
	return dst
}

// holdEventsIfSwitchPending holds a session's server events when its
// effective space now differs from sentSpace. Call without interestMu.
func (s *Server) holdEventsIfSwitchPending(sessionID int64) {
	s.interestMu.Lock()
	pending := s.switchPendingLocked(sessionID)
	s.interestMu.Unlock()
	if pending {
		s.holdSessionEvents(sessionID)
	}
}

// holdSessionEvents makes the listed sessions hold their server events until
// their transition is queued or the replication pass ends. Call without
// interestMu.
func (s *Server) holdSessionEvents(sessionIDs ...int64) {
	if len(sessionIDs) == 0 {
		return
	}
	s.eventHoldMu.Lock()
	defer s.eventHoldMu.Unlock()
	if s.eventHolds == nil {
		s.eventHolds = make(map[int64]bool, len(sessionIDs))
	}
	for _, sid := range sessionIDs {
		s.eventHolds[sid] = false
	}
	s.listener.HoldSessionEvents(sessionIDs...)
}

// releaseTransitionEvents flushes the events a session held once its
// transition frames are queued, so they follow SpaceSettled on the stream.
// A session whose effective space already moved on keeps holding until the
// pass that plans that newer transition. Tick goroutine only.
func (s *Server) releaseTransitionEvents(sessionID int64) {
	s.eventHoldMu.Lock()
	_, held := s.eventHolds[sessionID]
	s.eventHoldMu.Unlock()
	if !held {
		return
	}
	s.interestMu.Lock()
	pending := s.switchPendingLocked(sessionID)
	s.interestMu.Unlock()
	if pending {
		return
	}
	s.eventHoldMu.Lock()
	delete(s.eventHolds, sessionID)
	s.listener.FlushSessionEvents(sessionID)
	s.eventHoldMu.Unlock()
}

// releaseHeldSessionEvents ends a replication pass: it flushes every session
// still holding events, except that a session whose switch is still unplanned
// is kept once more for the next pass. Tick goroutine only.
func (s *Server) releaseHeldSessionEvents() {
	s.eventHoldMu.Lock()
	if len(s.eventHolds) == 0 {
		s.eventHoldMu.Unlock()
		return
	}
	candidates := make([]int64, 0, len(s.eventHolds))
	for sid, kept := range s.eventHolds {
		if !kept {
			candidates = append(candidates, sid)
		}
	}
	s.eventHoldMu.Unlock()

	var pending []int64
	if len(candidates) > 0 {
		s.interestMu.Lock()
		for _, sid := range candidates {
			if s.switchPendingLocked(sid) {
				pending = append(pending, sid)
			}
		}
		s.interestMu.Unlock()
	}

	s.eventHoldMu.Lock()
	defer s.eventHoldMu.Unlock()
	var flush []int64
	for sid, kept := range s.eventHolds {
		if !kept && slices.Contains(pending, sid) {
			s.eventHolds[sid] = true
			continue
		}
		delete(s.eventHolds, sid)
		flush = append(flush, sid)
	}
	s.listener.FlushSessionEvents(flush...)
}
