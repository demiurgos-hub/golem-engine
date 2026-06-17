package net

import (
	"strings"
	"testing"
	"time"
)

func TestUint64DeltaWrap(t *testing.T) {
	const max = ^uint64(0)
	if d := uint64Delta(uint64(3), max); d != 4 {
		t.Fatalf("delta across wrap: got %d want 4", d)
	}
}

func TestSessionOutboundSnapshotWebSocketNoDatagramProtocol(t *testing.T) {
	conn, cleanup := testWSPair(t)
	defer cleanup()
	s := newWebSocketSession(42, conn)
	now := time.Now()
	sn := s.OutboundSnapshot(now)
	if sn.SessionID != 42 {
		t.Fatalf("SessionID = %d, want 42", sn.SessionID)
	}
	if sn.PendingOrdered != 0 || sn.PendingUnordered != 0 {
		t.Fatalf("pending should be 0 without datagram protocol: %+v", sn)
	}
}

func TestOutboundBacklogForLogIncludesInboundProtocolDeltas(t *testing.T) {
	listener := NewListener(nil, Config{Transport: TransportWebTransport})
	sess := newSession(7, &captureReliableChannel{}, &captureDatagramChannel{}, nil)
	listener.addSession(sess)
	defer listener.removeSession(sess)

	_ = listener.OutboundBacklogForLog(time.UnixMilli(0))

	sess.protocolMu.Lock()
	sess.protocol.inboundPackets = 3
	sess.protocol.inboundAckOnly = 2
	sess.protocolMu.Unlock()

	line := listener.OutboundBacklogForLog(time.UnixMilli(1000))
	if !strings.Contains(line, "pin_1s=3") {
		t.Fatalf("OutboundBacklogForLog = %q, want pin_1s=3", line)
	}
	if !strings.Contains(line, "ack_1s=2") {
		t.Fatalf("OutboundBacklogForLog = %q, want ack_1s=2", line)
	}
}
