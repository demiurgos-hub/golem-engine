package golem

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http/httptest"
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
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := pc.LocalAddr().String()
	_ = pc.Close()
	s := NewServer(ServerConfig{Addr: addr, CellSize: cellSize, DevSelfSignedCert: true})
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
