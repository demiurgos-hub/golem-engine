package net

import (
	"fmt"

	"github.com/demiurgos-hub/golem-engine/internal/netwire"
)

// EventDelivery selects the lane that carries a server event to a session.
type EventDelivery uint8

const (
	// EventStream delivers on the reliable stream, ordered with snapshots, spawns and removals.
	EventStream EventDelivery = iota
	// EventReliableOrdered delivers on the reliable ordered datagram lane.
	EventReliableOrdered
	// EventReliableUnordered delivers on the reliable unordered datagram lane.
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
	switch delivery {
	case EventUnreliable:
		return nil, s.SendUnreliable(record)
	case EventReliableOrdered:
		return nil, s.queueReliable(datagramLaneReliableOrdered, record)
	default:
		return nil, s.queueReliable(datagramLaneReliableUnordered, record)
	}
}
