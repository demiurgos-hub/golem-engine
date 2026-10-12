package golem

import (
	"fmt"

	"github.com/demiurgos-hub/golem-engine/golem/interest"
	golemnet "github.com/demiurgos-hub/golem-engine/golem/net"
	"github.com/demiurgos-hub/golem-engine/golem/registry"
)

// replicationScratch holds lookup maps reused by filtered broadcast and interest ticks.
// The cached payload fragments are immutable inputs; only the maps are reset
// between ticks. SendBatch copies the outer slice, so descriptors can be reused,
// but the frame bytes must remain immutable after enqueueing.
type replicationScratch struct {
	spawnData          map[int64][]byte
	deltaData          map[int64][]byte
	removalData        map[int64][]byte
	wrappedDeltas      map[int64][]byte
	wrappedRemovals    map[int64][]byte
	wrappedFull        map[int64][]byte // authoritative full frames
	wrappedPublicFull  map[int64][]byte // public/redacted full frames
	wrappedPublicDelta map[int64][]byte // public/redacted stream deltas (nil value = skip)
	eventualChanges    map[int64]eventualStateChange
	eventualFrames     map[int64]eventualPreparedFrame
	eventualDirty      []eventualStateChange
	eventualPrepared   []eventualPreparedFrame
}

// reset clears the reused per-tick lookup maps while preserving capacity.
func (s *replicationScratch) reset() {
	s.eventualDirty = s.eventualDirty[:0]
	if s.spawnData == nil {
		s.spawnData = make(map[int64][]byte)
		s.deltaData = make(map[int64][]byte)
		s.removalData = make(map[int64][]byte)
		s.wrappedDeltas = make(map[int64][]byte)
		s.wrappedRemovals = make(map[int64][]byte)
		s.wrappedFull = make(map[int64][]byte)
		s.wrappedPublicFull = make(map[int64][]byte)
		s.wrappedPublicDelta = make(map[int64][]byte)
		s.eventualChanges = make(map[int64]eventualStateChange)
		s.eventualFrames = make(map[int64]eventualPreparedFrame)
		return
	}

	clear(s.spawnData)
	clear(s.deltaData)
	clear(s.removalData)
	clear(s.wrappedDeltas)
	clear(s.wrappedRemovals)
	clear(s.wrappedFull)
	clear(s.wrappedPublicFull)
	clear(s.wrappedPublicDelta)
	clear(s.eventualChanges)
	clear(s.eventualFrames)
}

// replicationTick borrows scratch for one synchronous tick. Cached payload bytes
// are immutable; clearing maps never mutates frames queued to the listener.
type replicationTick struct {
	*replicationScratch
	updates           [][]byte
	ownerScopedDeltas bool
	eventualCache     *eventualStateTickCache
}

func (s *Server) prepareReplicationTick(result registry.FlushResult) (*replicationTick, error) {
	scratch := &s.replicationScratch
	scratch.reset()

	spawnData := scratch.spawnData
	wrappedFull := scratch.wrappedFull
	for i, id := range result.SpawnIDs {
		data := result.Spawns[i]
		spawnData[id] = data
		wrapped := s.listener.Wrap(data)
		wrappedFull[id] = wrapped
	}

	deltaData := scratch.deltaData
	eventualChanges := scratch.eventualChanges
	ownerScopedDeltas := false
	for i, id := range result.DeltaIDs {
		data := result.Deltas[i]
		deltaData[id] = data
		if e, ok := s.reg.Get(id); ok && entityIsOwnerScoped(e) {
			ownerScopedDeltas = true
		}
		if s.usesDatagramStateUpdates() {
			eventualChanges[id] = s.eventualChangeForDelta(id)
		}
	}
	removalData := scratch.removalData
	wrappedRemovals := scratch.wrappedRemovals
	for i, id := range result.Removals {
		data, err := s.serializeRemoval(id, result.RemovalRevisions[i], removalDeleted)
		if err != nil {
			return nil, err
		}
		removalData[id] = data
		wrappedRemovals[id] = s.listener.Wrap(data)
	}
	if s.usesDatagramStateUpdates() {
		s.clearEventualEntities(result.Removals)
	}

	var updates [][]byte
	updates = append(updates, result.Spawns...)
	updates = append(updates, result.Deltas...)
	for _, id := range result.Removals {
		updates = append(updates, removalData[id])
	}
	cache := (*eventualStateTickCache)(nil)
	if s.usesDatagramStateUpdates() {
		cache = newEventualStateTickCache()
	}
	return &replicationTick{replicationScratch: scratch, updates: updates, ownerScopedDeltas: ownerScopedDeltas, eventualCache: cache}, nil
}

func (s *Server) prepareEventualFrames(tick *replicationTick, sessionIDs []int64) error {
	if s.hasDatagramStateSessions(sessionIDs) && !tick.ownerScopedDeltas {
		for id, ch := range tick.eventualChanges {
			frame, err := s.eventualPreparedFrameForChange(ch)
			if err != nil {
				return err
			}
			tick.eventualFrames[id] = frame
		}
	}
	return nil
}

type replicationScope string

const (
	replicationVisibility replicationScope = "visibility"
	replicationFOI        replicationScope = "FOI"
)

// deliverReplicationDiff reports a disconnected send separately from preparation
// errors, so caller-owned known-set cleanup cannot swallow serializer failures.
func (s *Server) deliverReplicationDiff(sessionID int64, transport golemnet.Transport, diff interest.Diff, tick *replicationTick, ownershipPending map[int64]ownershipRefresh, scope replicationScope) (eventualStateSendStats, bool, error) {
	var stats eventualStateSendStats
	scratch := tick.replicationScratch
	spawnData, deltaData := scratch.spawnData, scratch.deltaData
	wrappedFull, wrappedPublicFull := scratch.wrappedFull, scratch.wrappedPublicFull
	wrappedDeltas, wrappedPublicDelta := scratch.wrappedDeltas, scratch.wrappedPublicDelta
	eventualChanges, eventualFrames := scratch.eventualChanges, scratch.eventualFrames
	ownerScopedDeltas := tick.ownerScopedDeltas
	sessionDatagrams := s.usesDatagramStateUpdates() && transport == golemnet.TransportWebTransport
	var (
		streamFrames         [][]byte
		eventualDirty        = scratch.eventualDirty[:0]
		eventualDirectFrames = scratch.eventualPrepared[:0]
	)

	for _, id := range diff.Entered {
		if sessionDatagrams {
			s.clearEventualEntity(sessionID, id)
		}
		data, ok, err := s.wrappedFullForSession(sessionID, id, spawnData, wrappedFull, wrappedPublicFull)
		if err != nil {
			return stats, false, fmt.Errorf("full update for entity %d entering %s: %w", id, scope, err)
		}
		if !ok {
			continue
		}
		streamFrames = append(streamFrames, data)
	}

	for _, id := range diff.Stayed {
		if authDelta, ok := deltaData[id]; ok {
			if sessionDatagrams {
				if ch, ok := eventualChanges[id]; ok {
					sch, send := s.eventualChangeForSession(sessionID, ch)
					if send {
						eventualDirty = append(eventualDirty, sch)
						if !sch.public && !ownerScopedDeltas {
							if prepared, ok := eventualFrames[id]; ok {
								eventualDirectFrames = append(eventualDirectFrames, prepared)
							}
						}
					}
				}
			} else {
				data, send, err := s.streamDeltaForSession(sessionID, id, authDelta, wrappedDeltas, wrappedPublicDelta)
				if err != nil {
					return stats, false, err
				}
				if send {
					streamFrames = append(streamFrames, data)
				}
			}
		}
		if _, ok := spawnData[id]; ok {
			data, ok, err := s.wrappedFullForSession(sessionID, id, spawnData, wrappedFull, wrappedPublicFull)
			if err != nil {
				return stats, false, err
			}
			if ok {
				streamFrames = append(streamFrames, data)
			}
		}
	}
	for _, id := range diff.Exited {
		if sessionDatagrams {
			s.clearEventualEntity(sessionID, id)
		}
		data, err := s.wrappedExit(tick, id, scope)
		if err != nil {
			return stats, false, err
		}
		streamFrames = append(streamFrames, data)
	}

	var err error
	streamFrames, err = s.appendOwnershipRefreshFrames(sessionID, ownershipPending, diff.Stayed, wrappedFull, wrappedPublicFull, streamFrames)
	if err != nil {
		return stats, false, err
	}

	if len(streamFrames) > 0 {
		sent, err := s.sendReplicationStream(sessionID, transport, streamFrames, countAfterSend)
		if err != nil {
			if isDisconnectedSessionSend(err) {
				return stats, true, nil
			}
			return stats, false, err
		}
		stats.add(sent)
	}
	if sessionDatagrams {
		sent, err := s.sendSessionEventualState(sessionID, eventualDirty, eventualDirectFrames, tick.eventualCache)
		if err != nil {
			// Broadcast historically absorbs disconnected eventual sends; interest
			// propagates errors that survive the eventual sender's own handling.
			if scope == replicationVisibility && isDisconnectedSessionSend(err) {
				return stats, true, nil
			}
			return stats, false, err
		}
		stats.add(sent)
	}
	scratch.eventualDirty = eventualDirty[:0]
	scratch.eventualPrepared = eventualDirectFrames[:0]
	return stats, false, nil
}

type removalReason uint8

const (
	removalDeleted removalReason = iota
	removalVisibilityExit
	removalFOIExit
)

func (s *Server) serializeRemoval(id int64, revision uint64, reason removalReason) ([]byte, error) {
	action, diagnostic := "removed", "removal"
	switch reason {
	case removalVisibilityExit:
		action, diagnostic = "exited visibility", "visibility exit"
	case removalFOIExit:
		action, diagnostic = "exited FOI", "FOI exit"
	}
	if s.removalSerializer == nil {
		return nil, fmt.Errorf("entity %d %s but no RemovalSerializer configured", id, action)
	}
	data, err := s.removalSerializer(id, revision)
	if err != nil {
		return nil, fmt.Errorf("serializing %s for entity %d: %w", diagnostic, id, err)
	}
	return data, nil
}

func (s *Server) wrappedExit(tick *replicationTick, id int64, scope replicationScope) ([]byte, error) {
	if data, ok := tick.wrappedRemovals[id]; ok {
		return data, nil
	}
	// Visibility exits do not advance revisions: re-entry may have the same state.
	revision := uint64(0)
	if e, ok := s.reg.Get(id); ok {
		if r, ok := e.(registry.StateRevisioner); ok {
			revision = r.StateRevision()
		}
	}
	reason := removalVisibilityExit
	if scope == replicationFOI {
		reason = removalFOIExit
	}
	data, err := s.serializeRemoval(id, revision, reason)
	if err != nil {
		return nil, err
	}
	tick.removalData[id] = data
	wrapped := s.listener.Wrap(data)
	tick.wrappedRemovals[id] = wrapped
	return wrapped, nil
}

type streamCountOrder uint8

const (
	countBeforeSend streamCountOrder = iota
	countAfterSend
)

// sendReplicationStream preserves the caller's validation/counting order.
// These are submission counts; physical-success accounting belongs to net.
func (s *Server) sendReplicationStream(sessionID int64, transport golemnet.Transport, frames [][]byte, order streamCountOrder) (eventualStateSendStats, error) {
	var stats eventualStateSendStats
	var count int
	if order == countBeforeSend {
		var err error
		count, err = golemnet.ReliableStreamWriteChunkCount(transport, frames)
		if err != nil {
			return stats, err
		}
	}
	if err := s.listener.SendBatch(sessionID, frames); err != nil {
		return stats, err
	}
	if order == countAfterSend {
		count, _ = golemnet.ReliableStreamWriteChunkCount(transport, frames)
	}
	stats.streamBatchedFrames = len(frames)
	stats.streamWireMsgs = count
	return stats, nil
}

func (stats *eventualStateSendStats) add(other eventualStateSendStats) {
	stats.streamBatchedFrames += other.streamBatchedFrames
	stats.streamWireMsgs += other.streamWireMsgs
	stats.datagramBatchedFrames += other.datagramBatchedFrames
	stats.datagramWirePayloads += other.datagramWirePayloads
}
