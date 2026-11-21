package golem

import (
	"fmt"
	"slices"

	golemnet "github.com/demiurgos-hub/golem-engine/golem/net"
)

// enqueueEntityResync is installed on the Listener as the EntityResync hook.
// It runs on the connection goroutine and stores the request for the next
// replication pass outside the bounded message queue (see storeEntityResync).
func (s *Server) enqueueEntityResync(sess *Session, entityID int64) {
	s.storeEntityResync(sess.ID, entityID)
}

// storeEntityResync records a client's request for a full state of entityID
// for the next replication pass. Only WebTransport sessions on the datagram
// state lane receive compact records, so requests from other sessions are
// dropped, and so are requests for entities the session does not know: their
// spawn will carry current state or the client gets their removal. Requests
// coalesce per session into a set bounded by the session's known set, so the
// inbox cannot overflow and a request is never dropped like an ordinary
// queued message.
func (s *Server) storeEntityResync(sessionID, entityID int64) {
	if entityID <= 0 || !s.usesDatagramStateUpdates() {
		return
	}
	if transport, connected := s.listener.SessionTransport(sessionID); !connected || transport != golemnet.TransportWebTransport {
		return
	}
	if !s.sessionKnowsEntity(sessionID, entityID) {
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
	ids[entityID] = struct{}{}
}

// takeEntityResyncs returns and clears every EntityResync request stored
// since the previous replication pass. Tick goroutine only.
func (s *Server) takeEntityResyncs() map[int64]map[int64]struct{} {
	s.entityResyncMu.Lock()
	defer s.entityResyncMu.Unlock()
	inbox := s.entityResyncInbox
	s.entityResyncInbox = nil
	return inbox
}

// appendEntityResyncFrames appends, in ID order, a full state in the
// session's current owner or public view of every requested entity that
// stays known in this pass. The client evicted compact datagram records held
// for those entities before their spawn arrived; it acknowledged those
// datagrams on receipt, so the server will not resend them. Answering inside
// the session's replication delivery keeps the full state behind the spawn
// and inside the session's current space: entities that exit this pass (for
// example moved to another space from a Post) get their removal instead, and
// entered ones already get a full state. Frames come from the pass's full
// state cache. Only WebTransport sessions on the datagram lane are answered.
func (s *Server) appendEntityResyncFrames(
	sessionID int64,
	requested map[int64]struct{},
	stayed []int64,
	tick *replicationTick,
	streamFrames [][]byte,
) ([][]byte, error) {
	if len(requested) == 0 {
		return streamFrames, nil
	}
	stayedSet := int64Set(stayed)
	ids := make([]int64, 0, len(requested))
	for id := range requested {
		if _, ok := stayedSet[id]; ok {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	scratch := tick.replicationScratch
	for _, id := range ids {
		frame, ok, err := s.wrappedFullForSession(sessionID, id, scratch.spawnData, scratch.wrappedFull, scratch.wrappedPublicFull)
		if err != nil {
			return streamFrames, fmt.Errorf("entity resync full update for entity %d: %w", id, err)
		}
		if ok {
			streamFrames = append(streamFrames, frame)
		}
	}
	return streamFrames, nil
}
