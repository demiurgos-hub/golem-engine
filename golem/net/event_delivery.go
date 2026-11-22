package net

import (
	"fmt"
	"time"

	"github.com/demiurgos-hub/golem-engine/internal/netwire"
)

// EventDelivery selects the lane that carries a server event to a session.
//
// The datagram lanes are bound to the space the session is in. Once a session
// has changed space, its datagram event records name the transition that
// brought it there, and a client drops every record of an older transition:
// such an event describes a space the client has left. That includes records
// on the reliable lanes: an event sent shortly before a space change whose
// datagram is retransmitted, and so arrives after the change, is lost. Send
// events that must survive any space change, such as inventory or chat, with
// EventStream, which stays ordered with the transition, or prepare the change
// with golem.Server.PrepareSpaceChange, which moves the session's events to
// the stream and waits until its earlier reliable datagrams are acknowledged
// (BeginEventDrain, ReliableSettled).
type EventDelivery uint8

const (
	// EventStream delivers on the reliable stream, ordered with snapshots,
	// spawns, removals and space transitions. It is the only delivery that is
	// never dropped across a space change.
	EventStream EventDelivery = iota
	// EventReliableOrdered delivers on the reliable ordered datagram lane.
	// Reliable within one space only; see EventDelivery.
	EventReliableOrdered
	// EventReliableUnordered delivers on the reliable unordered datagram lane.
	// Reliable within one space only; see EventDelivery.
	EventReliableUnordered
	// EventUnreliable delivers on the lossy datagram lane.
	EventUnreliable
)

// SendEvent queues one marshalled ServerEvent for a session. Datagram
// deliveries carry it as an event record, tagged with the session's space
// transition once SetSessionEventTransition gave it a non-zero one; a session
// without that lane, one marked stream-only, or one whose tagged record no
// longer fits the lane receives streamFrame, its wrapped ServerMessage, on the
// reliable stream. A session holding its events (HoldSessionEvents) keeps the
// event until FlushSessionEvents. An event whose untagged record is too large
// for its datagram lane fails for every session.
func (l *Listener) SendEvent(sessionID int64, delivery EventDelivery, event, streamFrame []byte) error {
	record, err := eventRecord(delivery, event, streamFrame)
	if err != nil {
		return err
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	s, ok := l.sessions[sessionID]
	if !ok {
		return fmt.Errorf("%w: %d", ErrSessionNotFound, sessionID)
	}
	return s.sendEvent(delivery, event, record, streamFrame)
}

// BroadcastEvent queues one marshalled ServerEvent for every connected
// session, falling back to streamFrame per session as SendEvent does.
func (l *Listener) BroadcastEvent(delivery EventDelivery, event, streamFrame []byte) error {
	record, err := eventRecord(delivery, event, streamFrame)
	if err != nil {
		return err
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	for _, s := range l.sessions {
		if err := s.sendEvent(delivery, event, record, streamFrame); err != nil {
			return err
		}
	}
	return nil
}

// SendEventTo queues one marshalled ServerEvent for each listed session that is
// still connected, falling back to streamFrame per session as SendEvent does.
// Sessions that are no longer connected are skipped.
func (l *Listener) SendEventTo(sessionIDs []int64, delivery EventDelivery, event, streamFrame []byte) error {
	record, err := eventRecord(delivery, event, streamFrame)
	if err != nil {
		return err
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	for _, id := range sessionIDs {
		s, ok := l.sessions[id]
		if !ok {
			continue
		}
		if err := s.sendEvent(delivery, event, record, streamFrame); err != nil {
			return err
		}
	}
	return nil
}

// eventRecord validates an event and builds its shared untagged datagram
// record (nil for stream deliveries).
func eventRecord(delivery EventDelivery, event, streamFrame []byte) ([]byte, error) {
	if err := validateReliableMessageSize(streamFrame); err != nil {
		return nil, err
	}
	if delivery == EventStream {
		return nil, nil
	}
	if delivery > EventUnreliable {
		return nil, fmt.Errorf("golem/net: unknown event delivery %d", delivery)
	}
	record := netwire.AppendEventRecord(make([]byte, 0, netwire.EventRecordBytes(event)), event)
	return record, validateEventRecord(delivery, record)
}

// validateEventRecord checks an event record against its datagram lane's budget.
func validateEventRecord(delivery EventDelivery, record []byte) error {
	switch delivery {
	case EventReliableOrdered:
		return validateReliableDatagramMessageSize(datagramLaneReliableOrdered, record)
	case EventReliableUnordered:
		return validateReliableDatagramMessageSize(datagramLaneReliableUnordered, record)
	default:
		return validateUnreliableDatagramPayloadSize(record)
	}
}

// eventStreamOnlyBit marks Session.eventLane stream-only; the other bits hold
// the session's space transition.
const eventStreamOnlyBit = uint64(1) << 63

// heldEvent is one server event kept while its session holds events.
type heldEvent struct {
	delivery                   EventDelivery
	event, record, streamFrame []byte
}

// SetSessionEventsStreamOnly makes datagram-lane server events for a
// connected session use the reliable stream (on) or their datagram lane again
// (off), keeping the session's transition tag. While on, such events stay
// ordered with the stream, so they can arrive before datagram events sent
// earlier. Unknown sessions are ignored.
func (l *Listener) SetSessionEventsStreamOnly(sessionID int64, on bool) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if s, ok := l.sessions[sessionID]; ok {
		s.setEventsStreamOnly(on)
	}
}

// BeginEventDrain makes datagram-lane server events for a connected session
// use the reliable stream from now on, like SetSessionEventsStreamOnly(on),
// and returns a watermark for ReliableSettled. Switching lanes and reading the
// watermark happen together under the session's datagram lock, which every
// datagram-lane event also takes when it queues, so an event sent
// concurrently is either older than the watermark or goes out on the stream.
// reliable is false for sessions without reliable datagram lanes, whose
// events already travel on the stream; ok is false for unknown sessions.
func (l *Listener) BeginEventDrain(sessionID int64) (mark uint16, reliable, ok bool) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	s, ok := l.sessions[sessionID]
	if !ok {
		return 0, false, false
	}
	mark, reliable = s.beginEventDrain()
	return mark, reliable, true
}

// ReliableSettled reports whether every reliable datagram queued for a
// connected session before BeginEventDrain returned mark has been
// acknowledged by the client. Raw reliable sends count as well as event
// records. Sessions without reliable datagram lanes are always settled; ok is
// false for unknown sessions.
func (l *Listener) ReliableSettled(sessionID int64, mark uint16) (settled, ok bool) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	s, ok := l.sessions[sessionID]
	if !ok {
		return false, false
	}
	return s.reliableSettled(mark), true
}

// SetSessionEventTransition atomically sets a connected session's space
// transition and stream-only flag (see SetSessionEventsStreamOnly). Once the
// transition is non-zero, datagram event records for the session carry it, so
// clients can drop records of an older transition that arrive after a space
// change. Only the low 63 bits of transition are kept. Unknown sessions are
// ignored.
func (l *Listener) SetSessionEventTransition(sessionID int64, transition uint64, streamOnly bool) {
	lane := transition &^ eventStreamOnlyBit
	if streamOnly {
		lane |= eventStreamOnlyBit
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	if s, ok := l.sessions[sessionID]; ok {
		s.eventLane.Store(lane)
	}
}

// HoldSessionEvents makes every server event sent to the listed connected
// sessions wait, in order, until FlushSessionEvents. The parent golem.Server
// holds the events of a session that may change space on this tick, so events
// sent before its transition is planned follow the transition. Unknown
// sessions are ignored.
func (l *Listener) HoldSessionEvents(sessionIDs ...int64) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	for _, id := range sessionIDs {
		if s, ok := l.sessions[id]; ok {
			s.holdEvents()
		}
	}
}

// FlushSessionEvents sends the events held for the listed connected sessions
// in the order they were sent, each through the lane its session's current
// state selects (the stream while stream-only, tagged with the current
// transition otherwise), and stops holding. Events sent concurrently wait for
// the flush, so none overtakes a held one on its lane. Unknown sessions are
// ignored; their held events die with them.
func (l *Listener) FlushSessionEvents(sessionIDs ...int64) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	for _, id := range sessionIDs {
		if s, ok := l.sessions[id]; ok {
			s.flushEvents()
		}
	}
}

func (s *Session) setEventsStreamOnly(on bool) {
	for {
		old := s.eventLane.Load()
		next := old &^ eventStreamOnlyBit
		if on {
			next |= eventStreamOnlyBit
		}
		if old == next || s.eventLane.CompareAndSwap(old, next) {
			return
		}
	}
}

func (s *Session) beginEventDrain() (mark uint16, reliable bool) {
	if s.protocol == nil {
		s.setEventsStreamOnly(true)
		return 0, false
	}
	s.protocolMu.Lock()
	defer s.protocolMu.Unlock()
	s.setEventsStreamOnly(true)
	return s.protocol.nextMessageID, true
}

func (s *Session) reliableSettled(mark uint16) bool {
	if s.protocol == nil {
		return true
	}
	s.protocolMu.Lock()
	defer s.protocolMu.Unlock()
	return s.protocol.reliableSettledBefore(mark)
}

// queueReliableEvent queues one event record on a reliable datagram lane, or
// reports that the session became stream-only after dispatchEvent read its
// lane state. The stream-only bit is rechecked under protocolMu, which
// beginEventDrain holds while it sets the bit and reads its watermark.
func (s *Session) queueReliableEvent(lane datagramLane, record []byte) (stream bool, err error) {
	if s.isClosing() {
		return false, nil
	}
	s.protocolMu.Lock()
	if s.eventLane.Load()&eventStreamOnlyBit != 0 {
		s.protocolMu.Unlock()
		return true, nil
	}
	err = s.protocol.enqueueReliable(lane, record, time.Now())
	s.protocolMu.Unlock()
	if err != nil {
		if !s.closeOnQueueFailure(laneName(lane)+" datagram", -1) {
			return false, nil
		}
		return false, err
	}
	s.signalWake()
	return false, nil
}

func (s *Session) holdEvents() {
	s.eventHoldMu.Lock()
	s.eventHolding.Store(true)
	s.eventHoldMu.Unlock()
}

func (s *Session) flushEvents() {
	s.eventHoldMu.Lock()
	defer s.eventHoldMu.Unlock()
	if !s.eventHolding.Load() {
		return
	}
	held := s.eventHeld
	s.eventHeld = nil
	var frames [][]byte
	for _, e := range held {
		// Lanes and sizes were checked when the event was sent; a lane queue
		// failure closes the session, which drops the rest anyway.
		frame, _ := s.dispatchEvent(e.delivery, e.event, e.record, e.streamFrame)
		if frame != nil {
			frames = append(frames, frame)
		}
	}
	// One in-order batch, so held events do not take a stream queue slot each.
	if len(frames) > 0 {
		s.queueStream(frames)
	}
	s.eventHolding.Store(false)
}

// sendEvent queues one event for the session, or keeps it while the session
// holds its events. record is the shared untagged record (nil for stream
// deliveries).
func (s *Session) sendEvent(delivery EventDelivery, event, record, streamFrame []byte) error {
	if s.eventHolding.Load() {
		s.eventHoldMu.Lock()
		if s.eventHolding.Load() {
			// The caller may reuse event once the send returns; the shared
			// record already holds an owned copy (stream deliveries never
			// need the event bytes).
			event = nil
			if record != nil {
				event = record[netwire.FrameHeaderBytes+1:]
			}
			s.eventHeld = append(s.eventHeld, heldEvent{delivery: delivery, event: event, record: record, streamFrame: streamFrame})
			s.eventHoldMu.Unlock()
			return nil
		}
		// Flushed meanwhile: everything held is already queued ahead of this.
		s.eventHoldMu.Unlock()
	}
	frame, err := s.dispatchEvent(delivery, event, record, streamFrame)
	if frame != nil {
		s.Send(frame)
	}
	return err
}

// dispatchEvent sends one event on its datagram lane, or returns the stream
// frame the caller must queue instead.
func (s *Session) dispatchEvent(delivery EventDelivery, event, record, streamFrame []byte) ([]byte, error) {
	lane := s.eventLane.Load()
	if delivery == EventStream || lane&eventStreamOnlyBit != 0 {
		return streamFrame, nil
	}
	if (delivery == EventUnreliable && s.datagrams == nil) || (delivery != EventUnreliable && s.protocol == nil) {
		return streamFrame, nil
	}
	if transition := lane &^ eventStreamOnlyBit; transition != 0 {
		record = netwire.AppendEventRecordTransition(make([]byte, 0, netwire.EventRecordTransitionBytes(event, transition)), event, transition)
		if validateEventRecord(delivery, record) != nil {
			// The tag pushed the record over the lane budget.
			return streamFrame, nil
		}
	}
	if delivery == EventUnreliable {
		return nil, s.SendUnreliable(record)
	}
	queue := datagramLaneReliableUnordered
	if delivery == EventReliableOrdered {
		queue = datagramLaneReliableOrdered
	}
	stream, err := s.queueReliableEvent(queue, record)
	if stream {
		return streamFrame, nil
	}
	return nil, err
}
