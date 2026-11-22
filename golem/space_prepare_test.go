package golem

import (
	"context"
	"io"
	"slices"
	"testing"
	"time"

	golemnet "github.com/demiurgos-hub/golem-engine/golem/net"
	"github.com/demiurgos-hub/golem-engine/internal/netwire"
	"github.com/quic-go/webtransport-go"
)

// The raw WebTransport peers of these tests never acknowledge datagrams on
// their own, so each test decides when the server learns that its reliable
// datagrams arrived.

// prepareRecorder collects PrepareSpaceChange outcomes.
type prepareRecorder struct{ calls []bool }

func (r *prepareRecorder) ready(_ *Server, ok bool) { r.calls = append(r.calls, ok) }

// drainUntilReady runs message drains until rec has n outcomes. When ack is
// set, it first acknowledges every datagram the peer has received, resends
// included, until the server settles.
func drainUntilReady(t *testing.T, s *Server, peer *webtransport.Session, rec *prepareRecorder, n int, ack bool) {
	t.Helper()
	var newest uint16
	seen := false
	for deadline := time.Now().Add(3 * time.Second); len(rec.calls) < n; {
		if time.Now().After(deadline) {
			t.Fatalf("preparation outcomes = %v, want %d", rec.calls, n)
		}
		if ack {
			for {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
				data, err := peer.ReceiveDatagram(ctx)
				cancel()
				if err != nil {
					break
				}
				if packet, err := netwire.DecodePacket(data); err == nil && (!seen || netwire.Newer(packet.Sequence, newest)) {
					newest, seen = packet.Sequence, true
				}
			}
			if seen {
				ackAllPackets(t, peer, newest)
			}
		}
		time.Sleep(2 * time.Millisecond)
		s.drainMessages()
	}
}

// ackAllPackets sends an ACK-only datagram covering newest and the full
// selective window before it.
func ackAllPackets(t *testing.T, peer *webtransport.Session, newest uint16) {
	t.Helper()
	var mask netwire.AckMask
	for i := 0; i < netwire.AckWindow; i++ {
		mask.SetBit(i)
	}
	data, err := netwire.EncodePacketInto(nil, netwire.Packet{ACK: netwire.ACK{Sequence: newest, Mask: mask}, Flags: netwire.FlagACKOnly})
	if err != nil {
		t.Fatal(err)
	}
	if err := peer.SendDatagram(data); err != nil {
		t.Fatal(err)
	}
}

// receiveEventRecordsExcept receives datagrams until one carries an event
// record other than skip, which resends of an earlier record may still bring.
func receiveEventRecordsExcept(t *testing.T, peer *webtransport.Session, skip string) []eventRecord {
	t.Helper()
	for {
		records := receiveEventRecords(t, peer)
		if len(records) != 1 || records[0].event != skip {
			return records
		}
	}
}

func expectStreamEvent(t *testing.T, stream io.Reader, event string) {
	t.Helper()
	if got := decodeServerTokens(t, mustReadReliableFrame(t, stream)); !slices.Equal(got, []string{"event:" + event}) {
		t.Fatalf("stream frame = %q, want event %s", got, event)
	}
}

// TestPrepareSpaceChangeWaitsForEarlierReliableEvents: a reliable-lane event
// queued before PrepareSpaceChange holds ready back until the client
// acknowledges it, events sent meanwhile take the stream, and a move made from
// ready announces the new space only after that event arrived.
func TestPrepareSpaceChangeWaitsForEarlierReliableEvents(t *testing.T) {
	s, peer, stream, sid := replicationTestPeer(t, 8)
	s.SetRemovalSerializer(func(int64, uint64) ([]byte, error) { return []byte("removed"), nil })
	avatar := &replicationContractEntity{interestTickEntity: interestTickEntity{id: 1, full: []byte("avatar-full"), revision: 1}}
	if err := s.SpawnAvatar(&Session{ID: sid}, avatar, AvatarOptions{FOIRadius: 10, FOIMargin: 1}); err != nil {
		t.Fatal(err)
	}
	tick := func() {
		t.Helper()
		if err := s.runInterestTick(); err != nil {
			t.Fatal(err)
		}
	}
	tick()
	if got := decodeServerTokens(t, mustReadReliableFrame(t, stream)); !slices.Equal(got, []string{"avatar-full"}) {
		t.Fatalf("spawn frame = %q", got)
	}
	if err := s.SendServerEvent(sid, EventReliableOrdered, []byte("evt-before")); err != nil {
		t.Fatal(err)
	}
	if got := receiveEventRecords(t, peer); !slices.Equal(got, []eventRecord{{0, "evt-before"}}) {
		t.Fatalf("event before preparing = %+v", got)
	}

	var outcomes []bool
	moved := func(s *Server, ok bool) {
		outcomes = append(outcomes, ok)
		if ok {
			if err := s.MoveToSpace(SpaceTransfer{To: 3, Entities: []int64{avatar.id}}); err != nil {
				t.Error(err)
			}
		}
	}
	rec := &prepareRecorder{}
	s.PrepareSpaceChange(sid, func(s *Server, ok bool) { rec.ready(s, ok); moved(s, ok) })
	if len(rec.calls) != 0 {
		t.Fatal("ready ran inside PrepareSpaceChange")
	}
	if err := s.SendServerEvent(sid, EventReliableOrdered, []byte("evt-prepared")); err != nil {
		t.Fatal(err)
	}
	expectStreamEvent(t, stream, "evt-prepared")

	s.drainMessages()
	time.Sleep(120 * time.Millisecond) // past the first resend
	s.drainMessages()
	if len(rec.calls) != 0 {
		t.Fatalf("ready ran before the earlier event was acknowledged: %v", rec.calls)
	}

	drainUntilReady(t, s, peer, rec, 1, true)
	if !slices.Equal(outcomes, []bool{true}) {
		t.Fatalf("ready outcomes = %v, want [true]", outcomes)
	}
	tick()
	if got := decodeServerTokens(t, mustReadReliableFrame(t, stream)); !slices.Equal(got, []string{"changed:3:1", "avatar-full", "settled:1"}) {
		t.Fatalf("transition frame = %q", got)
	}
	// The transition gate keeps events on the stream until SpaceAck.
	if err := s.SendServerEvent(sid, EventReliableOrdered, []byte("evt-gated")); err != nil {
		t.Fatal(err)
	}
	expectStreamEvent(t, stream, "evt-gated")
	if _, err := stream.(io.Writer).Write(netwire.AppendFrame(nil, golemnet.ClientSpaceAckControlFrame(1))); err != nil {
		t.Fatal(err)
	}
	waitForSpaceAck(t, s, sid, 1)
	s.drainMessages()
	if err := s.SendServerEvent(sid, EventReliableOrdered, []byte("evt-after")); err != nil {
		t.Fatal(err)
	}
	if got := receiveEventRecordsExcept(t, peer, "evt-before"); !slices.Equal(got, []eventRecord{{1, "evt-after"}}) {
		t.Fatalf("event after the SpaceAck = %+v", got)
	}
}

// TestPrepareSpaceChangeKeepsStreamWhileAnyPreparationWaits: the session's
// events stay on the stream while any preparation waits, whether another one
// was cancelled or a space transition was acknowledged meanwhile, and return
// to their datagram lane once the last one completes.
func TestPrepareSpaceChangeKeepsStreamWhileAnyPreparationWaits(t *testing.T) {
	s, peer, stream, sid := replicationTestPeer(t, 8)
	s.SetRemovalSerializer(func(int64, uint64) ([]byte, error) { return []byte("removed"), nil })
	avatar := &replicationContractEntity{interestTickEntity: interestTickEntity{id: 1, full: []byte("avatar-full"), revision: 1}}
	if err := s.SpawnAvatar(&Session{ID: sid}, avatar, AvatarOptions{FOIRadius: 10, FOIMargin: 1}); err != nil {
		t.Fatal(err)
	}
	if err := s.runInterestTick(); err != nil {
		t.Fatal(err)
	}
	mustReadReliableFrame(t, stream)
	if err := s.SendServerEvent(sid, EventReliableUnordered, []byte("evt-before")); err != nil {
		t.Fatal(err)
	}
	receiveEventRecords(t, peer)

	cancelled, waiting := &prepareRecorder{}, &prepareRecorder{}
	cancel := s.PrepareSpaceChange(sid, cancelled.ready)
	cancelWaiting := s.PrepareSpaceChange(sid, waiting.ready)
	cancel()
	cancel()
	s.drainMessages()
	if err := s.SendServerEvent(sid, EventReliableUnordered, []byte("evt-one-waits")); err != nil {
		t.Fatal(err)
	}
	expectStreamEvent(t, stream, "evt-one-waits")

	// A transition acknowledged while the preparation waits keeps the stream.
	if err := s.MoveToSpace(SpaceTransfer{To: 3, Entities: []int64{avatar.id}}); err != nil {
		t.Fatal(err)
	}
	if err := s.runInterestTick(); err != nil {
		t.Fatal(err)
	}
	mustReadReliableFrame(t, stream)
	if _, err := stream.(io.Writer).Write(netwire.AppendFrame(nil, golemnet.ClientSpaceAckControlFrame(1))); err != nil {
		t.Fatal(err)
	}
	waitForSpaceAck(t, s, sid, 1)
	s.drainMessages()
	if len(waiting.calls) != 0 {
		t.Fatalf("preparation completed before its earlier event was acknowledged: %v", waiting.calls)
	}
	if err := s.SendServerEvent(sid, EventReliableUnordered, []byte("evt-acked-switch")); err != nil {
		t.Fatal(err)
	}
	expectStreamEvent(t, stream, "evt-acked-switch")

	drainUntilReady(t, s, peer, waiting, 1, true)
	if !slices.Equal(waiting.calls, []bool{true}) || len(cancelled.calls) != 0 {
		t.Fatalf("outcomes: waiting=%v cancelled=%v", waiting.calls, cancelled.calls)
	}
	cancelWaiting() // after ready: no effect
	if err := s.SendServerEvent(sid, EventReliableUnordered, []byte("evt-datagram")); err != nil {
		t.Fatal(err)
	}
	if got := receiveEventRecordsExcept(t, peer, "evt-before"); !slices.Equal(got, []eventRecord{{1, "evt-datagram"}}) {
		t.Fatalf("event once nothing waits = %+v", got)
	}
	s.drainMessages()
	if !slices.Equal(waiting.calls, []bool{true}) {
		t.Fatalf("ready ran again: %v", waiting.calls)
	}
}

// TestPrepareSpaceChangeReportsLostSessions: a session that disconnects, or
// was never connected, completes its preparation with ok=false, and so does a
// preparation a checkpoint restore made meaningless.
func TestPrepareSpaceChangeReportsLostSessions(t *testing.T) {
	t.Run("disconnect", func(t *testing.T) {
		s, peer, _, sid := replicationTestPeer(t, 8)
		if err := s.SendServerEvent(sid, EventReliableOrdered, []byte("evt-before")); err != nil {
			t.Fatal(err)
		}
		receiveEventRecords(t, peer)
		rec := &prepareRecorder{}
		s.PrepareSpaceChange(sid, rec.ready)
		_ = peer.CloseWithError(0, "")
		drainUntilReady(t, s, peer, rec, 1, false)
		if !slices.Equal(rec.calls, []bool{false}) {
			t.Fatalf("outcome after disconnect = %v, want [false]", rec.calls)
		}
	})
	t.Run("unknown session", func(t *testing.T) {
		s := NewServer(ServerConfig{})
		rec := &prepareRecorder{}
		s.PrepareSpaceChange(99, rec.ready)
		s.drainMessages()
		if !slices.Equal(rec.calls, []bool{false}) {
			t.Fatalf("outcome for an unknown session = %v, want [false]", rec.calls)
		}
	})
	t.Run("restore", func(t *testing.T) {
		s, _, _, sid := replicationTestPeer(t, 8)
		rec := &prepareRecorder{}
		s.PrepareSpaceChange(sid, rec.ready)
		s.interestMu.Lock()
		s.failSpacePreparesLocked()
		s.interestMu.Unlock()
		s.drainMessages()
		if !slices.Equal(rec.calls, []bool{false}) {
			t.Fatalf("outcome after a restore = %v, want [false]", rec.calls)
		}
	})
	t.Run("nothing to wait for", func(t *testing.T) {
		s, _, _, sid := replicationTestPeer(t, 8)
		rec := &prepareRecorder{}
		s.PrepareSpaceChange(sid, rec.ready)
		if len(rec.calls) != 0 {
			t.Fatal("ready ran inside PrepareSpaceChange")
		}
		s.drainMessages()
		if !slices.Equal(rec.calls, []bool{true}) {
			t.Fatalf("outcome without earlier reliable datagrams = %v, want [true]", rec.calls)
		}
		if s.spacePreparesActive.Load() {
			t.Fatal("drain stays active with nothing waiting")
		}
	})
}
