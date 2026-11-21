package golem

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http/httptest"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	golemnet "github.com/demiurgos-hub/golem-engine/golem/net"
	"github.com/demiurgos-hub/golem-engine/internal/netwire"
	"github.com/quic-go/webtransport-go"
)

// Keep a real transport boundary: the server's queues retain owned packet bytes
// after the tick sender returns and reuses its scratch.
func replicationTestPeer(t testing.TB, cellSize float64) (*Server, *webtransport.Session, io.Reader, int64) {
	t.Helper()
	return replicationTestPeerWith(t, ServerConfig{CellSize: cellSize})
}

// replicationTestPeerWith is replicationTestPeer with a caller config; Addr and
// DevSelfSignedCert are filled in.
func replicationTestPeerWith(t testing.TB, cfg ServerConfig) (*Server, *webtransport.Session, io.Reader, int64) {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := pc.LocalAddr().String()
	_ = pc.Close()
	cfg.Addr, cfg.DevSelfSignedCert = addr, true
	s := NewServer(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.listener.ListenAndServe(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil && !errors.Is(err, context.Canceled) {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("listener shutdown timed out")
		}
	})
	readyCtx, readyCancel := context.WithTimeout(ctx, 5*time.Second)
	defer readyCancel()
	if err := s.WaitReady(readyCtx); err != nil {
		t.Fatal(err)
	}
	dialer := &webtransport.Dialer{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	t.Cleanup(func() { _ = dialer.Close() })
	_, peer, err := dialer.Dial(readyCtx, "https://"+addr+"/wt", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = peer.CloseWithError(0, "") })
	stream, err := peer.OpenStreamSync(readyCtx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Write(nil); err != nil {
		t.Fatal(err)
	}
	_ = stream.SetReadDeadline(time.Now().Add(10 * time.Second))
	select {
	case m := <-s.msgQueue:
		if m.kind != msgConnect {
			t.Fatalf("expected connect, got %v", m.kind)
		}
		return s, peer, stream, m.sess.ID
	case <-readyCtx.Done():
		t.Fatal("session was not admitted")
	}
	return nil, nil, nil, 0
}

func receiveReplicationDatagram(t testing.TB, peer *webtransport.Session) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	data, err := peer.ReceiveDatagram(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) > 1200 {
		t.Fatalf("oversized packet: %d", len(data))
	}
	return data
}

type replicationContractEntity struct{ interestTickEntity }

func (e *replicationContractEntity) MarshalDeltaMask(uint64) ([]byte, error) { return e.flush, nil }

func TestReplicationCallbackDiffAndPreparedFrameTiming(t *testing.T) {
	for _, interestMode := range []bool{false, true} {
		name := "broadcast"
		cellSize := 0.0
		if interestMode {
			name = "interest"
			cellSize = 8
		}
		t.Run(name, func(t *testing.T) {
			s, peer, stream, sid := replicationTestPeer(t, cellSize)
			e := &replicationContractEntity{interestTickEntity: interestTickEntity{id: 1, full: []byte("full"), revision: 7}}
			if err := s.CreateEntity(e); err != nil {
				t.Fatal(err)
			}
			s.SetEntityVisibilityGroup(1, "g")
			s.JoinVisibilityGroup(sid, "g")
			if interestMode {
				s.AssignFOI(sid, 1, 10, 1)
			}
			run := s.runBroadcastTick
			if interestMode {
				run = s.runInterestTick
			}
			if err := run(); err != nil {
				t.Fatal(err)
			}
			_ = mustReadReliableFrame(t, stream)
			e.flush = []byte("delta")
			e.compact = []byte("before-callback")
			s.OnUpdates(func(updates [][]byte) {
				if len(updates) != 1 || !bytes.Equal(updates[0], e.flush) {
					t.Errorf("raw flush callback: %q", updates)
				}
				e.compact = []byte("after-callback")
			})
			if err := run(); err != nil {
				t.Fatal(err)
			}
			want := []byte("before-callback")
			if interestMode {
				want = []byte("after-callback")
			}
			if got := receiveReplicationDatagram(t, peer); !bytes.Contains(got, want) {
				t.Fatalf("packet %q lacks %q", got, want)
			}
			var revisions []uint64
			s.SetRemovalSerializer(func(_ int64, revision uint64) ([]byte, error) {
				revisions = append(revisions, revision)
				return []byte("removed"), nil
			})
			s.OnUpdates(func([][]byte) { s.LeaveVisibilityGroup(sid, "g") })
			if err := run(); err != nil {
				t.Fatal(err)
			}
			known := len(s.SessionsKnowing(1)) > 0
			if known != interestMode {
				t.Fatalf("known=%v after policy callback", known)
			}
			s.OnUpdates(nil)
			e.flush = nil
			if err := run(); err != nil {
				t.Fatal(err)
			}
			if len(s.SessionsKnowing(1)) != 0 || len(revisions) != 1 || revisions[0] != 7 {
				t.Fatalf("exit revisions=%v", revisions)
			}
			s.JoinVisibilityGroup(sid, "g")
			if err := run(); err != nil {
				t.Fatal(err)
			}
			if len(s.SessionsKnowing(1)) != 1 {
				t.Fatal("same-revision re-entry not known")
			}
			s.DeleteEntity(1)
			if err := run(); err != nil {
				t.Fatal(err)
			}
			if len(revisions) != 2 || revisions[1] != 8 {
				t.Fatalf("deletion revisions=%v", revisions)
			}
		})
	}
}

func TestEventualSendCorePartialFailureAndOwnership(t *testing.T) {
	s, peer, _, sid := replicationTestPeer(t, 0)
	tracker := newEventualStateTracker()
	for id := int64(1); id <= 3; id++ {
		tracker.markDirtyMask(id, 1)
	}
	frame := func(id int64, value byte) eventualPreparedFrame {
		data := bytes.Repeat([]byte{value}, golemnet.EventualStateDatagramPayloadBudget()-4)
		return eventualPreparedFrame{change: eventualStateChange{id: id, mask: 1}, live: true, frame: eventualStateFrame{datagram: data, payloadLen: golemnet.CompactStateDatagramFramePayloadLen(data), fitsDatagram: true}}
	}
	first, second := frame(1, 'a'), frame(2, 'b')
	boom := errors.New("serialization failed after a submitted packet")
	for _, e := range []*deliveryContractEntity{
		{maskAwareEventualEntity: maskAwareEventualEntity{id: 1}, compact: first.frame.datagram},
		{maskAwareEventualEntity: maskAwareEventualEntity{id: 2}, compact: second.frame.datagram},
		{maskAwareEventualEntity: maskAwareEventualEntity{id: 3}, compactErr: boom},
	} {
		if err := s.CreateEntity(e); err != nil {
			t.Fatal(err)
		}
	}
	stats, err := s.sendEventualStateChanges(sid, tracker, nil, []eventualStateChange{first.change, second.change, {id: 3, mask: 1}})
	if !errors.Is(err, boom) || stats.datagramWirePayloads != 1 || stats.datagramBatchedFrames != 1 {
		t.Fatalf("partial stats=%+v err=%v", stats, err)
	}
	if _, dirty := tracker.dirtySet[1]; dirty || len(tracker.dirtySet) != 2 || len(tracker.inFlight) != 1 {
		t.Fatal("partial send changed unsent dirty state")
	}
	// Reuse descriptors and the unsent packet buffer before consuming transport output.
	third := frame(3, 'c')
	if _, err := s.sendPreparedEventualStateFrames(sid, tracker, []eventualPreparedFrame{third}); err != nil {
		t.Fatal(err)
	}
	for token, changes := range tracker.inFlight {
		if len(changes) != 1 || changes[0].id == 2 {
			t.Fatalf("aliased in-flight descriptors: %d: %+v", token, changes)
		}
	}
	seen := map[byte]bool{}
	for i := 0; i < 2; i++ {
		data := receiveReplicationDatagram(t, peer)
		for _, f := range []eventualPreparedFrame{first, third} {
			packed := golemnet.AppendCompactStateDatagramFrame(nil, f.frame.datagram)
			if bytes.HasSuffix(data, packed) {
				seen[f.frame.datagram[0]] = true
			}
		}
	}
	if !seen['a'] || !seen['c'] {
		t.Fatalf("queued payload bytes changed: %v", seen)
	}
	if _, dirty := tracker.dirtySet[2]; !dirty {
		t.Fatal("unsent second frame cleared")
	}
}

func TestEventualCachedAndPreparedPackingParity(t *testing.T) {
	s, peer, stream, sid := replicationTestPeer(t, 0)
	// Two compact frames exactly fill one payload; a full state uses the stream.
	budget := golemnet.EventualStateDatagramPayloadBudget()
	entities := []*deliveryContractEntity{
		{maskAwareEventualEntity: maskAwareEventualEntity{id: 1}, compact: bytes.Repeat([]byte("a"), 500)},
		{maskAwareEventualEntity: maskAwareEventualEntity{id: 2}, compact: bytes.Repeat([]byte("b"), budget-508)},
		{maskAwareEventualEntity: maskAwareEventualEntity{id: 3, full: []byte("stream-full")}},
	}
	changes := []eventualStateChange{{id: 1, mask: 1}, {id: 2, mask: 1}, {id: 3, full: true}, {id: 4, mask: 1}}
	for _, e := range entities {
		if err := s.CreateEntity(e); err != nil {
			t.Fatal(err)
		}
	}
	for _, prepared := range []bool{false, true} {
		tracker := newEventualStateTracker()
		for _, ch := range changes {
			tracker.markDirtyChange(ch)
		}
		var stats eventualStateSendStats
		var err error
		if prepared {
			frames := make([]eventualPreparedFrame, 0, len(changes))
			for _, ch := range changes {
				f, e := s.eventualPreparedFrameForChange(ch)
				if e != nil {
					t.Fatal(e)
				}
				frames = append(frames, f)
			}
			stats, err = s.sendPreparedEventualStateFrames(sid, tracker, frames)
		} else {
			stats, err = s.sendEventualStateChanges(sid, tracker, nil, changes)
		}
		if err != nil || stats.datagramBatchedFrames != 2 || stats.datagramWirePayloads != 1 || stats.streamBatchedFrames != 1 || stats.streamWireMsgs != 1 {
			t.Fatalf("prepared=%v stats=%+v err=%v", prepared, stats, err)
		}
		if tracker.hasDirty() || len(tracker.inFlight) != 1 {
			t.Fatal("wrong dirty/in-flight state")
		}
		packet := receiveReplicationDatagram(t, peer)
		packed := golemnet.AppendCompactStateDatagramFrame(nil, entities[0].compact)
		packed = golemnet.AppendCompactStateDatagramFrame(packed, entities[1].compact)
		if !bytes.HasSuffix(packet, packed) {
			t.Fatal("packing changed")
		}
		if got := mustReadReliableFrame(t, stream); !bytes.Equal(got, WrapEntityUpdate(entities[2].full)) {
			t.Fatalf("fallback=%q", got)
		}
	}
}

type deliveryContractEntity struct {
	maskAwareEventualEntity
	compact    []byte
	compactErr error
}

func (e *deliveryContractEntity) MarshalCompactDeltaMask(uint64) ([]byte, error) {
	return e.compact, e.compactErr
}

func TestEventualPartialStreamFailureKeepsDirty(t *testing.T) {
	s, peer, _, sid := replicationTestPeer(t, 0)
	tracker := newEventualStateTracker()
	tracker.markDirtyMask(1, 1)
	tracker.markDirtyFull(2)
	data := []byte("accepted-datagram")
	frames := []eventualPreparedFrame{
		{change: eventualStateChange{id: 1, mask: 1}, live: true, frame: eventualStateFrame{datagram: data, payloadLen: golemnet.CompactStateDatagramFramePayloadLen(data), fitsDatagram: true}},
		{change: eventualStateChange{id: 2, full: true}, live: true, frame: eventualStateFrame{stream: make([]byte, 256*1024+1)}},
	}
	stats, err := s.sendPreparedEventualStateFrames(sid, tracker, frames)
	if err == nil || stats.datagramWirePayloads != 1 || stats.streamBatchedFrames != 0 {
		t.Fatalf("stats=%+v err=%v", stats, err)
	}
	if _, dirty := tracker.dirtySet[2]; !dirty || len(tracker.dirtySet) != 1 || len(tracker.inFlight) != 1 {
		t.Fatal("failed stream cleared dirty state")
	}
	if got := receiveReplicationDatagram(t, peer); !bytes.HasSuffix(got, data) {
		t.Fatal("earlier packet was not retained")
	}
}

func TestBlindQuietTickRetainsDeferredEventualDirty(t *testing.T) {
	s := NewServer(ServerConfig{})
	s.eventualTracker(7).markDirtyMask(1, 1)
	if err := s.runBroadcastTick(); err != nil {
		t.Fatal(err)
	}
	if !s.eventualTracker(7).hasDirty() || s.nextEventualToken != 0 {
		t.Fatal("quiet-tick retry scheduling changed")
	}
}

func TestReplicationSerializerSessionErrorIsNotSwallowed(t *testing.T) {
	for _, cellSize := range []float64{0, 8} {
		s := NewServer(ServerConfig{CellSize: cellSize, Transport: golemnet.TransportWebSocket, StateUpdateLane: StateUpdateLaneStream})
		httpServer := httptest.NewServer(s.Handler())
		t.Cleanup(httpServer.Close)
		client := mustDialGameClient(t, "ws"+httpServer.URL[4:])
		t.Cleanup(func() { client.CloseNow() })
		sid := waitForSessionIDs(t, s, 1)[0]
		e := &interestTickEntity{id: 1, full: []byte("full")}
		if err := s.CreateEntity(e); err != nil {
			t.Fatal(err)
		}
		s.SetEntityVisibilityGroup(1, "g")
		s.JoinVisibilityGroup(sid, "g")
		if cellSize > 0 {
			s.AssignFOI(sid, 1, 10, 1)
		}
		run := s.runBroadcastTick
		if cellSize > 0 {
			run = s.runInterestTick
		}
		if err := run(); err != nil {
			t.Fatal(err)
		}
		_ = mustReadWrappedMessages(t, client, 1)
		s.LeaveVisibilityGroup(sid, "g")
		s.SetRemovalSerializer(func(int64, uint64) ([]byte, error) { return nil, golemnet.ErrSessionNotFound })
		if err := run(); !errors.Is(err, golemnet.ErrSessionNotFound) {
			t.Fatalf("serializer error swallowed: %v", err)
		}
		client.CloseNow()
		httpServer.Close()
	}
}

func BenchmarkSendEventualConnected(b *testing.B) {
	s, peer, _, sid := replicationTestPeer(b, 0)
	frame := eventualPreparedFrame{change: eventualStateChange{id: 1, mask: 1}, live: true, frame: eventualStateFrame{datagram: bytes.Repeat([]byte("x"), 96), payloadLen: 100, fitsDatagram: true}}
	tracker := newEventualStateTracker()
	var window netwire.SequenceWindow
	var ackBuffer []byte
	s.drainMessages()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		stats, err := s.sendPreparedEventualStateFrames(sid, tracker, []eventualPreparedFrame{frame})
		if err != nil || stats.datagramWirePayloads != 1 {
			b.Fatalf("stats=%+v err=%v", stats, err)
		}
		// Measure sender cost while allowing every owned buffer to cross the real
		// transport boundary. ACK through the protocol so long runs cannot stall.
		b.StopTimer()
		packet, err := netwire.DecodePacket(receiveReplicationDatagram(b, peer))
		if err != nil {
			b.Fatal(err)
		}
		window.Accept(packet.Sequence)
		ackBuffer, err = netwire.EncodePacketInto(ackBuffer, netwire.Packet{Flags: netwire.FlagACKOnly, ACK: window.Snapshot()})
		if err != nil {
			b.Fatal(err)
		}
		if err := peer.SendDatagram(ackBuffer); err != nil {
			b.Fatal(err)
		}
		select {
		case m := <-s.msgQueue:
			if m.kind != msgEventualFeedback {
				b.Fatalf("unexpected event: %v", m.kind)
			}
			tracker.applyFeedback(m.eventualFeedback, nil)
			if len(tracker.inFlight) != 0 {
				b.Fatal("ACK did not release in-flight state")
			}
		case <-time.After(3 * time.Second):
			b.Fatal("feedback timed out")
		}
		b.StartTimer()
	}
}

func expectNoReplicationDatagram(t testing.TB, peer *webtransport.Session) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	if data, err := peer.ReceiveDatagram(ctx); err == nil {
		t.Fatalf("unexpected datagram %q", data)
	}
}

// TestSpaceTransitionGatesDatagramsUntilSpaceAck uses a real WebTransport peer
// in datagram mode: after a transition, deltas use the ordered stream until the
// client acknowledges it, then datagrams resume and first resend what the
// stream carried meanwhile.
func TestSpaceTransitionGatesDatagramsUntilSpaceAck(t *testing.T) {
	for _, interestMode := range []bool{false, true} {
		name, cellSize := "broadcast", 0.0
		if interestMode {
			name, cellSize = "interest", 8
		}
		t.Run(name, func(t *testing.T) {
			s, peer, stream, sid := replicationTestPeer(t, cellSize)
			s.SetRemovalSerializer(func(id int64, _ uint64) ([]byte, error) { return []byte("removed"), nil })
			writer := stream.(io.Writer)
			avatar := &replicationContractEntity{interestTickEntity: interestTickEntity{id: 1, full: []byte("avatar-full"), revision: 1}}
			crate := &replicationContractEntity{interestTickEntity: interestTickEntity{id: 2, x: 1, full: []byte("crate-full"), revision: 1}}
			opts := AvatarOptions{}
			if interestMode {
				opts.FOIRadius, opts.FOIMargin = 10, 1
			}
			if err := s.SpawnAvatar(&Session{ID: sid}, avatar, opts); err != nil {
				t.Fatal(err)
			}
			if err := s.CreateEntity(crate); err != nil {
				t.Fatal(err)
			}
			run := s.runBroadcastTick
			if interestMode {
				run = s.runInterestTick
			}
			tick := func() {
				t.Helper()
				if err := run(); err != nil {
					t.Fatal(err)
				}
			}
			tick()
			spawned := append(decodeServerTokens(t, mustReadReliableFrame(t, stream)), decodeServerTokens(t, mustReadReliableFrame(t, stream))...)
			if slices.Sort(spawned); !slices.Equal(spawned, []string{"avatar-full", "crate-full"}) {
				t.Fatalf("spawn frames = %q", spawned)
			}
			crate.flush, crate.revision = []byte("d0"), 2
			tick()
			if got := receiveReplicationDatagram(t, peer); !bytes.Contains(got, []byte("d0")) {
				t.Fatalf("datagram before transition = %q", got)
			}

			crate.flush = nil
			if err := s.MoveToSpace(SpaceTransfer{To: 3, Entities: []int64{avatar.id, crate.id}}); err != nil {
				t.Fatal(err)
			}
			tick()
			// The whole transition arrives as one logical stream frame.
			got := decodeServerTokens(t, mustReadReliableFrame(t, stream))
			if len(got) != 4 || got[0] != "changed:3:1" || got[3] != "settled:1" || !slices.Contains(got, "avatar-full") || !slices.Contains(got, "crate-full") {
				t.Fatalf("transition frame = %q", got)
			}

			crate.flush, crate.revision = []byte("d1"), 3
			tick()
			if got := decodeServerTokens(t, mustReadReliableFrame(t, stream)); !slices.Equal(got, []string{"d1"}) {
				t.Fatalf("gated delta = %q", got)
			}
			expectNoReplicationDatagram(t, peer)

			// A stale acknowledgement keeps the gate closed.
			if _, err := writer.Write(netwire.AppendFrame(nil, golemnet.ClientSpaceAckControlFrame(7))); err != nil {
				t.Fatal(err)
			}
			if _, err := writer.Write(netwire.AppendFrame(nil, golemnet.ClientSpaceAckControlFrame(1))); err != nil {
				t.Fatal(err)
			}
			waitForSpaceAck(t, s, sid, 1)
			s.applySpaceAcks()
			s.interestMu.Lock()
			_, pending := s.spaceAckPending[sid]
			s.interestMu.Unlock()
			if pending {
				t.Fatal("SpaceAck did not release the gate")
			}

			// No new change: the first datagram resends the field the stream
			// carried while gated, with its current value.
			crate.flush, crate.compact = nil, []byte("current")
			tick()
			if got := receiveReplicationDatagram(t, peer); !bytes.Contains(got, []byte("current")) {
				t.Fatalf("post-ack datagram = %q", got)
			}
			crate.flush, crate.compact, crate.revision = []byte("d3"), nil, 4
			tick()
			if got := receiveReplicationDatagram(t, peer); !bytes.Contains(got, []byte("d3")) {
				t.Fatalf("datagram after ack = %q", got)
			}

			// Back to space 0: a new transition closes the gate again.
			crate.flush = nil
			if err := s.MoveToSpace(SpaceTransfer{To: 0, Entities: []int64{avatar.id, crate.id}}); err != nil {
				t.Fatal(err)
			}
			tick()
			got = decodeServerTokens(t, mustReadReliableFrame(t, stream))
			if len(got) != 4 || got[0] != "changed:0:2" || got[3] != "settled:2" {
				t.Fatalf("return transition frame = %q", got)
			}
			crate.flush, crate.revision = []byte("d4"), 5
			tick()
			if got := decodeServerTokens(t, mustReadReliableFrame(t, stream)); !slices.Equal(got, []string{"d4"}) {
				t.Fatalf("gated delta after return = %q", got)
			}
			expectNoReplicationDatagram(t, peer)
		})
	}
}

// waitForSpaceAck waits until the SpaceAck inbox holds transition for sid.
func waitForSpaceAck(t *testing.T, s *Server, sid int64, transition uint64) {
	t.Helper()
	for deadline := time.Now().Add(3 * time.Second); ; {
		s.spaceAckMu.Lock()
		got := s.spaceAckInbox[sid]
		s.spaceAckMu.Unlock()
		if got == transition {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("SpaceAck %d was not delivered (inbox has %d)", transition, got)
		}
		time.Sleep(time.Millisecond)
	}
}

// eventRecord is one datagram event record: its transition tag (0 when
// untagged) and ServerEvent bytes.
type eventRecord struct {
	transition uint64
	event      string
}

// receiveEventRecords receives one datagram and decodes its event records.
func receiveEventRecords(t testing.TB, peer *webtransport.Session) []eventRecord {
	t.Helper()
	packet, err := netwire.DecodePacket(receiveReplicationDatagram(t, peer))
	if err != nil {
		t.Fatal(err)
	}
	var out []eventRecord
	for payload := packet.Payload; len(payload) > 0; {
		if len(payload) < netwire.FrameHeaderBytes {
			t.Fatalf("truncated datagram frame %x", payload)
		}
		n := int(binary.BigEndian.Uint32(payload))
		frame := payload[netwire.FrameHeaderBytes : netwire.FrameHeaderBytes+n]
		payload = payload[netwire.FrameHeaderBytes+n:]
		transition, event, ok := netwire.EventRecordTransition(frame)
		if !ok {
			t.Fatalf("datagram frame %x is not an event record", frame)
		}
		out = append(out, eventRecord{transition, string(event)})
	}
	return out
}

// TestSpaceTransitionKeepsDatagramEventsOnStreamUntilSpaceAck uses a real
// WebTransport peer in datagram mode: while a transition is unacknowledged,
// datagram-lane events for the session use the ordered stream, so they arrive
// after SpaceSettled; after the ack they use datagrams again, tagged with the
// acknowledged transition. An event sent from OnUpdates on the switch tick
// follows the transition too, whether the session is planned before OnUpdates
// (interest mode with an FOI) or after it (broadcast mode, interest mode
// without an FOI). Events before any switch are untagged.
func TestSpaceTransitionKeepsDatagramEventsOnStreamUntilSpaceAck(t *testing.T) {
	for _, mode := range []string{"broadcast", "interest", "interest-no-foi"} {
		t.Run(mode, func(t *testing.T) {
			cellSize := 0.0
			if mode != "broadcast" {
				cellSize = 8
			}
			s, peer, stream, sid := replicationTestPeer(t, cellSize)
			s.SetRemovalSerializer(func(id int64, _ uint64) ([]byte, error) { return []byte("removed"), nil })
			avatar := &replicationContractEntity{interestTickEntity: interestTickEntity{id: 1, full: []byte("avatar-full"), revision: 1}}
			opts := AvatarOptions{}
			if mode == "interest" {
				opts.FOIRadius, opts.FOIMargin = 10, 1
			}
			if err := s.SpawnAvatar(&Session{ID: sid}, avatar, opts); err != nil {
				t.Fatal(err)
			}
			run := s.runBroadcastTick
			if cellSize > 0 {
				run = s.runInterestTick
			}
			tick := func() {
				t.Helper()
				if err := run(); err != nil {
					t.Fatal(err)
				}
			}
			tick()
			if mode != "interest-no-foi" {
				if got := decodeServerTokens(t, mustReadReliableFrame(t, stream)); !slices.Equal(got, []string{"avatar-full"}) {
					t.Fatalf("spawn frame = %q", got)
				}
			}
			if err := s.SendServerEvent(sid, EventUnreliable, []byte("evt-before")); err != nil {
				t.Fatal(err)
			}
			if got := receiveEventRecords(t, peer); !slices.Equal(got, []eventRecord{{0, "evt-before"}}) {
				t.Fatalf("event before transition = %+v", got)
			}

			if err := s.MoveToSpace(SpaceTransfer{To: 3, Entities: []int64{avatar.id}}); err != nil {
				t.Fatal(err)
			}
			// A spawn nobody sees makes OnUpdates run on the switch tick.
			if err := s.CreateEntityIn(9, &interestTickEntity{id: 50, full: []byte("hidden-full")}); err != nil {
				t.Fatal(err)
			}
			s.OnUpdates(func([][]byte) {
				if err := s.SendServerEvent(sid, EventUnreliable, []byte("evt-on-updates")); err != nil {
					t.Error(err)
				}
			})
			tick()
			s.OnUpdates(nil)
			want := []string{"changed:3:1", "avatar-full", "settled:1"}
			if mode == "interest-no-foi" {
				want = []string{"changed:3:1", "settled:1"}
			}
			if got := decodeServerTokens(t, mustReadReliableFrame(t, stream)); !slices.Equal(got, want) {
				t.Fatalf("transition frame = %q", got)
			}
			if got := decodeServerTokens(t, mustReadReliableFrame(t, stream)); !slices.Equal(got, []string{"event:evt-on-updates"}) {
				t.Fatalf("OnUpdates event on the switch tick = %q", got)
			}
			if err := s.SendServerEvent(sid, EventUnreliable, []byte("evt-gated")); err != nil {
				t.Fatal(err)
			}
			if err := s.Space(3).BroadcastServerEvent(EventReliableOrdered, []byte("evt-space")); err != nil {
				t.Fatal(err)
			}
			if got := decodeServerTokens(t, mustReadReliableFrame(t, stream)); !slices.Equal(got, []string{"event:evt-gated"}) {
				t.Fatalf("gated event = %q", got)
			}
			if got := decodeServerTokens(t, mustReadReliableFrame(t, stream)); !slices.Equal(got, []string{"event:evt-space"}) {
				t.Fatalf("gated space event = %q", got)
			}
			expectNoReplicationDatagram(t, peer)

			if _, err := stream.(io.Writer).Write(netwire.AppendFrame(nil, golemnet.ClientSpaceAckControlFrame(1))); err != nil {
				t.Fatal(err)
			}
			waitForSpaceAck(t, s, sid, 1)
			s.drainMessages()
			if err := s.SendServerEvent(sid, EventUnreliable, []byte("evt-after")); err != nil {
				t.Fatal(err)
			}
			if got := receiveEventRecords(t, peer); !slices.Equal(got, []eventRecord{{1, "evt-after"}}) {
				t.Fatalf("event after ack = %+v", got)
			}
			s.eventHoldMu.Lock()
			holds := len(s.eventHolds)
			s.eventHoldMu.Unlock()
			if holds != 0 {
				t.Fatalf("%d sessions still hold events after the pass", holds)
			}
		})
	}
}

// eventPeer is one WebTransport client of a running server.
type eventPeer struct {
	peer   *webtransport.Session
	stream *webtransport.Stream
	sess   *Session
}

// TestSpaceSwitchHoldsEventsSentBeforePlanning runs a real Server.Run with
// WebTransport peers in datagram mode: unreliable events sent from a Post and
// from OnTick right after MoveToSpace of the session's avatar, on the same
// tick, arrive on the stream after SpaceSettled, while a session that does not
// switch keeps its datagram lane in that tick. After the SpaceAck, datagram
// events carry the new transition; single-space sessions stay untagged.
func TestSpaceSwitchHoldsEventsSentBeforePlanning(t *testing.T) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := pc.LocalAddr().String()
	_ = pc.Close()
	s := NewServer(ServerConfig{Addr: addr, DevSelfSignedCert: true, TickRate: 100})
	s.SetRemovalSerializer(func(id int64, _ uint64) ([]byte, error) { return []byte("removed"), nil })
	connected := make(chan *Session, 2)
	s.OnConnect(func(sess *Session) { connected <- sess })
	var onTick atomic.Pointer[func()]
	s.OnTick(func(float64, *Server) {
		if fn := onTick.Swap(nil); fn != nil {
			(*fn)()
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- s.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-runDone:
		case <-time.After(5 * time.Second):
			t.Error("Run did not stop")
		}
	})
	readyCtx, readyCancel := context.WithTimeout(ctx, 5*time.Second)
	defer readyCancel()
	if err := s.WaitReady(readyCtx); err != nil {
		t.Fatal(err)
	}
	dialer := &webtransport.Dialer{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	t.Cleanup(func() { _ = dialer.Close() })
	connect := func() eventPeer {
		t.Helper()
		_, peer, err := dialer.Dial(readyCtx, "https://"+addr+"/wt", nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = peer.CloseWithError(0, "") })
		stream, err := peer.OpenStreamSync(readyCtx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := stream.Write(nil); err != nil {
			t.Fatal(err)
		}
		_ = stream.SetReadDeadline(time.Now().Add(10 * time.Second))
		select {
		case sess := <-connected:
			return eventPeer{peer, stream, sess}
		case <-readyCtx.Done():
			t.Fatal("session was not admitted")
		}
		return eventPeer{}
	}
	mover, bystander := connect(), connect()
	post := func(fn func()) {
		t.Helper()
		done := make(chan struct{})
		if err := s.Post(func(*Server) { fn(); close(done) }); err != nil {
			t.Fatal(err)
		}
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("Post callback did not run")
		}
	}
	send := func(sid int64, name string) {
		if err := s.SendServerEvent(sid, EventUnreliable, []byte(name)); err != nil {
			t.Error(err)
		}
	}

	avatar := &interestTickEntity{id: 1, full: []byte("avatar-full"), revision: 1}
	post(func() {
		if err := s.SpawnAvatar(mover.sess, avatar, AvatarOptions{}); err != nil {
			t.Error(err)
		}
	})
	for _, p := range []eventPeer{mover, bystander} {
		if got := decodeServerTokens(t, mustReadReliableFrame(t, p.stream)); !slices.Equal(got, []string{"avatar-full"}) {
			t.Fatalf("spawn frame = %q", got)
		}
	}
	post(func() { send(mover.sess.ID, "evt-single-space") })
	if got := receiveEventRecords(t, mover.peer); !slices.Equal(got, []eventRecord{{0, "evt-single-space"}}) {
		t.Fatalf("single-space event = %+v", got)
	}

	post(func() {
		if err := s.MoveToSpace(SpaceTransfer{To: 3, Entities: []int64{avatar.id}}); err != nil {
			t.Error(err)
		}
		send(mover.sess.ID, "evt-post")
		send(bystander.sess.ID, "evt-bystander")
		fn := func() { send(mover.sess.ID, "evt-tick") }
		onTick.Store(&fn)
	})
	// The bystander's event is not held: it leaves on its datagram lane.
	if got := receiveEventRecords(t, bystander.peer); !slices.Equal(got, []eventRecord{{0, "evt-bystander"}}) {
		t.Fatalf("bystander event = %+v", got)
	}
	if got := decodeServerTokens(t, mustReadReliableFrame(t, mover.stream)); !slices.Equal(got, []string{"changed:3:1", "avatar-full", "settled:1"}) {
		t.Fatalf("transition frame = %q", got)
	}
	var held []string
	for len(held) < 2 {
		held = append(held, decodeServerTokens(t, mustReadReliableFrame(t, mover.stream))...)
	}
	if !slices.Equal(held, []string{"event:evt-post", "event:evt-tick"}) {
		t.Fatalf("events sent before planning = %q", held)
	}
	if got := decodeServerTokens(t, mustReadReliableFrame(t, bystander.stream)); !slices.Equal(got, []string{"removed"}) {
		t.Fatalf("bystander exit = %q", got)
	}
	expectNoReplicationDatagram(t, mover.peer)

	if _, err := mover.stream.Write(netwire.AppendFrame(nil, golemnet.ClientSpaceAckControlFrame(1))); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(3 * time.Second); ; {
		s.interestMu.Lock()
		_, pending := s.spaceAckPending[mover.sess.ID]
		s.interestMu.Unlock()
		if !pending {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("SpaceAck did not release the gate")
		}
		time.Sleep(time.Millisecond)
	}
	post(func() {
		send(mover.sess.ID, "evt-after")
		send(bystander.sess.ID, "evt-bystander-after")
	})
	if got := receiveEventRecords(t, mover.peer); !slices.Equal(got, []eventRecord{{1, "evt-after"}}) {
		t.Fatalf("event after ack = %+v", got)
	}
	if got := receiveEventRecords(t, bystander.peer); !slices.Equal(got, []eventRecord{{0, "evt-bystander-after"}}) {
		t.Fatalf("bystander event after ack = %+v", got)
	}
}

// TestSpaceTransitionStreamLaneWebTransportKeepsGateUntilSpaceAck: a
// WebTransport session whose state updates use the stream still has datagram
// event lanes, so the gate and stream-only events last until its SpaceAck.
func TestSpaceTransitionStreamLaneWebTransportKeepsGateUntilSpaceAck(t *testing.T) {
	s, peer, stream, sid := replicationTestPeerWith(t, ServerConfig{Transport: golemnet.TransportWebTransport, StateUpdateLane: StateUpdateLaneStream})
	avatar := &interestTickEntity{id: 1, full: []byte("avatar-full"), revision: 1}
	if err := s.SpawnAvatar(&Session{ID: sid}, avatar, AvatarOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := s.runBroadcastTick(); err != nil {
		t.Fatal(err)
	}
	if got := decodeServerTokens(t, mustReadReliableFrame(t, stream)); !slices.Equal(got, []string{"avatar-full"}) {
		t.Fatalf("spawn frame = %q", got)
	}
	if err := s.SetEntitySpace(avatar.id, 2); err != nil {
		t.Fatal(err)
	}
	if err := s.runBroadcastTick(); err != nil {
		t.Fatal(err)
	}
	if got := decodeServerTokens(t, mustReadReliableFrame(t, stream)); !slices.Equal(got, []string{"changed:2:1", "avatar-full", "settled:1"}) {
		t.Fatalf("transition frame = %q", got)
	}
	s.interestMu.Lock()
	_, pending := s.spaceAckPending[sid]
	s.interestMu.Unlock()
	if !pending {
		t.Fatal("stream-lane WebTransport session released the gate before its SpaceAck")
	}
	if err := s.SendServerEvent(sid, EventReliableUnordered, []byte("evt-gated")); err != nil {
		t.Fatal(err)
	}
	if got := decodeServerTokens(t, mustReadReliableFrame(t, stream)); !slices.Equal(got, []string{"event:evt-gated"}) {
		t.Fatalf("gated event = %q", got)
	}
	expectNoReplicationDatagram(t, peer)

	if _, err := stream.(io.Writer).Write(netwire.AppendFrame(nil, golemnet.ClientSpaceAckControlFrame(1))); err != nil {
		t.Fatal(err)
	}
	waitForSpaceAck(t, s, sid, 1)
	s.drainMessages()
	s.interestMu.Lock()
	_, pending = s.spaceAckPending[sid]
	s.interestMu.Unlock()
	if pending {
		t.Fatal("SpaceAck did not release the gate")
	}
	if err := s.SendServerEvent(sid, EventUnreliable, []byte("evt-after")); err != nil {
		t.Fatal(err)
	}
	if got := receiveReplicationDatagram(t, peer); !bytes.Contains(got, []byte("evt-after")) {
		t.Fatalf("event after ack = %q", got)
	}
}

// TestSpaceSwitchEventHoldReleasesUnswitchedAndKeepsUnplannedOnce uses a real
// WebTransport peer in interest mode: a session that was marked as switching
// but ends the tick in its announced space gets its held events at the end of
// the pass on their datagram lane; a session that changes space after its pass
// was planned (from OnUpdates) keeps holding until the next pass queues its
// transition, so events sent in between follow SpaceSettled.
func TestSpaceSwitchEventHoldReleasesUnswitchedAndKeepsUnplannedOnce(t *testing.T) {
	s, peer, stream, sid := replicationTestPeer(t, 8)
	s.SetRemovalSerializer(func(id int64, _ uint64) ([]byte, error) { return []byte("removed"), nil })
	avatar := &interestTickEntity{id: 1, full: []byte("avatar-full"), revision: 1}
	if err := s.SpawnAvatar(&Session{ID: sid}, avatar, AvatarOptions{FOIRadius: 10, FOIMargin: 1}); err != nil {
		t.Fatal(err)
	}
	tick := func() {
		t.Helper()
		if err := s.runInterestTick(); err != nil {
			t.Fatal(err)
		}
	}
	holds := func() map[int64]bool {
		s.eventHoldMu.Lock()
		defer s.eventHoldMu.Unlock()
		return maps.Clone(s.eventHolds)
	}
	tick()
	if got := decodeServerTokens(t, mustReadReliableFrame(t, stream)); !slices.Equal(got, []string{"avatar-full"}) {
		t.Fatalf("spawn frame = %q", got)
	}

	// There and back within one tick: held, then released on its lane.
	if err := s.MoveToSpace(SpaceTransfer{To: 3, Entities: []int64{avatar.id}}); err != nil {
		t.Fatal(err)
	}
	if err := s.SendServerEvent(sid, EventUnreliable, []byte("evt-unswitched")); err != nil {
		t.Fatal(err)
	}
	if err := s.MoveToSpace(SpaceTransfer{To: 0, Entities: []int64{avatar.id}}); err != nil {
		t.Fatal(err)
	}
	expectNoReplicationDatagram(t, peer)
	tick()
	if got := receiveEventRecords(t, peer); !slices.Equal(got, []eventRecord{{0, "evt-unswitched"}}) {
		t.Fatalf("event of a session that did not switch = %+v", got)
	}
	if h := holds(); len(h) != 0 {
		t.Fatalf("holds after an unswitched pass = %v", h)
	}

	// A move from OnUpdates comes after this pass planned the session.
	if err := s.CreateEntityIn(9, &interestTickEntity{id: 50, full: []byte("hidden-full")}); err != nil {
		t.Fatal(err)
	}
	s.OnUpdates(func([][]byte) {
		if err := s.MoveToSpace(SpaceTransfer{To: 4, Entities: []int64{avatar.id}}); err != nil {
			t.Error(err)
		}
		if err := s.SendServerEvent(sid, EventUnreliable, []byte("evt-late")); err != nil {
			t.Error(err)
		}
	})
	tick()
	s.OnUpdates(nil)
	if h := holds(); len(h) != 1 || !h[sid] {
		t.Fatalf("holds after an unplanned switch = %v, want the session kept once", h)
	}
	expectNoReplicationDatagram(t, peer)
	// As from a Post on the next tick, before replication.
	if err := s.SendServerEvent(sid, EventUnreliable, []byte("evt-next-tick")); err != nil {
		t.Fatal(err)
	}
	tick()
	if got := decodeServerTokens(t, mustReadReliableFrame(t, stream)); !slices.Equal(got, []string{"changed:4:1", "avatar-full", "settled:1"}) {
		t.Fatalf("transition frame = %q", got)
	}
	var held []string
	for len(held) < 2 {
		held = append(held, decodeServerTokens(t, mustReadReliableFrame(t, stream))...)
	}
	if !slices.Equal(held, []string{"event:evt-late", "event:evt-next-tick"}) {
		t.Fatalf("events held across the unplanned switch = %q", held)
	}
	if h := holds(); len(h) != 0 {
		t.Fatalf("holds after the transition = %v", h)
	}
}

// entityResyncInbox returns the IDs stored for sid, sorted.
func entityResyncInbox(s *Server, sid int64) []int64 {
	s.entityResyncMu.Lock()
	defer s.entityResyncMu.Unlock()
	var ids []int64
	for id := range s.entityResyncInbox[sid] {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

// entityResyncSentinel is a SpaceAck transition no test plans.
const entityResyncSentinel = 1 << 40

// sendEntityResyncs writes EntityResync control frames for ids on the client
// stream, then a SpaceAck sentinel, and waits for the sentinel: the connection
// goroutine handles control frames in order, so every request has been stored
// or dropped by then. The sentinel is consumed (and ignored) right away.
func sendEntityResyncs(t *testing.T, s *Server, stream io.Reader, sid int64, ids ...int64) {
	t.Helper()
	writer := stream.(io.Writer)
	for _, id := range ids {
		if _, err := writer.Write(netwire.AppendFrame(nil, golemnet.ClientEntityResyncControlFrame(id))); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := writer.Write(netwire.AppendFrame(nil, golemnet.ClientSpaceAckControlFrame(entityResyncSentinel))); err != nil {
		t.Fatal(err)
	}
	waitForSpaceAck(t, s, sid, entityResyncSentinel)
	s.applySpaceAcks()
}

// expectStreamMarker queues a marker frame and checks it is the next frame
// the client reads, so nothing else was sent before it.
func expectStreamMarker(t *testing.T, s *Server, stream io.Reader, sid int64) {
	t.Helper()
	if err := s.listener.SendBatch(sid, [][]byte{s.listener.Wrap([]byte("marker"))}); err != nil {
		t.Fatal(err)
	}
	if got := decodeServerTokens(t, mustReadReliableFrame(t, stream)); !slices.Equal(got, []string{"marker"}) {
		t.Fatalf("frame before the marker = %q", got)
	}
}

// TestEntityResyncAnsweredByReplicationPass uses a real WebTransport peer in
// datagram mode: an EntityResync request is stored only for an entity the
// session knows, and the next replication pass answers it on the ordered
// stream with the entity's full state in the session's current owner or
// public view; unknown and other-space entities are dropped on receipt.
func TestEntityResyncAnsweredByReplicationPass(t *testing.T) {
	for _, interestMode := range []bool{false, true} {
		name, cellSize := "broadcast", 0.0
		if interestMode {
			name, cellSize = "interest", 8
		}
		t.Run(name, func(t *testing.T) {
			s, _, stream, sid := replicationTestPeer(t, cellSize)
			avatar := &interestTickEntity{id: 1, full: []byte("avatar-full"), revision: 1}
			crate := &ownerScopedTickEntity{interestTickEntity: interestTickEntity{id: 2, x: 1, full: []byte("crate-full"), revision: 1}, publicFull: []byte("crate-public")}
			elsewhere := &interestTickEntity{id: 3, full: []byte("elsewhere-full"), revision: 1}
			opts := AvatarOptions{}
			if interestMode {
				opts.FOIRadius, opts.FOIMargin = 10, 1
			}
			if err := s.SpawnAvatar(&Session{ID: sid}, avatar, opts); err != nil {
				t.Fatal(err)
			}
			if err := s.CreateEntity(crate, sid+100); err != nil {
				t.Fatal(err)
			}
			if err := s.CreateEntityIn(5, elsewhere); err != nil {
				t.Fatal(err)
			}
			run := s.runBroadcastTick
			if interestMode {
				run = s.runInterestTick
			}
			tick := func() {
				t.Helper()
				if err := run(); err != nil {
					t.Fatal(err)
				}
			}
			tick()
			spawned := append(decodeServerTokens(t, mustReadReliableFrame(t, stream)), decodeServerTokens(t, mustReadReliableFrame(t, stream))...)
			if slices.Sort(spawned); !slices.Equal(spawned, []string{"avatar-full", "crate-public"}) {
				t.Fatalf("spawn frames = %q", spawned)
			}

			// Only the known crate is stored; drainMessages does not answer.
			sendEntityResyncs(t, s, stream, sid, 2, 3, 99)
			if got := entityResyncInbox(s, sid); !slices.Equal(got, []int64{2}) {
				t.Fatalf("stored requests = %v, want [2]", got)
			}
			s.drainMessages()
			tick()
			if got := decodeServerTokens(t, mustReadReliableFrame(t, stream)); !slices.Equal(got, []string{"crate-public"}) {
				t.Fatalf("resync frame = %q", got)
			}
			if got := entityResyncInbox(s, sid); len(got) != 0 {
				t.Fatalf("requests left after the pass = %v", got)
			}
			sendEntityResyncs(t, s, stream, sid, 1, 2)
			tick()
			// In ID order, one logical frame each.
			if got := append(decodeServerTokens(t, mustReadReliableFrame(t, stream)), decodeServerTokens(t, mustReadReliableFrame(t, stream))...); !slices.Equal(got, []string{"avatar-full", "crate-public"}) {
				t.Fatalf("resync frames = %q", got)
			}
			// The view follows the current owner (the grant comes first).
			if !s.SetOwner(crate.id, sid) {
				t.Fatal("SetOwner")
			}
			tick()
			if got := decodeServerTokens(t, mustReadReliableFrame(t, stream)); !slices.Equal(got, []string{"crate-full"}) {
				t.Fatalf("ownership grant = %q", got)
			}
			sendEntityResyncs(t, s, stream, sid, 2)
			tick()
			if got := decodeServerTokens(t, mustReadReliableFrame(t, stream)); !slices.Equal(got, []string{"crate-full"}) {
				t.Fatalf("owner resync frame = %q", got)
			}
			tick()
			expectStreamMarker(t, s, stream, sid)
		})
	}
}

// TestEntityResyncBurstBeyondHeldRecordCap: a burst of more than 256
// requests (one per evicted entity) is stored and answered in full by one
// pass, which leaves the blind broadcast path for it.
func TestEntityResyncBurstBeyondHeldRecordCap(t *testing.T) {
	s, _, stream, sid := replicationTestPeer(t, 0)
	const n = 300
	want := make([]string, 0, n)
	ids := make([]int64, 0, n)
	for i := int64(1); i <= n; i++ {
		full := fmt.Sprintf("e%d-full", i)
		if err := s.CreateEntity(&interestTickEntity{id: i, full: []byte(full), revision: 1}); err != nil {
			t.Fatal(err)
		}
		want, ids = append(want, full), append(ids, i)
	}
	readAll := func(count int) []string {
		t.Helper()
		var got []string
		for len(got) < count {
			got = append(got, decodeServerTokens(t, mustReadReliableFrame(t, stream))...)
		}
		slices.Sort(got)
		return got
	}
	slices.Sort(want)
	if err := s.runBroadcastTick(); err != nil {
		t.Fatal(err)
	}
	if got := readAll(n); !slices.Equal(got, want) {
		t.Fatalf("spawned %d frames", len(got))
	}
	s.interestMu.Lock()
	blind := !s.shouldFilterBroadcastLocked(s.listener.SessionIDs(), s.reg.All(), false)
	s.interestMu.Unlock()
	if !blind {
		t.Fatal("setup: the quiet pass should be blind-eligible")
	}
	sendEntityResyncs(t, s, stream, sid, ids...)
	if got := entityResyncInbox(s, sid); len(got) != n {
		t.Fatalf("stored %d requests, want %d", len(got), n)
	}
	if err := s.runBroadcastTick(); err != nil {
		t.Fatal(err)
	}
	if got := readAll(n); !slices.Equal(got, want) {
		t.Fatalf("resync answered %d frames, want %d", len(got), n)
	}
	if err := s.runBroadcastTick(); err != nil {
		t.Fatal(err)
	}
	expectStreamMarker(t, s, stream, sid)
}

// TestEntityResyncNotAnsweredAcrossSpaceBoundary: a request for an entity
// that moves to another space before the pass (as from a Post) is not
// answered; the session gets the entity's exit instead.
func TestEntityResyncNotAnsweredAcrossSpaceBoundary(t *testing.T) {
	for _, interestMode := range []bool{false, true} {
		name, cellSize := "broadcast", 0.0
		if interestMode {
			name, cellSize = "interest", 8
		}
		t.Run(name, func(t *testing.T) {
			s, _, stream, sid := replicationTestPeer(t, cellSize)
			s.SetRemovalSerializer(func(id int64, _ uint64) ([]byte, error) { return []byte(fmt.Sprintf("removed:%d", id)), nil })
			avatar := &interestTickEntity{id: 1, full: []byte("avatar-full"), revision: 1}
			crate := &interestTickEntity{id: 2, x: 1, full: []byte("crate-full"), revision: 1}
			opts := AvatarOptions{}
			if interestMode {
				opts.FOIRadius, opts.FOIMargin = 10, 1
			}
			if err := s.SpawnAvatar(&Session{ID: sid}, avatar, opts); err != nil {
				t.Fatal(err)
			}
			if err := s.CreateEntity(crate); err != nil {
				t.Fatal(err)
			}
			run := s.runBroadcastTick
			if interestMode {
				run = s.runInterestTick
			}
			if err := run(); err != nil {
				t.Fatal(err)
			}
			decodeServerTokens(t, mustReadReliableFrame(t, stream))
			decodeServerTokens(t, mustReadReliableFrame(t, stream))
			sendEntityResyncs(t, s, stream, sid, crate.id)
			if got := entityResyncInbox(s, sid); !slices.Equal(got, []int64{crate.id}) {
				t.Fatalf("stored requests = %v", got)
			}
			if err := s.SetEntitySpace(crate.id, 5); err != nil {
				t.Fatal(err)
			}
			if err := run(); err != nil {
				t.Fatal(err)
			}
			if got := decodeServerTokens(t, mustReadReliableFrame(t, stream)); !slices.Equal(got, []string{"removed:2"}) {
				t.Fatalf("pass after the move = %q", got)
			}
			if err := run(); err != nil {
				t.Fatal(err)
			}
			expectStreamMarker(t, s, stream, sid)
		})
	}
}

// TestEntityResyncIgnoredOnStreamStateLane: sessions that never receive
// compact datagram records have their resync requests dropped on receipt.
func TestEntityResyncIgnoredOnStreamStateLane(t *testing.T) {
	s, _, stream, sid := replicationTestPeerWith(t, ServerConfig{Transport: golemnet.TransportWebTransport, StateUpdateLane: StateUpdateLaneStream})
	avatar := &interestTickEntity{id: 1, full: []byte("avatar-full"), revision: 1}
	if err := s.SpawnAvatar(&Session{ID: sid}, avatar, AvatarOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := s.runBroadcastTick(); err != nil {
		t.Fatal(err)
	}
	if got := decodeServerTokens(t, mustReadReliableFrame(t, stream)); !slices.Equal(got, []string{"avatar-full"}) {
		t.Fatalf("spawn frame = %q", got)
	}
	sendEntityResyncs(t, s, stream, sid, avatar.id)
	if got := entityResyncInbox(s, sid); len(got) != 0 {
		t.Fatalf("stored requests = %v", got)
	}
	if err := s.runBroadcastTick(); err != nil {
		t.Fatal(err)
	}
	expectStreamMarker(t, s, stream, sid)
}

// revisionBumpEntity counts BumpStateRevision calls.
type revisionBumpEntity struct {
	interestTickEntity
	bumps int
}

func (e *revisionBumpEntity) BumpStateRevision() { e.bumps++; e.revision++ }

func TestSetOwnerBumpsStateRevisionOnChange(t *testing.T) {
	s := NewServer(ServerConfig{})
	e := &revisionBumpEntity{interestTickEntity: interestTickEntity{id: 1, revision: 4}}
	if err := s.CreateEntity(e, 7); err != nil {
		t.Fatal(err)
	}
	for _, owner := range []int64{7, 8, 8, 0, 0} {
		if !s.SetOwner(e.id, owner) {
			t.Fatalf("SetOwner(%d)", owner)
		}
	}
	if e.bumps != 2 || e.revision != 6 {
		t.Fatalf("bumps=%d revision=%d, want one bump per owner change", e.bumps, e.revision)
	}
	if s.SetOwner(99, 1) || e.bumps != 2 {
		t.Fatal("unknown entity changed revisions")
	}
}
