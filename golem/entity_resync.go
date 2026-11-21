package golem

import (
	"log"
	"slices"

	golemnet "github.com/demiurgos-hub/golem-engine/golem/net"
)

// maxEntityResyncPerSession bounds the entity IDs one session may have waiting
// for the next tick. Generated client managers hold compact records for at
// most 256 entities and request a resync once per evicted entity, so a
// well-behaved client never reaches it; extra requests are dropped.
const maxEntityResyncPerSession = 256

// enqueueEntityResync is installed on the Listener as the EntityResync hook.
// It runs on the connection goroutine and stores the request for tick-time
// dispatch outside the bounded message queue (see storeEntityResync).
func (s *Server) enqueueEntityResync(sess *Session, entityID int64) {
	s.storeEntityResync(sess.ID, entityID)
}

// storeEntityResync records a client's request for a full state of entityID
// for the next drainMessages. Requests coalesce per session into a set of at
// most maxEntityResyncPerSession IDs, so the inbox cannot overflow and a
// request is never dropped like an ordinary queued message.
func (s *Server) storeEntityResync(sessionID, entityID int64) {
	if entityID <= 0 {
		return
	}
	s.entityResyncMu.Lock()
	defer s.entityResyncMu.Unlock()
	if s.entityResyncInbox == nil {
		s.entityResyncInbox = make(map[int64]map[int64]struct{})
	}
	ids := s.entityResyncInbox[sessionID]
	if ids == nil {
		ids = make(map[int64]struct{})
		s.entityResyncInbox[sessionID] = ids
	}
	if len(ids) < maxEntityResyncPerSession {
		ids[entityID] = struct{}{}
	}
}

// applyEntityResyncs answers every EntityResync request stored since the
// previous call, on the tick goroutine.
func (s *Server) applyEntityResyncs() {
	s.entityResyncMu.Lock()
	inbox := s.entityResyncInbox
	if len(inbox) == 0 {
		s.entityResyncMu.Unlock()
		return
	}
	s.entityResyncInbox = nil
	s.entityResyncMu.Unlock()
	for sessionID, ids := range inbox {
		s.applyEntityResync(sessionID, ids)
	}
}

// applyEntityResync sends the session a full state, in its current owner or
// public view, of every requested entity it knows, on the ordered stream.
// The client evicted compact datagram records held for those entities before
// their spawn arrived; it acknowledged those datagrams on receipt, so the
// server will not resend them. Because the session already knows the entity,
// its spawn was queued earlier and the full state lands after it. Unknown,
// deleted or out-of-space entities are ignored: their spawn will carry
// current state or the client gets their removal. Only WebTransport sessions
// on the datagram state lane receive compact records, so requests from other
// sessions (stream lane, WebSocket) are ignored, as are requests of sessions
// that disconnected meanwhile.
func (s *Server) applyEntityResync(sessionID int64, ids map[int64]struct{}) {
	if !s.usesDatagramStateUpdates() {
		return
	}
	transport, connected := s.listener.SessionTransport(sessionID)
	if !connected || transport != golemnet.TransportWebTransport {
		return
	}
	known := make([]int64, 0, len(ids))
	s.interestMu.Lock()
	for id := range ids {
		if s.sessionKnowsEntityLocked(sessionID, id) {
			known = append(known, id)
		}
	}
	s.interestMu.Unlock()
	if len(known) == 0 {
		return
	}
	slices.Sort(known)
	frames := make([][]byte, 0, len(known))
	for _, id := range known {
		e, ok := s.reg.Get(id)
		if !ok {
			continue
		}
		raw, err := s.fullUpdateForSession(e, sessionID)
		if err != nil {
			log.Printf("golem: entity resync for session %d entity %d: %v", sessionID, id, err)
			continue
		}
		frames = append(frames, s.listener.Wrap(raw))
	}
	if len(frames) == 0 {
		return
	}
	if err := s.listener.SendBatch(sessionID, frames); err != nil && !isDisconnectedSessionSend(err) {
		log.Printf("golem: entity resync for session %d: %v", sessionID, err)
	}
}
