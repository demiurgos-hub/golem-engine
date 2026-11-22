package golem

import "sort"

// spacePrepare is one PrepareSpaceChange waiting until its session's reliable
// datagrams queued before the call are acknowledged.
type spacePrepare struct {
	sessionID int64
	seq       uint64 // registration order, so callbacks run in call order
	mark      uint16 // reliable datagram watermark from BeginEventDrain
	reliable  bool   // false: the session has no reliable datagram lanes
	ready     func(*Server, bool)
	// failed marks a preparation a checkpoint restore made meaningless.
	failed bool
}

// spacePrepareOutcome is one preparation the drain completed.
type spacePrepareOutcome struct {
	p  *spacePrepare
	ok bool
}

// PrepareSpaceChange makes sessionID's datagram-lane events travel on the
// reliable stream from now on and calls ready on the tick goroutine once every
// reliable datagram queued for the session before this call has been
// acknowledged by its client. ok is false if the session disconnected first,
// or a checkpoint restore replaced the world.
//
// Clients drop datagram events of a space they have left, so an event queued
// on a reliable datagram lane shortly before a space change can be lost when
// its datagram, or a resend, arrives after the change. Moving the session from
// ready (MoveToSpace, or SetSessionSpace for a session without an avatar)
// guarantees that every such event reaches the client in its old space, and
// events sent between this call and the move travel on the stream ahead of
// the change. A move made after ready has returned gets no such guarantee.
//
// ready runs during a tick's message drain, after the SpaceAcks of that tick,
// never inside this call: from a command handler that can be the same tick.
// The wait is about one round trip, longer when datagrams need resending; a
// datagram that cannot be delivered closes the session, so the wait is
// bounded. Sessions without reliable datagram lanes (WebSocket) are ready at
// once. Several preparations of one session complete independently, and the
// session stays on the stream while any is waiting. After ready returns, the
// session's events go back to their datagram lanes unless a space transition
// or another preparation keeps them on the stream.
//
// cancel withdraws the preparation if the game decides not to move. It is
// idempotent and does nothing once ready ran. PrepareSpaceChange and cancel
// are safe from any goroutine.
func (s *Server) PrepareSpaceChange(sessionID int64, ready func(s *Server, ok bool)) (cancel func()) {
	s.eventLaneMu.Lock()
	defer s.eventLaneMu.Unlock()
	// The lane switch and the watermark are one step on the session, and the
	// registration below happens before any lane refresh can read it.
	mark, reliable, _ := s.listener.BeginEventDrain(sessionID)
	s.interestMu.Lock()
	s.spacePrepareSeq++
	p := &spacePrepare{sessionID: sessionID, seq: s.spacePrepareSeq, mark: mark, reliable: reliable, ready: ready}
	if s.spacePrepares == nil {
		s.spacePrepares = make(map[int64][]*spacePrepare)
	}
	s.spacePrepares[sessionID] = append(s.spacePrepares[sessionID], p)
	s.spacePreparesActive.Store(true)
	s.interestMu.Unlock()
	return func() { s.cancelSpacePrepare(p) }
}

// cancelSpacePrepare removes p if it is still waiting. The session's lane is
// refreshed by the next drain on the tick goroutine, so only the tick ever
// moves a session's events back to their datagram lanes.
func (s *Server) cancelSpacePrepare(p *spacePrepare) {
	s.interestMu.Lock()
	defer s.interestMu.Unlock()
	if !s.removeSpacePrepareLocked(p) {
		return
	}
	if s.spaceLaneDirty == nil {
		s.spaceLaneDirty = make(map[int64]struct{})
	}
	s.spaceLaneDirty[p.sessionID] = struct{}{}
	s.spacePreparesActive.Store(true)
}

// removeSpacePrepareLocked removes p from its session's list and reports
// whether it was there. Caller holds interestMu.
func (s *Server) removeSpacePrepareLocked(p *spacePrepare) bool {
	list := s.spacePrepares[p.sessionID]
	for i, q := range list {
		if q != p {
			continue
		}
		list = append(list[:i:i], list[i+1:]...)
		if len(list) == 0 {
			delete(s.spacePrepares, p.sessionID)
		} else {
			s.spacePrepares[p.sessionID] = list
		}
		return true
	}
	return false
}

// failSpacePreparesLocked makes every waiting preparation complete with
// ok=false on the next drain. Caller holds interestMu.
func (s *Server) failSpacePreparesLocked() {
	for _, list := range s.spacePrepares {
		for _, p := range list {
			p.failed = true
		}
	}
}

// drainPreparedSpaceChanges completes the preparations whose sessions
// disconnected or whose earlier reliable datagrams are all acknowledged, runs
// their callbacks in call order, then refreshes the event lanes of every
// session it touched. Tick goroutine only; takes no lock when nothing waits.
func (s *Server) drainPreparedSpaceChanges() {
	if !s.spacePreparesActive.Load() {
		return
	}
	type candidate struct {
		p    *spacePrepare
		lost bool // failed by a restore, or its session closed
	}
	s.interestMu.Lock()
	var waiting []candidate
	for sid, list := range s.spacePrepares {
		_, closed := s.closedSessions[sid]
		for _, p := range list {
			waiting = append(waiting, candidate{p, closed || p.failed})
		}
	}
	touched := s.spaceLaneDirty
	s.spaceLaneDirty = nil
	s.interestMu.Unlock()

	// Listener queries take session locks; keep them outside interestMu.
	var done []spacePrepareOutcome
	for _, c := range waiting {
		p := c.p
		if c.lost {
			done = append(done, spacePrepareOutcome{p, false})
			continue
		}
		settled, connected := s.listener.ReliableSettled(p.sessionID, p.mark)
		switch {
		case !connected:
			done = append(done, spacePrepareOutcome{p, false})
		case settled || !p.reliable:
			done = append(done, spacePrepareOutcome{p, true})
		}
	}

	s.interestMu.Lock()
	fired := done[:0]
	for _, o := range done {
		// A concurrent cancel wins: its preparation must not report ready.
		if s.removeSpacePrepareLocked(o.p) {
			fired = append(fired, o)
		}
	}
	s.spacePreparesActive.Store(len(s.spacePrepares) > 0 || len(s.spaceLaneDirty) > 0)
	s.interestMu.Unlock()

	sort.Slice(fired, func(i, j int) bool { return fired[i].p.seq < fired[j].p.seq })
	for _, o := range fired {
		if o.p.ready != nil {
			o.p.ready(s, o.ok)
		}
	}
	if touched == nil && len(fired) > 0 {
		touched = make(map[int64]struct{}, len(fired))
	}
	for _, o := range fired {
		touched[o.p.sessionID] = struct{}{}
	}
	for sid := range touched {
		s.refreshEventLane(sid)
	}
}

// refreshEventLane puts a session's datagram-lane events on the stream while
// a preparation waits or a space transition awaits its SpaceAck, and back on
// their datagram lanes otherwise, keeping the transition tag. It is the one
// place that turns stream-only off, so neither owner clears the other's
// state. eventLaneMu orders it with PrepareSpaceChange, which turns the flag
// on together with registering. Tick goroutine only.
func (s *Server) refreshEventLane(sessionID int64) {
	s.eventLaneMu.Lock()
	defer s.eventLaneMu.Unlock()
	s.interestMu.Lock()
	_, gated := s.spaceAckPending[sessionID]
	streamOnly := gated || len(s.spacePrepares[sessionID]) > 0
	_, closed := s.closedSessions[sessionID]
	s.interestMu.Unlock()
	if closed {
		return
	}
	s.listener.SetSessionEventsStreamOnly(sessionID, streamOnly)
}
