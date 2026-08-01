package golem

import (
	"bytes"
	"fmt"
	"net/http/httptest"
	"sync"
	"testing"

	golemnet "github.com/demiurgos-hub/golem-engine/golem/net"
	"github.com/demiurgos-hub/golem-engine/golem/pb"
	"github.com/demiurgos-hub/golem-engine/golem/registry"
)

// ownerScopedTickEntity is a test entity with private/public full and delta payloads.
type ownerScopedTickEntity struct {
	interestTickEntity
	publicFull    []byte
	publicCompact []byte
	publicFlush   []byte
	ownerMask     uint64
}

func (e *ownerScopedTickEntity) FlushUpdate() ([]byte, error) {
	e.flushCalls++
	if e.flush == nil {
		return nil, nil
	}
	// Preserve caller-provided flushMask (interestTickEntity overwrites it to 1).
	return e.flush, nil
}

func (e *ownerScopedTickEntity) PublicFullUpdate() ([]byte, error) {
	return e.publicFull, nil
}

func (e *ownerScopedTickEntity) PublicReplicationMask(mask uint64) uint64 {
	return mask &^ e.ownerMask
}

func (e *ownerScopedTickEntity) MarshalCompactDeltaMask(mask uint64) ([]byte, error) {
	if mask == 0 {
		return nil, nil
	}
	body := e.compact
	if mask&e.ownerMask == 0 && e.publicCompact != nil {
		body = e.publicCompact
	}
	if body == nil {
		body = e.flush
	}
	if body == nil {
		return nil, nil
	}
	w := &pb.Writer{}
	w.Int64(e.id)
	w.Uint64(e.revision)
	w.Uint64(mask)
	w.Raw(body)
	return w.Finish(), nil
}

func (e *ownerScopedTickEntity) MarshalDeltaMask(mask uint64) ([]byte, error) {
	if mask == 0 {
		return nil, nil
	}
	if mask&e.ownerMask == 0 && e.publicFlush != nil {
		return e.publicFlush, nil
	}
	return e.flush, nil
}

var _ registry.OwnerScopedEntity = (*ownerScopedTickEntity)(nil)

func TestBroadcastOwnerVsNonOwnerSpawnAndDelta(t *testing.T) {
	srv := NewServer(ServerConfig{
		Transport:       golemnet.TransportWebSocket,
		StateUpdateLane: StateUpdateLaneStream,
	})
	srv.SetRemovalSerializer(func(entityID int64, _ uint64) ([]byte, error) {
		return []byte(fmt.Sprintf("removed:%d", entityID)), nil
	})

	httpSrv := httptest.NewServer(srv.Handler())
	defer httpSrv.Close()
	clientA := mustDialGameClient(t, "ws"+httpSrv.URL[4:])
	defer clientA.CloseNow()
	clientB := mustDialGameClient(t, "ws"+httpSrv.URL[4:])
	defer clientB.CloseNow()
	ids := waitForSessionIDs(t, srv, 2)
	ownerSID := ids[0]

	ent := &ownerScopedTickEntity{
		interestTickEntity: interestTickEntity{
			id:        10,
			full:      []byte("auth-full"),
			flush:     []byte("auth-delta"),
			flushMask: 0b111,
		},
		publicFull:  []byte("public-full"),
		publicFlush: []byte("public-delta"),
		ownerMask:   0b100,
	}
	if err := srv.CreateEntity(ent, ownerSID); err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}
	if err := srv.runBroadcastTick(); err != nil {
		t.Fatalf("spawn tick: %v", err)
	}

	batchA := mustReadWrappedMessages(t, clientA, 1)[0]
	batchB := mustReadWrappedMessages(t, clientB, 1)[0]
	ownerBatch, otherBatch := batchA, batchB
	ownerConn, otherConn := clientA, clientB
	if !containsPayload(batchA, []byte("auth-full")) {
		ownerBatch, otherBatch = batchB, batchA
		ownerConn, otherConn = clientB, clientA
	}
	if !containsPayload(ownerBatch, []byte("auth-full")) {
		t.Fatalf("owner missing auth full: %v", ownerBatch)
	}
	if containsPayload(ownerBatch, []byte("public-full")) {
		t.Fatalf("owner unexpectedly received public full")
	}
	if !containsPayload(otherBatch, []byte("public-full")) {
		t.Fatalf("non-owner missing public full: %v", otherBatch)
	}
	if containsPayload(otherBatch, []byte("auth-full")) {
		t.Fatalf("non-owner leaked auth full")
	}

	ent.flush = []byte("auth-delta-2")
	ent.publicFlush = []byte("public-delta-2")
	ent.flushMask = 0b111
	if err := srv.runBroadcastTick(); err != nil {
		t.Fatalf("delta tick: %v", err)
	}
	ownerDelta := mustReadWrappedMessages(t, ownerConn, 1)[0]
	otherDelta := mustReadWrappedMessages(t, otherConn, 1)[0]
	if !containsPayload(ownerDelta, []byte("auth-delta-2")) {
		t.Fatalf("owner missing auth delta: %v", ownerDelta)
	}
	if !containsPayload(otherDelta, []byte("public-delta-2")) {
		t.Fatalf("non-owner missing public delta: %v", otherDelta)
	}
	if containsPayload(otherDelta, []byte("auth-delta-2")) {
		t.Fatalf("non-owner leaked auth delta")
	}
}

func TestBroadcastOwnerConnectSnapshot(t *testing.T) {
	srv := NewServer(ServerConfig{
		Transport:       golemnet.TransportWebSocket,
		StateUpdateLane: StateUpdateLaneStream,
	})
	ent := &ownerScopedTickEntity{
		interestTickEntity: interestTickEntity{id: 1, full: []byte("auth-full")},
		publicFull:         []byte("public-full"),
		ownerMask:          0b100,
	}
	if err := srv.CreateEntity(ent); err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}

	httpSrv := httptest.NewServer(srv.Handler())
	defer httpSrv.Close()

	client := mustDialGameClient(t, "ws"+httpSrv.URL[4:])
	defer client.CloseNow()
	ids := waitForSessionIDs(t, srv, 1)
	payloads := mustReadWrappedMessages(t, client, 1)[0]
	if !containsPayload(payloads, []byte("public-full")) || containsPayload(payloads, []byte("auth-full")) {
		t.Fatalf("unowned connect snapshot want public only, got %v", payloads)
	}

	if !srv.SetOwner(ent.id, ids[0]) {
		t.Fatal("SetOwner failed")
	}
	if err := srv.runBroadcastTick(); err != nil {
		t.Fatalf("ownership refresh tick: %v", err)
	}
	refresh := mustReadWrappedMessages(t, client, 1)[0]
	if !containsPayload(refresh, []byte("auth-full")) {
		t.Fatalf("new owner missing auth refresh: %v", refresh)
	}
}

func TestOwnershipTransferClearAndCoalesce(t *testing.T) {
	srv := NewServer(ServerConfig{
		Transport:       golemnet.TransportWebSocket,
		StateUpdateLane: StateUpdateLaneStream,
	})
	srv.SetRemovalSerializer(func(entityID int64, _ uint64) ([]byte, error) {
		return []byte(fmt.Sprintf("removed:%d", entityID)), nil
	})

	httpSrv := httptest.NewServer(srv.Handler())
	defer httpSrv.Close()
	clientA := mustDialGameClient(t, "ws"+httpSrv.URL[4:])
	defer clientA.CloseNow()
	clientB := mustDialGameClient(t, "ws"+httpSrv.URL[4:])
	defer clientB.CloseNow()
	ids := waitForSessionIDs(t, srv, 2)

	ent := &ownerScopedTickEntity{
		interestTickEntity: interestTickEntity{id: 5, full: []byte("auth-full")},
		publicFull:         []byte("public-full"),
		ownerMask:          0b100,
	}
	ownerSID, otherSID := ids[0], ids[1]
	if err := srv.CreateEntity(ent, ownerSID); err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}
	if err := srv.runBroadcastTick(); err != nil {
		t.Fatalf("seed tick: %v", err)
	}
	batchA := mustReadWrappedMessages(t, clientA, 1)[0]
	batchB := mustReadWrappedMessages(t, clientB, 1)[0]
	ownerConn, otherConn := clientA, clientB
	switch {
	case containsPayload(batchA, []byte("auth-full")):
		ownerConn, otherConn = clientA, clientB
	case containsPayload(batchB, []byte("auth-full")):
		ownerConn, otherConn = clientB, clientA
	default:
		t.Fatalf("seed missing auth full: A=%v B=%v", batchA, batchB)
	}

	// A→B→A same tick cancels the pending refresh.
	if !srv.SetOwner(ent.id, otherSID) {
		t.Fatal("SetOwner B failed")
	}
	if !srv.SetOwner(ent.id, ownerSID) {
		t.Fatal("SetOwner back to A failed")
	}
	srv.interestMu.Lock()
	pending := len(srv.ownershipRefresh)
	srv.interestMu.Unlock()
	if pending != 0 {
		t.Fatalf("A→B→A left pending ownership refresh: %d", pending)
	}
	if err := srv.runBroadcastTick(); err != nil {
		t.Fatalf("coalesce tick: %v", err)
	}

	// Transfer A→B: A gets public clear, B gets auth. Authority is immediate.
	if !srv.SetOwner(ent.id, otherSID) {
		t.Fatal("transfer SetOwner failed")
	}
	if got, _ := srv.Owner(ent.id); got != otherSID {
		t.Fatalf("command authority want %d got %d", otherSID, got)
	}
	if err := srv.runBroadcastTick(); err != nil {
		t.Fatalf("transfer tick: %v", err)
	}
	oldBatch := mustReadWrappedMessages(t, ownerConn, 1)[0]
	newBatch := mustReadWrappedMessages(t, otherConn, 1)[0]
	if !containsPayload(oldBatch, []byte("public-full")) {
		t.Fatalf("old owner missing public clear: %v", oldBatch)
	}
	if containsPayload(oldBatch, []byte("auth-full")) {
		t.Fatalf("old owner must not receive auth on clear")
	}
	if !containsPayload(newBatch, []byte("auth-full")) {
		t.Fatalf("new owner missing auth refresh: %v", newBatch)
	}

	// Become unowned: clear current owner.
	if !srv.SetOwner(ent.id, 0) {
		t.Fatal("clear ownership failed")
	}
	if _, owned := srv.Owner(ent.id); owned {
		t.Fatal("Owner still reports owned after clear")
	}
	if err := srv.runBroadcastTick(); err != nil {
		t.Fatalf("unowned tick: %v", err)
	}
	clearBatch := mustReadWrappedMessages(t, otherConn, 1)[0]
	if !containsPayload(clearBatch, []byte("public-full")) {
		t.Fatalf("cleared owner missing public reset: %v", clearBatch)
	}
}

func TestOwnerVarsRespectVisibilityGroups(t *testing.T) {
	srv := NewServer(ServerConfig{
		Transport:       golemnet.TransportWebSocket,
		StateUpdateLane: StateUpdateLaneStream,
	})
	srv.SetRemovalSerializer(func(entityID int64, _ uint64) ([]byte, error) {
		return []byte(fmt.Sprintf("removed:%d", entityID)), nil
	})

	httpSrv := httptest.NewServer(srv.Handler())
	defer httpSrv.Close()
	clientA := mustDialGameClient(t, "ws"+httpSrv.URL[4:])
	defer clientA.CloseNow()
	clientB := mustDialGameClient(t, "ws"+httpSrv.URL[4:])
	defer clientB.CloseNow()
	ids := waitForSessionIDs(t, srv, 2)
	ownerSID, outsiderSID := ids[0], ids[1]

	ent := &ownerScopedTickEntity{
		interestTickEntity: interestTickEntity{id: 7, full: []byte("auth-full")},
		publicFull:         []byte("public-full"),
		ownerMask:          0b100,
	}
	if err := srv.CreateEntity(ent, ownerSID); err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}
	srv.SetEntityVisibilityGroup(ent.id, "party")
	srv.JoinVisibilityGroup(ownerSID, "party")

	if err := srv.runBroadcastTick(); err != nil {
		t.Fatalf("tick: %v", err)
	}

	// Visibility groups still gate known-set membership; owner-only redaction
	// applies only among allowed recipients.
	if knowing := sortedInt64(srv.SessionsKnowing(ent.id)); !equalInt64(knowing, []int64{ownerSID}) {
		t.Fatalf("SessionsKnowing = %v, want [%d]", knowing, ownerSID)
	}
	if containsInt64(srv.SessionsKnowing(ent.id), outsiderSID) {
		t.Fatalf("outsider in SessionsKnowing")
	}
	ownerPayload, err := srv.fullUpdateForSession(ent, ownerSID)
	if err != nil {
		t.Fatalf("fullUpdateForSession owner: %v", err)
	}
	if !bytes.Equal(ownerPayload, []byte("auth-full")) {
		t.Fatalf("allowed owner payload = %q, want auth-full", ownerPayload)
	}
	outsiderPayload, err := srv.fullUpdateForSession(ent, outsiderSID)
	if err != nil {
		t.Fatalf("fullUpdateForSession outsider: %v", err)
	}
	if !bytes.Equal(outsiderPayload, []byte("public-full")) {
		t.Fatalf("disallowed recipient payload selector = %q, want public-full", outsiderPayload)
	}
	_ = clientA
	_ = clientB
}

func TestInterestOwnerSpawnDelta(t *testing.T) {
	srv := NewServer(ServerConfig{
		Transport:       golemnet.TransportWebSocket,
		StateUpdateLane: StateUpdateLaneStream,
		CellSize:        4,
	})
	srv.SetRemovalSerializer(func(entityID int64, _ uint64) ([]byte, error) {
		return []byte(fmt.Sprintf("removed:%d", entityID)), nil
	})

	anchor := &interestTickEntity{id: 1, x: 0, y: 0, full: []byte("anchor-full")}
	ent := &ownerScopedTickEntity{
		interestTickEntity: interestTickEntity{
			id: 2, x: 1, y: 0,
			full:      []byte("auth-full"),
			flush:     []byte("auth-delta"),
			flushMask: 0b111,
		},
		publicFull:  []byte("public-full"),
		publicFlush: []byte("public-delta"),
		ownerMask:   0b100,
	}
	if err := srv.CreateEntity(anchor); err != nil {
		t.Fatalf("CreateEntity anchor: %v", err)
	}

	httpSrv := httptest.NewServer(srv.Handler())
	defer httpSrv.Close()
	clientA := mustDialGameClient(t, "ws"+httpSrv.URL[4:])
	defer clientA.CloseNow()
	clientB := mustDialGameClient(t, "ws"+httpSrv.URL[4:])
	defer clientB.CloseNow()
	ids := waitForSessionIDs(t, srv, 2)
	for _, id := range ids {
		srv.AssignFOI(id, anchor.id, 10, 1)
	}
	ownerSID := ids[0]
	if err := srv.CreateEntity(ent, ownerSID); err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}
	if err := srv.runInterestTick(); err != nil {
		t.Fatalf("enter tick: %v", err)
	}
	batchA := mustReadWrappedMessages(t, clientA, 1)[0]
	batchB := mustReadWrappedMessages(t, clientB, 1)[0]

	ownerConn, otherConn := clientA, clientB
	if containsPayload(batchB, []byte("auth-full")) {
		ownerConn, otherConn = clientB, clientA
		batchA, batchB = batchB, batchA
	}
	if !containsPayload(batchA, []byte("auth-full")) {
		t.Fatalf("owner missing auth full: %v", batchA)
	}
	if !containsPayload(batchB, []byte("public-full")) {
		t.Fatalf("non-owner missing public full: %v", batchB)
	}
	if containsPayload(batchB, []byte("auth-full")) {
		t.Fatalf("non-owner leaked auth full")
	}

	ent.flush = []byte("auth-delta-2")
	ent.publicFlush = []byte("public-delta-2")
	ent.flushMask = 0b111
	if err := srv.runInterestTick(); err != nil {
		t.Fatalf("delta tick: %v", err)
	}
	ownerDelta := mustReadWrappedMessages(t, ownerConn, 1)[0]
	otherDelta := mustReadWrappedMessages(t, otherConn, 1)[0]
	if !containsPayload(ownerDelta, []byte("auth-delta-2")) {
		t.Fatalf("owner missing delta: %v", ownerDelta)
	}
	if !containsPayload(otherDelta, []byte("public-delta-2")) {
		t.Fatalf("non-owner missing public delta: %v", otherDelta)
	}
}

func TestOwnerZeroPublicMaskSendsNothing(t *testing.T) {
	srv := NewServer(ServerConfig{
		Transport:       golemnet.TransportWebSocket,
		StateUpdateLane: StateUpdateLaneStream,
	})
	httpSrv := httptest.NewServer(srv.Handler())
	defer httpSrv.Close()
	clientA := mustDialGameClient(t, "ws"+httpSrv.URL[4:])
	defer clientA.CloseNow()
	clientB := mustDialGameClient(t, "ws"+httpSrv.URL[4:])
	defer clientB.CloseNow()
	ids := waitForSessionIDs(t, srv, 2)

	ent := &ownerScopedTickEntity{
		interestTickEntity: interestTickEntity{
			id: 3, full: []byte("auth-full"), flush: []byte("auth-delta"), flushMask: 0b100,
		},
		publicFull:  []byte("public-full"),
		publicFlush: []byte("public-delta"),
		ownerMask:   0b100,
	}
	if err := srv.CreateEntity(ent, ids[0]); err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}
	if err := srv.runBroadcastTick(); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	_ = mustReadWrappedMessages(t, clientA, 1)
	_ = mustReadWrappedMessages(t, clientB, 1)

	ownerConn := clientA
	if !srv.sessionOwnsEntity(ids[0], ent.id) {
		ownerConn = clientB
	}
	ent.flush = []byte("auth-only-delta")
	ent.flushMask = 0b100
	pubMask := ent.PublicReplicationMask(ent.flushMask)
	if pubMask != 0 {
		t.Fatalf("PublicReplicationMask = %b, want 0", pubMask)
	}
	if err := srv.runBroadcastTick(); err != nil {
		t.Fatalf("delta: %v", err)
	}
	ownerBatch := mustReadWrappedMessages(t, ownerConn, 1)[0]
	if !containsPayload(ownerBatch, []byte("auth-only-delta")) {
		t.Fatalf("owner missing auth-only delta: %v", ownerBatch)
	}
	// Non-owner stays known but receives no frame when the public mask is empty.
	if knowing := srv.SessionsKnowing(ent.id); len(knowing) != 2 {
		t.Fatalf("both sessions should still know entity, got %v", knowing)
	}
}

func TestOwnerScopedPersistenceUsesAuthFull(t *testing.T) {
	srv := NewServer(ServerConfig{})
	ent := &ownerScopedTickEntity{
		interestTickEntity: interestTickEntity{id: 9, full: []byte("auth-full")},
		publicFull:         []byte("public-full"),
		ownerMask:          0b100,
	}
	if err := srv.CreateEntity(ent); err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}
	snaps, err := srv.SnapshotAll()
	if err != nil {
		t.Fatalf("SnapshotAll: %v", err)
	}
	if len(snaps) != 1 || !bytes.Equal(snaps[0], []byte("auth-full")) {
		t.Fatalf("persistence must use authoritative FullUpdate, got %v", snaps)
	}
}

func TestOwnerCompactEventualDatagramAndFallback(t *testing.T) {
	srv := NewServer(ServerConfig{
		Transport:       golemnet.TransportWebTransport,
		StateUpdateLane: StateUpdateLaneDatagram,
	})
	ent := &ownerScopedTickEntity{
		interestTickEntity: interestTickEntity{
			id:        11,
			full:      []byte("auth-full"),
			flush:     []byte("auth-body"),
			compact:   []byte("auth-compact"),
			flushMask: 0b111,
			revision:  1,
		},
		publicFull:    []byte("public-full"),
		publicCompact: []byte("public-compact"),
		publicFlush:   []byte("public-stream-delta"),
		ownerMask:     0b100,
	}
	if err := srv.CreateEntity(ent); err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}
	if !srv.SetOwner(ent.id, 1) {
		t.Fatal("SetOwner")
	}

	ch := eventualStateChange{id: ent.id, mask: 0b111}
	ownerCh, ok := srv.eventualChangeForSession(1, ch)
	if !ok || ownerCh.public || ownerCh.mask != 0b111 {
		t.Fatalf("owner change = %+v ok=%v", ownerCh, ok)
	}
	pubCh, ok := srv.eventualChangeForSession(2, ch)
	if !ok || !pubCh.public || pubCh.mask != 0b011 {
		t.Fatalf("public change = %+v ok=%v", pubCh, ok)
	}

	cache := newEventualStateTickCache()
	ownerFrame, _, err := cache.frame(srv, ownerCh)
	if err != nil {
		t.Fatalf("owner frame: %v", err)
	}
	pubFrame, _, err := cache.frame(srv, pubCh)
	if err != nil {
		t.Fatalf("public frame: %v", err)
	}
	if !ownerFrame.fitsDatagram || !pubFrame.fitsDatagram {
		t.Fatalf("expected both compact datagrams; owner=%v public=%v", ownerFrame.fitsDatagram, pubFrame.fitsDatagram)
	}
	if bytes.Equal(ownerFrame.datagram, pubFrame.datagram) {
		t.Fatal("cache must not reuse private compact bytes for public recipients")
	}

	ent.publicCompact = bytes.Repeat([]byte("x"), 4000)
	pubFrame2, _, err := srv.eventualFrameForChange(pubCh)
	if err != nil {
		t.Fatalf("oversized public frame: %v", err)
	}
	if pubFrame2.fitsDatagram {
		t.Fatal("expected stream fallback for oversized public compact")
	}
	if bytes.Contains(pubFrame2.stream, []byte("auth-body")) || bytes.Contains(pubFrame2.stream, []byte("auth-compact")) {
		t.Fatalf("oversized fallback leaked auth payload: %q", pubFrame2.stream)
	}
	if !bytes.Contains(pubFrame2.stream, []byte("public-stream-delta")) {
		t.Fatalf("oversized fallback missing public stream delta: %q", pubFrame2.stream)
	}
}

func TestFormerOwnerLostPrivateMaskCannotLeakAfterTransfer(t *testing.T) {
	srv := NewServer(ServerConfig{
		Transport:       golemnet.TransportWebTransport,
		StateUpdateLane: StateUpdateLaneDatagram,
	})
	ent := &ownerScopedTickEntity{
		interestTickEntity: interestTickEntity{
			id:        21,
			full:      []byte("auth-full"),
			flush:     []byte("auth-body"),
			compact:   []byte("auth-compact-private"),
			flushMask: 0b111,
			revision:  1,
		},
		publicFull:    []byte("public-full"),
		publicCompact: []byte("public-compact-ok"),
		publicFlush:   []byte("public-stream-delta"),
		ownerMask:     0b100,
	}
	if err := srv.CreateEntity(ent, 1); err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}

	former := int64(1)
	tracker := srv.eventualTracker(former)
	// Former owner had a private mask in flight that is lost and requeued.
	privateCh := eventualStateChange{id: ent.id, mask: 0b111}
	tracker.markSent(10, []eventualStateChange{privateCh})
	tracker.applyFeedback([]golemnet.EventualStateDelivery{{Token: 10, Delivered: false}}, func(int64) bool { return true })

	// Ownership moves away; public clear must drop tracker state.
	if !srv.SetOwner(ent.id, 2) {
		t.Fatal("SetOwner")
	}
	frames, err := srv.appendOwnershipRefreshFrames(
		former,
		map[int64]ownershipRefresh{ent.id: {clearOwner: former, newOwner: 2}},
		[]int64{ent.id},
		map[int64][]byte{},
		map[int64][]byte{},
		nil,
	)
	if err != nil {
		t.Fatalf("appendOwnershipRefreshFrames: %v", err)
	}
	if len(frames) != 1 || !bytes.Contains(frames[0], []byte("public-full")) {
		t.Fatalf("expected public clear frame, got %v", frames)
	}
	if tracker.hasDirty() {
		t.Fatalf("ownership clear must clear eventual tracker dirty, got %v", tracker.dirtyIDs())
	}

	// Simulate a buggy merge that reintroduces the private bit with public:true
	// (loss/requeue + later public delta). Serialize must still redact.
	stalePublic := eventualStateChange{id: ent.id, mask: 0b111, public: true}
	tracker.markDirtyChange(stalePublic)
	tracker.markDirtyChange(eventualStateChange{id: ent.id, mask: 0b001, public: true})

	frame, live, err := srv.eventualFrameForChange(stalePublic)
	if err != nil || !live {
		t.Fatalf("eventualFrameForChange: live=%v err=%v", live, err)
	}
	if !frame.fitsDatagram {
		t.Fatal("expected compact datagram for small public payload")
	}
	if bytes.Contains(frame.datagram, []byte("auth-compact-private")) {
		t.Fatalf("compact leaked private bytes: %q", frame.datagram)
	}
	if !bytes.Contains(frame.datagram, []byte("public-compact-ok")) {
		t.Fatalf("compact missing public body: %q", frame.datagram)
	}

	// Stream-fallback variant: oversized public compact must use public stream delta.
	ent.publicCompact = bytes.Repeat([]byte("y"), 4000)
	fallback, live, err := srv.eventualFrameForChange(stalePublic)
	if err != nil || !live {
		t.Fatalf("fallback frame: live=%v err=%v", live, err)
	}
	if fallback.fitsDatagram {
		t.Fatal("expected stream fallback")
	}
	if bytes.Contains(fallback.stream, []byte("auth-body")) || bytes.Contains(fallback.stream, []byte("auth-compact-private")) {
		t.Fatalf("stream fallback leaked private bytes: %q", fallback.stream)
	}
	if !bytes.Contains(fallback.stream, []byte("public-stream-delta")) {
		t.Fatalf("stream fallback missing public delta: %q", fallback.stream)
	}

	// Zero public mask after reapply produces no payload.
	onlyPrivate := eventualStateChange{id: ent.id, mask: 0b100, public: true}
	empty, live, err := srv.eventualFrameForChange(onlyPrivate)
	if err != nil || !live {
		t.Fatalf("zero-mask frame: live=%v err=%v", live, err)
	}
	if empty.datagram != nil || empty.stream != nil {
		t.Fatalf("zero public mask must yield empty payload, got %+v", empty)
	}
}

func TestSetOwnerConcurrentCoalesceAndAuthority(t *testing.T) {
	srv := NewServer(ServerConfig{})
	ent := &ownerScopedTickEntity{
		interestTickEntity: interestTickEntity{id: 31, full: []byte("auth-full")},
		publicFull:         []byte("public-full"),
		ownerMask:          0b100,
	}
	if err := srv.CreateEntity(ent, 1); err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}

	var wg sync.WaitGroup
	// Concurrent transfers A→B and A→C; final owner must be one of them and
	// refresh clearOwner must remain the original pre-change owner (1).
	for i := 0; i < 32; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			srv.SetOwner(ent.id, 2)
		}()
		go func() {
			defer wg.Done()
			srv.SetOwner(ent.id, 3)
		}()
	}
	wg.Wait()

	final, owned := srv.Owner(ent.id)
	if !owned || (final != 2 && final != 3) {
		t.Fatalf("Owner = (%d,%v), want 2 or 3", final, owned)
	}
	srv.interestMu.Lock()
	ref, ok := srv.ownershipRefresh[ent.id]
	srv.interestMu.Unlock()
	if !ok {
		t.Fatal("expected pending ownership refresh")
	}
	if ref.clearOwner != 1 {
		t.Fatalf("clearOwner = %d, want original owner 1", ref.clearOwner)
	}
	if ref.newOwner != final {
		t.Fatalf("newOwner = %d, want final owner %d", ref.newOwner, final)
	}

	// Same-tick A→B→A under concurrency still cancels.
	if !srv.SetOwner(ent.id, 1) {
		t.Fatal("reset owner to 1")
	}
	srv.interestMu.Lock()
	srv.ownershipRefresh = make(map[int64]ownershipRefresh)
	srv.interestMu.Unlock()

	wg.Add(2)
	go func() {
		defer wg.Done()
		srv.SetOwner(ent.id, 2)
		srv.SetOwner(ent.id, 1)
	}()
	go func() {
		defer wg.Done()
		srv.SetOwner(ent.id, 3)
		srv.SetOwner(ent.id, 1)
	}()
	wg.Wait()
	if got, owned := srv.Owner(ent.id); !owned || got != 1 {
		t.Fatalf("Owner after A→…→A = (%d,%v), want (1,true)", got, owned)
	}
	srv.interestMu.Lock()
	pending := len(srv.ownershipRefresh)
	srv.interestMu.Unlock()
	if pending != 0 {
		t.Fatalf("A→…→A must cancel refresh, pending=%d", pending)
	}
}

func TestSetOwnerConcurrentRace(t *testing.T) {
	srv := NewServer(ServerConfig{})
	ent := &ownerScopedTickEntity{
		interestTickEntity: interestTickEntity{id: 32, full: []byte("auth-full")},
		publicFull:         []byte("public-full"),
		ownerMask:          0b100,
	}
	if err := srv.CreateEntity(ent, 1); err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			target := int64(2 + n%3)
			if n%7 == 0 {
				target = 0
			}
			srv.SetOwner(ent.id, target)
			_, _ = srv.Owner(ent.id)
		}(i)
	}
	wg.Wait()
	// Must remain consistent: either unowned or a recorded owner.
	sid, owned := srv.Owner(ent.id)
	if owned && sid == 0 {
		t.Fatal("owned with session 0 is inconsistent")
	}
	srv.interestMu.Lock()
	ref, ok := srv.ownershipRefresh[ent.id]
	srv.interestMu.Unlock()
	if ok {
		if ref.clearOwner == ref.newOwner {
			t.Fatalf("pending refresh must not be a no-op: %+v", ref)
		}
		if owned && ref.newOwner != sid {
			t.Fatalf("pending newOwner %d != Owner %d", ref.newOwner, sid)
		}
		if !owned && ref.newOwner != 0 {
			t.Fatalf("unowned but pending newOwner=%d", ref.newOwner)
		}
	}
}
