package golem

import (
	"fmt"

	golemnet "github.com/demiurgos-hub/golem-engine/golem/net"
	"github.com/demiurgos-hub/golem-engine/golem/registry"
)

type eventualStateChange struct {
	id   int64
	mask uint64
	full bool
}

const (
	eventualInFlightFreeLimit  = 64
	eventualInFlightFreeMaxCap = 256
)

type eventualStateTracker struct {
	dirtySet     map[int64]eventualStateChange
	dirtyQueue   []int64
	inFlight     map[uint64][]eventualStateChange
	inFlightFree [][]eventualStateChange
}

type eventualStateFrame struct {
	datagram     []byte
	stream       []byte
	payloadLen   int
	fitsDatagram bool
}

type eventualPreparedFrame struct {
	change eventualStateChange
	frame  eventualStateFrame
	live   bool
}

type eventualStateFrameKey struct {
	id   int64
	mask uint64
	full bool
}

type eventualStateTickCache struct {
	frames map[eventualStateFrameKey]eventualStateFrame
}

type eventualStateSendScratch struct {
	dirtyChanges   []eventualStateChange
	pendingChanges []eventualStateChange
	streamChanges  []eventualStateChange
	streamFrames   [][]byte
	payload        []byte
}

func newEventualStateTickCache() *eventualStateTickCache {
	return &eventualStateTickCache{
		frames: make(map[eventualStateFrameKey]eventualStateFrame),
	}
}

func newEventualStateTracker() *eventualStateTracker {
	return &eventualStateTracker{
		dirtySet: make(map[int64]eventualStateChange),
		inFlight: make(map[uint64][]eventualStateChange),
	}
}

func (t *eventualStateTracker) markDirty(ids []int64) {
	for _, id := range ids {
		t.markDirtyFull(id)
	}
}

func (t *eventualStateTracker) markDirtyMask(id int64, mask uint64) {
	t.markDirtyChange(eventualStateChange{id: id, mask: mask})
}

func (t *eventualStateTracker) markDirtyFull(id int64) {
	t.markDirtyChange(eventualStateChange{id: id, full: true})
}

func (t *eventualStateTracker) markDirtyChange(ch eventualStateChange) {
	if ch.id == 0 || (!ch.full && ch.mask == 0) {
		return
	}
	if existing, ok := t.dirtySet[ch.id]; ok {
		if existing.full || ch.full {
			existing.full = true
			existing.mask = 0
		} else {
			existing.mask |= ch.mask
		}
		t.dirtySet[ch.id] = existing
		return
	}
	if len(t.dirtyQueue) != len(t.dirtySet) {
		t.compactDirtyQueueNow()
		if existing, ok := t.dirtySet[ch.id]; ok {
			if existing.full || ch.full {
				existing.full = true
				existing.mask = 0
			} else {
				existing.mask |= ch.mask
			}
			t.dirtySet[ch.id] = existing
			return
		}
	}
	t.dirtySet[ch.id] = ch
	t.dirtyQueue = append(t.dirtyQueue, ch.id)
}

func (t *eventualStateTracker) dirtyIDs() []int64 {
	return t.dirtyIDsInto(nil)
}

func (t *eventualStateTracker) dirtyIDsInto(ids []int64) []int64 {
	ids = ids[:0]
	if len(t.dirtySet) == 0 {
		return ids
	}
	if len(t.dirtyQueue) == len(t.dirtySet) {
		for _, id := range t.dirtyQueue {
			ids = append(ids, id)
		}
		return ids
	}
	for _, id := range t.dirtyQueue {
		if _, ok := t.dirtySet[id]; ok {
			ids = append(ids, id)
		}
	}
	return ids
}

func (t *eventualStateTracker) dirtyChangesInto(changes []eventualStateChange) []eventualStateChange {
	changes = changes[:0]
	if len(t.dirtySet) == 0 {
		return changes
	}
	for _, id := range t.dirtyQueue {
		if ch, ok := t.dirtySet[id]; ok {
			changes = append(changes, ch)
		}
	}
	return changes
}

// hasDirty reports whether the tracker has unsent or requeued dirty state.
func (t *eventualStateTracker) hasDirty() bool {
	return len(t.dirtySet) != 0
}

func (t *eventualStateTracker) markSent(token uint64, changes []eventualStateChange) {
	if len(changes) == 0 {
		return
	}
	if existing, ok := t.inFlight[token]; ok {
		t.releaseInFlightChanges(existing)
	}
	cp := t.borrowInFlightChanges(len(changes))
	cp = append(cp, changes...)
	t.inFlight[token] = cp
	for _, ch := range changes {
		t.clearDirty(ch.id)
	}
}

func (t *eventualStateTracker) borrowInFlightChanges(n int) []eventualStateChange {
	for i := len(t.inFlightFree) - 1; i >= 0; i-- {
		buf := t.inFlightFree[i]
		if cap(buf) < n {
			continue
		}
		last := len(t.inFlightFree) - 1
		t.inFlightFree[i] = t.inFlightFree[last]
		t.inFlightFree[last] = nil
		t.inFlightFree = t.inFlightFree[:last]
		return buf[:0]
	}
	return make([]eventualStateChange, 0, n)
}

func (t *eventualStateTracker) releaseInFlightChanges(changes []eventualStateChange) {
	if cap(changes) == 0 || cap(changes) > eventualInFlightFreeMaxCap || len(t.inFlightFree) >= eventualInFlightFreeLimit {
		return
	}
	t.inFlightFree = append(t.inFlightFree, changes[:0])
}

func (t *eventualStateTracker) clearDirty(id int64) {
	delete(t.dirtySet, id)
}

func (t *eventualStateTracker) clearEntity(id int64) {
	t.clearDirty(id)
	for token, changes := range t.inFlight {
		dst := changes[:0]
		for _, ch := range changes {
			if ch.id != id {
				dst = append(dst, ch)
			}
		}
		if len(dst) == 0 {
			delete(t.inFlight, token)
			t.releaseInFlightChanges(changes)
			continue
		}
		t.inFlight[token] = dst
	}
}

func (t *eventualStateTracker) clearEntities(ids []int64) {
	for _, id := range ids {
		t.clearEntity(id)
	}
}

func (t *eventualStateTracker) compactDirtyQueue() {
	if len(t.dirtyQueue) == 0 || len(t.dirtyQueue) == len(t.dirtySet) {
		return
	}
	if len(t.dirtySet) == 0 {
		t.dirtyQueue = t.dirtyQueue[:0]
		return
	}
	if len(t.dirtyQueue) <= len(t.dirtySet)*2+32 {
		return
	}
	t.compactDirtyQueueNow()
}

func (t *eventualStateTracker) compactDirtyQueueNow() {
	if len(t.dirtyQueue) == 0 || len(t.dirtyQueue) == len(t.dirtySet) {
		return
	}
	if len(t.dirtySet) == 0 {
		t.dirtyQueue = t.dirtyQueue[:0]
		return
	}
	dst := t.dirtyQueue[:0]
	for _, id := range t.dirtyQueue {
		if _, ok := t.dirtySet[id]; ok {
			dst = append(dst, id)
		}
	}
	t.dirtyQueue = dst
}

func (t *eventualStateTracker) applyFeedback(feedback []golemnet.EventualStateDelivery, isLive func(int64) bool) {
	for _, f := range feedback {
		ids, ok := t.inFlight[f.Token]
		if !ok {
			continue
		}
		delete(t.inFlight, f.Token)
		if f.Delivered {
			t.releaseInFlightChanges(ids)
			continue
		}
		t.compactDirtyQueueNow()
		for _, ch := range ids {
			if isLive != nil && !isLive(ch.id) {
				continue
			}
			if remaining := t.subtractNewerInFlight(f.Token, ch); remaining.id != 0 {
				t.markDirtyChange(remaining)
			}
		}
		t.releaseInFlightChanges(ids)
	}
}

func (t *eventualStateTracker) subtractNewerInFlight(lostToken uint64, ch eventualStateChange) eventualStateChange {
	if ch.full {
		for token, changes := range t.inFlight {
			if token <= lostToken {
				continue
			}
			for _, newer := range changes {
				if newer.id == ch.id && newer.full {
					return eventualStateChange{}
				}
			}
		}
		return ch
	}

	mask := ch.mask
	for token, changes := range t.inFlight {
		if token <= lostToken {
			continue
		}
		for _, newer := range changes {
			if newer.id != ch.id {
				continue
			}
			if newer.full {
				mask = 0
				break
			}
			mask &^= newer.mask
		}
		if mask == 0 {
			return eventualStateChange{}
		}
	}
	return eventualStateChange{id: ch.id, mask: mask}
}

func (s *Server) eventualTracker(sessionID int64) *eventualStateTracker {
	if s.eventualTrackers == nil {
		s.eventualTrackers = make(map[int64]*eventualStateTracker)
	}
	t := s.eventualTrackers[sessionID]
	if t == nil {
		t = newEventualStateTracker()
		s.eventualTrackers[sessionID] = t
	}
	return t
}

func (s *Server) applyEventualStateFeedback(sessionID int64, feedback []golemnet.EventualStateDelivery) {
	if len(feedback) == 0 || s.eventualTrackers == nil {
		return
	}
	if t := s.eventualTrackers[sessionID]; t != nil {
		t.applyFeedback(feedback, func(id int64) bool {
			_, ok := s.reg.Get(id)
			return ok
		})
	}
}

func (s *Server) nextEventualStateToken() uint64 {
	s.nextEventualToken++
	return s.nextEventualToken
}

// eventualChangeForDelta returns the eventual replication change represented by
// the most recent registry delta for id.
func (s *Server) eventualChangeForDelta(id int64) eventualStateChange {
	e, ok := s.reg.Get(id)
	if !ok {
		return eventualStateChange{id: id, full: true}
	}
	if d, ok := e.(registry.ReplicationDeltaEntity); ok {
		if mask := d.LastFlushMask(); mask != 0 {
			return eventualStateChange{id: id, mask: mask}
		}
	}
	return eventualStateChange{id: id, full: true}
}

// clearEventualEntity drops pending eventual state for one session/entity pair.
func (s *Server) clearEventualEntity(sessionID, entityID int64) {
	if s.eventualTrackers == nil {
		return
	}
	if t := s.eventualTrackers[sessionID]; t != nil {
		t.clearEntity(entityID)
	}
}

// clearEventualEntities drops pending eventual state for removed entities.
func (s *Server) clearEventualEntities(entityIDs []int64) {
	if len(entityIDs) == 0 || s.eventualTrackers == nil {
		return
	}
	for _, t := range s.eventualTrackers {
		for _, id := range entityIDs {
			t.clearEntity(id)
		}
	}
}

func (c *eventualStateTickCache) frame(s *Server, ch eventualStateChange) (eventualStateFrame, bool, error) {
	key := eventualStateFrameKey{id: ch.id, mask: ch.mask, full: ch.full}
	if f, ok := c.frames[key]; ok {
		return f, true, nil
	}
	f, live, err := s.eventualFrameForChange(ch)
	if err != nil || !live {
		return f, live, err
	}
	c.frames[key] = f
	return f, true, nil
}

// eventualPreparedFrameForChange builds a reusable current-tick frame for ch.
func (s *Server) eventualPreparedFrameForChange(ch eventualStateChange) (eventualPreparedFrame, error) {
	f, live, err := s.eventualFrameForChange(ch)
	if err != nil {
		return eventualPreparedFrame{}, err
	}
	return eventualPreparedFrame{change: ch, frame: f, live: live}, nil
}

// eventualFrameForChange serializes the current entity values represented by ch.
func (s *Server) eventualFrameForChange(ch eventualStateChange) (eventualStateFrame, bool, error) {
	e, ok := s.reg.Get(ch.id)
	if !ok {
		return eventualStateFrame{}, false, nil
	}
	if ch.full {
		data, err := e.FullUpdate()
		if err != nil {
			return eventualStateFrame{}, true, fmt.Errorf("full update for eventual entity %d: %w", ch.id, err)
		}
		f := eventualStateFrame{stream: s.listener.Wrap(data)}
		return f, true, nil
	}

	compact, err := s.compactDeltaForMask(ch.id, ch.mask)
	if err != nil {
		return eventualStateFrame{}, true, err
	}
	if compact != nil {
		payloadLen := golemnet.CompactStateDatagramFramePayloadLen(compact)
		if payloadLen <= golemnet.EventualStateDatagramPayloadBudget() {
			f := eventualStateFrame{
				datagram:     compact,
				payloadLen:   payloadLen,
				fitsDatagram: true,
			}
			return f, true, nil
		}
	}

	var data []byte
	if delta, ok := e.(registry.ReplicationDeltaEntity); ok {
		data, err = delta.MarshalDeltaMask(ch.mask)
		if err != nil {
			return eventualStateFrame{}, true, fmt.Errorf("delta update for eventual entity %d: %w", ch.id, err)
		}
		if data == nil {
			return eventualStateFrame{}, true, nil
		}
	} else {
		data, err = e.FullUpdate()
		if err != nil {
			return eventualStateFrame{}, true, fmt.Errorf("full update for eventual entity %d: %w", ch.id, err)
		}
	}
	f := eventualStateFrame{stream: s.listener.Wrap(data)}
	return f, true, nil
}

func (s *Server) sendEventualState(sessionID int64, t *eventualStateTracker, cache *eventualStateTickCache) (int, int, error) {
	scratch := &s.eventualScratch
	changes := t.dirtyChangesInto(scratch.dirtyChanges)
	scratch.dirtyChanges = changes
	if len(changes) == 0 {
		t.compactDirtyQueue()
		scratch.dirtyChanges = changes[:0]
		return 0, 0, nil
	}
	batched, wireMsgs, err := s.sendEventualStateChanges(sessionID, t, cache, changes)
	t.compactDirtyQueue()
	scratch.dirtyChanges = changes[:0]
	return batched, wireMsgs, err
}

// sendEventualStateChanges sends an already-collected set of eventual state
// changes and records datagram sends in the tracker for feedback handling.
func (s *Server) sendEventualStateChanges(sessionID int64, t *eventualStateTracker, cache *eventualStateTickCache, changes []eventualStateChange) (int, int, error) {
	if len(changes) == 0 {
		return 0, 0, nil
	}
	if cache == nil {
		cache = newEventualStateTickCache()
	}

	var (
		scratch        = &s.eventualScratch
		streamFrames   = scratch.streamFrames[:0]
		streamChanges  = scratch.streamChanges[:0]
		pendingChanges = scratch.pendingChanges[:0]
		payload        = scratch.payload[:0]
		pendingBytes   int
		batched        int
		wireMsgs       int
	)
	defer func() {
		scratch.pendingChanges = pendingChanges[:0]
		scratch.streamChanges = streamChanges[:0]
		scratch.streamFrames = streamFrames[:0]
		scratch.payload = payload[:0]
	}()

	flushPending := func() (bool, error) {
		if len(pendingChanges) == 0 {
			return false, nil
		}
		ownedPayload := payload
		token := s.nextEventualStateToken()
		if err := s.listener.SendEventualStateOwned(sessionID, token, ownedPayload); err != nil {
			if isDisconnectedSessionSend(err) {
				return true, nil
			}
			return false, err
		}
		t.markSent(token, pendingChanges)
		batched += len(pendingChanges)
		wireMsgs++
		pendingChanges = pendingChanges[:0]
		payload = make([]byte, 0, golemnet.EventualStateDatagramPayloadBudget())
		pendingBytes = 0
		return false, nil
	}

	for _, ch := range changes {
		frame, live, err := cache.frame(s, ch)
		if err != nil {
			return batched, wireMsgs, err
		}
		if !live {
			t.clearDirty(ch.id)
			continue
		}
		if frame.datagram == nil && frame.stream == nil {
			t.clearDirty(ch.id)
			continue
		}
		if !frame.fitsDatagram {
			streamFrames = append(streamFrames, frame.stream)
			streamChanges = append(streamChanges, ch)
			continue
		}
		if pendingBytes > 0 && pendingBytes+frame.payloadLen > golemnet.EventualStateDatagramPayloadBudget() {
			sessionGone, err := flushPending()
			if err != nil || sessionGone {
				return batched, wireMsgs, err
			}
		}
		pendingChanges = append(pendingChanges, ch)
		payload = golemnet.AppendCompactStateDatagramFrame(payload, frame.datagram)
		pendingBytes += frame.payloadLen
	}
	sessionGone, err := flushPending()
	if err != nil || sessionGone {
		return batched, wireMsgs, err
	}
	if len(streamFrames) > 0 {
		if err := s.listener.SendBatch(sessionID, streamFrames); err != nil {
			if isDisconnectedSessionSend(err) {
				return batched, wireMsgs, nil
			}
			return batched, wireMsgs, err
		}
		for _, ch := range streamChanges {
			t.clearDirty(ch.id)
		}
	}
	return batched, wireMsgs, nil
}

// sendPreparedEventualStateFrames sends prebuilt eventual frames for one session.
func (s *Server) sendPreparedEventualStateFrames(sessionID int64, t *eventualStateTracker, prepared []eventualPreparedFrame) (int, int, error) {
	if len(prepared) == 0 {
		return 0, 0, nil
	}

	var (
		scratch        = &s.eventualScratch
		streamFrames   = scratch.streamFrames[:0]
		streamChanges  = scratch.streamChanges[:0]
		pendingChanges = scratch.pendingChanges[:0]
		payload        = scratch.payload[:0]
		pendingBytes   int
		batched        int
		wireMsgs       int
	)
	defer func() {
		scratch.pendingChanges = pendingChanges[:0]
		scratch.streamChanges = streamChanges[:0]
		scratch.streamFrames = streamFrames[:0]
		scratch.payload = payload[:0]
	}()

	flushPending := func() (bool, error) {
		if len(pendingChanges) == 0 {
			return false, nil
		}
		ownedPayload := payload
		token := s.nextEventualStateToken()
		if err := s.listener.SendEventualStateOwned(sessionID, token, ownedPayload); err != nil {
			if isDisconnectedSessionSend(err) {
				return true, nil
			}
			return false, err
		}
		t.markSent(token, pendingChanges)
		batched += len(pendingChanges)
		wireMsgs++
		pendingChanges = pendingChanges[:0]
		payload = make([]byte, 0, golemnet.EventualStateDatagramPayloadBudget())
		pendingBytes = 0
		return false, nil
	}

	for _, pf := range prepared {
		ch := pf.change
		frame := pf.frame
		if !pf.live {
			t.clearDirty(ch.id)
			continue
		}
		if frame.datagram == nil && frame.stream == nil {
			t.clearDirty(ch.id)
			continue
		}
		if !frame.fitsDatagram {
			streamFrames = append(streamFrames, frame.stream)
			streamChanges = append(streamChanges, ch)
			continue
		}
		if pendingBytes > 0 && pendingBytes+frame.payloadLen > golemnet.EventualStateDatagramPayloadBudget() {
			sessionGone, err := flushPending()
			if err != nil || sessionGone {
				return batched, wireMsgs, err
			}
		}
		pendingChanges = append(pendingChanges, ch)
		payload = golemnet.AppendCompactStateDatagramFrame(payload, frame.datagram)
		pendingBytes += frame.payloadLen
	}
	sessionGone, err := flushPending()
	if err != nil || sessionGone {
		return batched, wireMsgs, err
	}
	if len(streamFrames) > 0 {
		if err := s.listener.SendBatch(sessionID, streamFrames); err != nil {
			if isDisconnectedSessionSend(err) {
				return batched, wireMsgs, nil
			}
			return batched, wireMsgs, err
		}
		for _, ch := range streamChanges {
			t.clearDirty(ch.id)
		}
	}
	return batched, wireMsgs, nil
}
