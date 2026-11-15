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
// deliveries carry it as a tagged event record; a session without that lane
// receives streamFrame, its wrapped ServerMessage, on the reliable stream.
// An event too large for its datagram lane fails for every session.
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
	return s.sendEvent(delivery, record, streamFrame)
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
		if err := s.sendEvent(delivery, record, streamFrame); err != nil {
			return err
		}
	}
	return nil
}

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
	switch delivery {
	case EventReliableOrdered:
		return record, validateReliableDatagramMessageSize(datagramLaneReliableOrdered, record)
	case EventReliableUnordered:
		return record, validateReliableDatagramMessageSize(datagramLaneReliableUnordered, record)
	default:
		return record, validateUnreliableDatagramPayloadSize(record)
	}
}

func (s *Session) sendEvent(delivery EventDelivery, record, streamFrame []byte) error {
	switch {
	case delivery == EventUnreliable && s.datagrams != nil:
		return s.SendUnreliable(record)
	case delivery == EventReliableOrdered && s.protocol != nil:
		return s.queueReliable(datagramLaneReliableOrdered, record)
	case delivery == EventReliableUnordered && s.protocol != nil:
		return s.queueReliable(datagramLaneReliableUnordered, record)
	}
	s.Send(streamFrame)
	return nil
}
