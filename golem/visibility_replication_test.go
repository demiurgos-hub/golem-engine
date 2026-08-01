package golem

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	golemnet "github.com/demiurgos-hub/golem-engine/golem/net"
)

func sortedInt64(ids []int64) []int64 {
	out := append([]int64(nil), ids...)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func containsPayload(payloads [][]byte, want []byte) bool {
	for _, p := range payloads {
		if bytes.Equal(p, want) {
			return true
		}
	}
	return false
}

func equalInt64(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func containsInt64(ids []int64, want int64) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

func tryReadWrappedMessages(t *testing.T, conn *websocket.Conn, timeout time.Duration) ([][]byte, bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	_, msg, err := conn.Read(ctx)
	if err != nil {
		return nil, false
	}
	return decodeWrappedEntityBatch(t, msg), true
}

func TestInterestVisibilityMemberNonMemberLeaveRejoin(t *testing.T) {
	srv := NewServer(ServerConfig{
		Transport:       golemnet.TransportWebSocket,
		StateUpdateLane: StateUpdateLaneStream,
		CellSize:        4,
	})
	srv.SetRemovalSerializer(func(entityID int64, _ uint64) ([]byte, error) {
		return []byte(fmt.Sprintf("removed:%d", entityID)), nil
	})

	anchor := &interestTickEntity{id: 1, x: 0, y: 0, full: []byte("anchor-full")}
	secret := &interestTickEntity{id: 2, x: 1, y: 0, full: []byte("secret-full"), flush: []byte("secret-delta")}
	for _, e := range []*interestTickEntity{anchor, secret} {
		if err := srv.CreateEntity(e); err != nil {
			t.Fatalf("CreateEntity(%d): %v", e.id, err)
		}
	}
	srv.SetEntityVisibilityGroup(secret.id, "party")

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
	memberSID, outsiderSID := ids[0], ids[1]
	srv.JoinVisibilityGroup(memberSID, "party")

	if err := srv.runInterestTick(); err != nil {
		t.Fatalf("tick1: %v", err)
	}
	batchA := mustReadWrappedMessages(t, clientA, 1)[0]
	batchB := mustReadWrappedMessages(t, clientB, 1)[0]

	var memberConn *websocket.Conn
	if containsPayload(batchA, []byte("secret-full")) {
		memberConn = clientA
		if containsPayload(batchB, []byte("secret-full")) {
			t.Fatal("non-member received secret full state")
		}
	} else if containsPayload(batchB, []byte("secret-full")) {
		memberConn = clientB
		if containsPayload(batchA, []byte("secret-full")) {
			t.Fatal("non-member received secret full state")
		}
	} else {
		t.Fatal("member did not receive secret full state")
	}
	if got := sortedInt64(srv.SessionsKnowing(secret.id)); !equalInt64(got, []int64{memberSID}) {
		t.Fatalf("SessionsKnowing after enter = %v, want [%d]", got, memberSID)
	}
	if knowing := srv.SessionsKnowing(secret.id); containsInt64(knowing, outsiderSID) {
		t.Fatalf("outsider must not be in SessionsKnowing: %v", knowing)
	}

	secret.flush = []byte("secret-delta-2")
	if err := srv.runInterestTick(); err != nil {
		t.Fatalf("tick2: %v", err)
	}
	deltaBatch := mustReadWrappedMessages(t, memberConn, 1)[0]
	if !containsPayload(deltaBatch, []byte("secret-delta-2")) {
		t.Fatalf("member missing delta: %v", deltaBatch)
	}

	srv.LeaveVisibilityGroup(memberSID, "party")
	if err := srv.runInterestTick(); err != nil {
		t.Fatalf("tick3 leave: %v", err)
	}
	leaveBatch := mustReadWrappedMessages(t, memberConn, 1)[0]
	if !containsPayload(leaveBatch, []byte("removed:2")) {
		t.Fatalf("leave must send EntityRemoved: %v", leaveBatch)
	}
	if knowing := srv.SessionsKnowing(secret.id); len(knowing) != 0 {
		t.Fatalf("SessionsKnowing after leave = %v, want empty", knowing)
	}

	srv.JoinVisibilityGroup(memberSID, "party")
	if err := srv.runInterestTick(); err != nil {
		t.Fatalf("tick4 rejoin: %v", err)
	}
	rejoinBatch := mustReadWrappedMessages(t, memberConn, 1)[0]
	if !containsPayload(rejoinBatch, []byte("secret-full")) {
		t.Fatalf("rejoin must send FullUpdate: %v", rejoinBatch)
	}
}

func TestBroadcastVisibilityTransitionMatrix(t *testing.T) {
	srv := NewServer(ServerConfig{
		Transport:       golemnet.TransportWebSocket,
		StateUpdateLane: StateUpdateLaneStream,
	})
	srv.SetRemovalSerializer(func(entityID int64, _ uint64) ([]byte, error) {
		return []byte(fmt.Sprintf("removed:%d", entityID)), nil
	})

	ent := &interestTickEntity{id: 1, x: 0, y: 0, full: []byte("ent-full"), flush: []byte("ent-delta")}
	if err := srv.CreateEntity(ent); err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}

	httpSrv := httptest.NewServer(srv.Handler())
	defer httpSrv.Close()

	c1 := mustDialGameClient(t, "ws"+httpSrv.URL[4:])
	defer c1.CloseNow()
	c2 := mustDialGameClient(t, "ws"+httpSrv.URL[4:])
	defer c2.CloseNow()
	ids := waitForSessionIDs(t, srv, 2)

	snap1 := mustReadWrappedMessages(t, c1, 1)[0]
	snap2 := mustReadWrappedMessages(t, c2, 1)[0]
	if !containsPayload(snap1, []byte("ent-full")) || !containsPayload(snap2, []byte("ent-full")) {
		t.Fatalf("public snapshot missing entity")
	}
	if got := sortedInt64(srv.SessionsKnowing(ent.id)); !equalInt64(got, sortedInt64(ids)) {
		t.Fatalf("SessionsKnowing after snapshot = %v, want %v", got, sortedInt64(ids))
	}

	s1, s2 := ids[0], ids[1]
	srv.SetEntityVisibilityGroup(ent.id, "a")
	srv.JoinVisibilityGroup(s1, "a")
	if err := srv.runBroadcastTick(); err != nil {
		t.Fatalf("public→grouped: %v", err)
	}

	b1, ok1 := tryReadWrappedMessages(t, c1, 200*time.Millisecond)
	b2, ok2 := tryReadWrappedMessages(t, c2, 200*time.Millisecond)
	var memberConn, outsiderConn *websocket.Conn
	switch {
	case ok1 && containsPayload(b1, []byte("removed:1")):
		outsiderConn, memberConn = c1, c2
	case ok2 && containsPayload(b2, []byte("removed:1")):
		outsiderConn, memberConn = c2, c1
	default:
		t.Fatalf("expected one EntityRemoved on public→grouped; ok=%v/%v batches=%v %v", ok1, ok2, b1, b2)
	}
	if got := sortedInt64(srv.SessionsKnowing(ent.id)); !equalInt64(got, []int64{s1}) {
		t.Fatalf("SessionsKnowing after public→grouped = %v, want [%d]", got, s1)
	}

	srv.SetEntityVisibilityGroup(ent.id, "b")
	srv.JoinVisibilityGroup(s2, "b")
	if err := srv.runBroadcastTick(); err != nil {
		t.Fatalf("group A→B: %v", err)
	}
	exitA := mustReadWrappedMessages(t, memberConn, 1)[0]
	enterB := mustReadWrappedMessages(t, outsiderConn, 1)[0]
	if !containsPayload(exitA, []byte("removed:1")) {
		t.Fatalf("old group member must exit: %v", exitA)
	}
	if !containsPayload(enterB, []byte("ent-full")) {
		t.Fatalf("new group member must get FullUpdate: %v", enterB)
	}
	if got := sortedInt64(srv.SessionsKnowing(ent.id)); !equalInt64(got, []int64{s2}) {
		t.Fatalf("SessionsKnowing after A→B = %v, want [%d]", got, s2)
	}

	srv.SetEntityVisibilityGroup(ent.id, "")
	if err := srv.runBroadcastTick(); err != nil {
		t.Fatalf("grouped→public: %v", err)
	}
	back := mustReadWrappedMessages(t, memberConn, 1)[0]
	if !containsPayload(back, []byte("ent-full")) {
		t.Fatalf("former non-member must get FullUpdate on grouped→public: %v", back)
	}
	if got := sortedInt64(srv.SessionsKnowing(ent.id)); !equalInt64(got, sortedInt64(ids)) {
		t.Fatalf("SessionsKnowing after grouped→public = %v", got)
	}

	srv.SetEntityVisibilityGroup(ent.id, "z")
	srv.JoinVisibilityGroup(s1, "z")
	if err := srv.runBroadcastTick(); err != nil {
		t.Fatalf("regroup: %v", err)
	}
	_, _ = tryReadWrappedMessages(t, c1, 100*time.Millisecond)
	_, _ = tryReadWrappedMessages(t, c2, 100*time.Millisecond)

	srv.DeleteEntity(ent.id)
	if err := srv.runBroadcastTick(); err != nil {
		t.Fatalf("delete tick: %v", err)
	}
	gotRemoval := false
	for _, c := range []*websocket.Conn{c1, c2} {
		if batch, ok := tryReadWrappedMessages(t, c, 200*time.Millisecond); ok && containsPayload(batch, []byte("removed:1")) {
			gotRemoval = true
			break
		}
	}
	if !gotRemoval {
		t.Fatal("delete must notify a known recipient with EntityRemoved")
	}
	srv.interestMu.Lock()
	groupLeft := srv.visibility.EntityGroup(ent.id)
	srv.interestMu.Unlock()
	if groupLeft != "" {
		t.Fatalf("group metadata should be cleared after removal replication, got %q", groupLeft)
	}
}

func TestBroadcastSnapshotExcludesGroupedAndCommitsKnownAfterSuccess(t *testing.T) {
	srv := NewServer(ServerConfig{
		Transport:       golemnet.TransportWebSocket,
		StateUpdateLane: StateUpdateLaneStream,
	})
	pub := &interestTickEntity{id: 1, full: []byte("pub-full")}
	secret := &interestTickEntity{id: 2, full: []byte("secret-full")}
	for _, e := range []*interestTickEntity{pub, secret} {
		if err := srv.CreateEntity(e); err != nil {
			t.Fatalf("CreateEntity: %v", err)
		}
	}
	srv.SetEntityVisibilityGroup(secret.id, "party")

	if knowing := srv.SessionsKnowing(pub.id); len(knowing) != 0 {
		t.Fatalf("SessionsKnowing before connect must be empty, got %v", knowing)
	}

	httpSrv := httptest.NewServer(srv.Handler())
	defer httpSrv.Close()
	client := mustDialGameClient(t, "ws"+httpSrv.URL[4:])
	defer client.CloseNow()
	ids := waitForSessionIDs(t, srv, 1)

	payloads := mustReadWrappedMessages(t, client, 1)[0]
	if !containsPayload(payloads, []byte("pub-full")) {
		t.Fatalf("snapshot missing public entity: %v", payloads)
	}
	if containsPayload(payloads, []byte("secret-full")) {
		t.Fatalf("snapshot leaked grouped entity: %v", payloads)
	}
	if got := sortedInt64(srv.SessionsKnowing(pub.id)); !equalInt64(got, ids) {
		t.Fatalf("SessionsKnowing(pub) after snapshot = %v, want %v", got, ids)
	}
	if knowing := srv.SessionsKnowing(secret.id); len(knowing) != 0 {
		t.Fatalf("SessionsKnowing(secret) after snapshot = %v, want empty", knowing)
	}
}

func TestBroadcastSnapshotFailureDoesNotCommitKnown(t *testing.T) {
	srv := NewServer(ServerConfig{
		Transport:       golemnet.TransportWebSocket,
		StateUpdateLane: StateUpdateLaneStream,
	})
	okEnt := &interestTickEntity{id: 1, full: []byte("ok")}
	if err := srv.CreateEntity(okEnt); err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}
	origProvider := srv.entitySnapshotForSession
	srv.listener.SetEntitySnapshotFunc(func(sessionID int64) ([]golemnet.EntitySnapshot, error) {
		snaps, err := origProvider(sessionID)
		if err != nil {
			return nil, err
		}
		return append(snaps, golemnet.EntitySnapshot{
			EntityID: 2,
			Data:     bytes.Repeat([]byte("x"), 40000),
		}), nil
	})

	httpSrv := httptest.NewServer(srv.Handler())
	defer httpSrv.Close()
	client := mustDialGameClient(t, "ws"+httpSrv.URL[4:])
	defer client.CloseNow()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if len(srv.listener.SessionIDs()) == 0 {
			if knowing := srv.SessionsKnowing(1); len(knowing) != 0 {
				t.Fatalf("failed snapshot must not commit known state: %v", knowing)
			}
			// Visibility APIs must not stall after a failed snapshot.
			done := make(chan struct{})
			go func() {
				srv.SetEntityVisibilityGroup(1, "g")
				close(done)
			}()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("SetEntityVisibilityGroup stalled after failed snapshot")
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	if knowing := srv.SessionsKnowing(1); len(knowing) != 0 {
		t.Fatalf("failed snapshot must not commit known state: %v", knowing)
	}
}

func TestBroadcastSnapshotMutationReconciledNextPass(t *testing.T) {
	// Point-in-time semantics: a public→grouped mutation during FullUpdate does
	// not revoke the already-selected snapshot frame, does not stall visibility
	// APIs, and is reconciled on the next replication pass via EntityRemoved.
	srv := NewServer(ServerConfig{
		Transport:       golemnet.TransportWebSocket,
		StateUpdateLane: StateUpdateLaneStream,
	})
	srv.SetRemovalSerializer(func(entityID int64, _ uint64) ([]byte, error) {
		return []byte(fmt.Sprintf("removed:%d", entityID)), nil
	})
	ent := &interestTickEntity{id: 1, full: []byte("pub-full")}
	if err := srv.CreateEntity(ent); err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}

	var setDone atomic.Bool
	ent.onFull = func() {
		done := make(chan struct{})
		go func() {
			srv.SetEntityVisibilityGroup(ent.id, "party")
			setDone.Store(true)
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("SetEntityVisibilityGroup stalled during snapshot FullUpdate")
		}
	}

	httpSrv := httptest.NewServer(srv.Handler())
	defer httpSrv.Close()
	client := mustDialGameClient(t, "ws"+httpSrv.URL[4:])
	defer client.CloseNow()
	ids := waitForSessionIDs(t, srv, 1)

	payloads := mustReadWrappedMessages(t, client, 1)[0]
	if !containsPayload(payloads, []byte("pub-full")) {
		t.Fatalf("point-in-time snapshot must still deliver selected public frame: %v", payloads)
	}
	if !setDone.Load() {
		t.Fatal("expected visibility mutation during snapshot selection to complete")
	}
	if got := sortedInt64(srv.SessionsKnowing(ent.id)); !equalInt64(got, ids) {
		t.Fatalf("successful snapshot IDs must become known: %v", got)
	}

	// Next pass applies the grouped policy: non-member loses the entity.
	if err := srv.runBroadcastTick(); err != nil {
		t.Fatalf("runBroadcastTick: %v", err)
	}
	removed := mustReadWrappedMessages(t, client, 1)[0]
	if !containsPayload(removed, []byte("removed:1")) {
		t.Fatalf("next pass must EntityRemoved after public→grouped: %v", removed)
	}
	if knowing := srv.SessionsKnowing(ent.id); len(knowing) != 0 {
		t.Fatalf("SessionsKnowing after next-pass removal = %v, want empty", knowing)
	}
}

func TestBroadcastBlindDecisionMutationReconciledNextPass(t *testing.T) {
	// Blind fast-path decision is point-in-time; a later group assignment is
	// reconciled on the next filtered pass without stalling visibility APIs.
	srv := NewServer(ServerConfig{
		Transport:       golemnet.TransportWebSocket,
		StateUpdateLane: StateUpdateLaneStream,
	})
	srv.SetRemovalSerializer(func(entityID int64, _ uint64) ([]byte, error) {
		return []byte(fmt.Sprintf("removed:%d", entityID)), nil
	})
	ent := &interestTickEntity{id: 1, full: []byte("ent-full"), flush: nil}
	if err := srv.CreateEntity(ent); err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}

	httpSrv := httptest.NewServer(srv.Handler())
	defer httpSrv.Close()
	c1 := mustDialGameClient(t, "ws"+httpSrv.URL[4:])
	defer c1.CloseNow()
	c2 := mustDialGameClient(t, "ws"+httpSrv.URL[4:])
	defer c2.CloseNow()
	ids := waitForSessionIDs(t, srv, 2)
	_ = mustReadWrappedMessages(t, c1, 1)
	_ = mustReadWrappedMessages(t, c2, 1)

	// Flush the create-time spawn so a quiet tick can take the blind path
	// without leaving unread spawn frames in client buffers.
	if err := srv.runBroadcastTick(); err != nil {
		t.Fatalf("spawn flush tick: %v", err)
	}
	_, _ = tryReadWrappedMessages(t, c1, 100*time.Millisecond)
	_, _ = tryReadWrappedMessages(t, c2, 100*time.Millisecond)
	if got := sortedInt64(srv.SessionsKnowing(ent.id)); !equalInt64(got, sortedInt64(ids)) {
		t.Fatalf("SessionsKnowing after blind flush = %v", got)
	}

	// Quiet tick: still blind-eligible (no groups, known matches live).
	if err := srv.runBroadcastTick(); err != nil {
		t.Fatalf("quiet blind tick: %v", err)
	}

	done := make(chan struct{})
	go func() {
		srv.SetEntityVisibilityGroup(ent.id, "party")
		srv.JoinVisibilityGroup(ids[0], "party")
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("visibility APIs stalled after blind decision")
	}

	if err := srv.runBroadcastTick(); err != nil {
		t.Fatalf("reconcile tick: %v", err)
	}
	b1, ok1 := tryReadWrappedMessages(t, c1, 200*time.Millisecond)
	b2, ok2 := tryReadWrappedMessages(t, c2, 200*time.Millisecond)
	removedCount := 0
	if ok1 && containsPayload(b1, []byte("removed:1")) {
		removedCount++
	}
	if ok2 && containsPayload(b2, []byte("removed:1")) {
		removedCount++
	}
	if removedCount != 1 {
		t.Fatalf("exactly one non-member should get EntityRemoved next pass; batches=%v %v", b1, b2)
	}
	if got := sortedInt64(srv.SessionsKnowing(ent.id)); !equalInt64(got, []int64{ids[0]}) {
		t.Fatalf("SessionsKnowing after reconcile = %v, want [%d]", got, ids[0])
	}
}

func TestBroadcastDisconnectDuringSendClearsKnown(t *testing.T) {
	srv := NewServer(ServerConfig{
		Transport:       golemnet.TransportWebSocket,
		StateUpdateLane: StateUpdateLaneStream,
	})
	srv.SetRemovalSerializer(func(entityID int64, _ uint64) ([]byte, error) {
		return []byte(fmt.Sprintf("removed:%d", entityID)), nil
	})
	ent := &interestTickEntity{id: 1, full: []byte("full"), flush: []byte("delta")}
	if err := srv.CreateEntity(ent); err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}

	httpSrv := httptest.NewServer(srv.Handler())
	defer httpSrv.Close()
	client := mustDialGameClient(t, "ws"+httpSrv.URL[4:])
	ids := waitForSessionIDs(t, srv, 1)
	sid := ids[0]
	_ = mustReadWrappedMessages(t, client, 1)

	secret := &interestTickEntity{id: 2, full: []byte("secret-full")}
	if err := srv.CreateEntity(secret); err != nil {
		t.Fatalf("CreateEntity secret: %v", err)
	}
	srv.SetEntityVisibilityGroup(secret.id, "party")
	srv.JoinVisibilityGroup(sid, "party")

	// Session IDs are captured before onUpdates; remove the session here so
	// SendBatch returns ErrSessionNotFound after enter decisions apply.
	srv.OnUpdates(func([][]byte) {
		client.CloseNow()
		deadline := time.Now().Add(time.Second)
		for time.Now().Before(deadline) {
			err := srv.listener.SendBatch(sid, [][]byte{[]byte("probe")})
			if errors.Is(err, golemnet.ErrSessionNotFound) {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Error("session did not leave listener before send loop")
	})

	if err := srv.runBroadcastTick(); err != nil {
		t.Fatalf("runBroadcastTick: %v", err)
	}
	srv.interestMu.Lock()
	_, stillKnown := srv.broadcastKnown[sid]
	srv.interestMu.Unlock()
	if stillKnown {
		t.Fatal("broadcast known state must be cleared when SendBatch hits a disconnected session")
	}
}

func TestVisibilityServerAPIsRaceSafe(t *testing.T) {
	srv := NewServer(ServerConfig{
		Transport:       golemnet.TransportWebSocket,
		StateUpdateLane: StateUpdateLaneStream,
		CellSize:        4,
	})
	srv.SetRemovalSerializer(func(entityID int64, _ uint64) ([]byte, error) {
		return []byte(fmt.Sprintf("removed:%d", entityID)), nil
	})
	anchor := &interestTickEntity{id: 1, x: 0, y: 0, full: []byte("a")}
	ent := &interestTickEntity{id: 2, x: 1, y: 0, full: []byte("e"), flush: []byte("d")}
	_ = srv.CreateEntity(anchor)
	_ = srv.CreateEntity(ent)

	httpSrv := httptest.NewServer(srv.Handler())
	defer httpSrv.Close()
	client := mustDialGameClient(t, "ws"+httpSrv.URL[4:])
	defer client.CloseNow()
	ids := waitForSessionIDs(t, srv, 1)
	sid := ids[0]
	srv.AssignFOI(sid, anchor.id, 10, 1)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				srv.JoinVisibilityGroup(sid, "g")
				srv.SetEntityVisibilityGroup(ent.id, "g")
				_ = srv.SessionsKnowing(ent.id)
				srv.LeaveVisibilityGroup(sid, "g")
				srv.SetEntityVisibilityGroup(ent.id, "")
			}
		}()
	}
	for k := 0; k < 20; k++ {
		if err := srv.runInterestTick(); err != nil {
			t.Fatalf("runInterestTick: %v", err)
		}
	}
	wg.Wait()
}
