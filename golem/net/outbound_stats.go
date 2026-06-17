package net

import (
	"time"
)

// uint64Delta returns b-a with unsigned wrap when b < a.
func uint64Delta(b, a uint64) uint64 {
	if b >= a {
		return b - a
	}
	return (^uint64(0) - a + 1) + b
}

// SessionOutboundSnapshot is a point-in-time sample of a session's outbound
// buffers: reliable stream and unreliable datagram channel depths, and the
// reliable datagram protocol pending queues. Used for diagnosing accumulation
// (head age shows how long the ordered queue has been waiting).
type SessionOutboundSnapshot struct {
	SessionID int64

	StreamBatchesQueued int
	// UnreliableQueued is raw unreliable payloads waiting in the per-session channel.
	UnreliableQueued int

	PendingOrdered    int
	InFlightOrdered   int
	HeadAgeOrdered    time.Duration // 0 if PendingOrdered==0; age of first queue entry
	PendingUnordered  int
	InFlightUnordered int
	HeadAgeUnordered  time.Duration
	EventualQueued    int
	EventualInFlight  int
	InboundPackets    uint64
	InboundAckOnly    uint64
}

// OutboundSnapshot returns send-queue and protocol pending counts. now is
// used only to compute how old the head of each reliable datagram queue is.
func (s *Session) OutboundSnapshot(now time.Time) SessionOutboundSnapshot {
	out := SessionOutboundSnapshot{SessionID: s.ID}
	if s.streamSend != nil {
		out.StreamBatchesQueued = len(s.streamSend)
	}
	if s.unreliableSend != nil {
		out.UnreliableQueued = len(s.unreliableSend)
	}
	if s.eventualSend != nil {
		out.EventualQueued = len(s.eventualSend)
	}
	if s.protocol == nil {
		return out
	}
	s.protocolMu.Lock()
	p := s.protocol
	for _, m := range p.pendingOrdered {
		out.PendingOrdered++
		if m.inFlight {
			out.InFlightOrdered++
		}
	}
	if out.PendingOrdered > 0 {
		out.HeadAgeOrdered = now.Sub(p.pendingOrdered[0].queuedAt)
	}
	for _, m := range p.pendingUnordered {
		out.PendingUnordered++
		if m.inFlight {
			out.InFlightUnordered++
		}
	}
	if out.PendingUnordered > 0 {
		out.HeadAgeUnordered = now.Sub(p.pendingUnordered[0].queuedAt)
	}
	out.EventualInFlight = len(p.inFlightEventual)
	out.InboundPackets = p.inboundPackets
	out.InboundAckOnly = p.inboundAckOnly
	s.protocolMu.Unlock()
	return out
}
