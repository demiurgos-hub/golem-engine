package golem

import (
	"context"
	"fmt"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	golemnet "github.com/demiurgos-hub/golem-engine/golem/net"
	"github.com/demiurgos-hub/golem-engine/golem/pb"
)

// spaceToken renders one ServerMessage field as a readable token: entity
// payloads as their raw test bytes, space messages as changed:<space>:<n>
// and settled:<n>, events as event:<payload>.
func spaceToken(t testing.TB, field int, data []byte) string {
	t.Helper()
	switch field {
	case serverMessageEntityField:
		return string(data)
	case serverMessageEventField:
		return "event:" + string(data)
	case serverMessageSpaceChangedField, serverMessageSpaceSettledField:
		var space, transition uint64
		r := pb.NewReader(data)
		for !r.Done() {
			f, wire := r.Tag()
			switch {
			case field == serverMessageSpaceChangedField && f == 1:
				space = uint64(r.Uint32())
			case field == serverMessageSpaceChangedField && f == 2, field == serverMessageSpaceSettledField && f == 1:
				transition = r.Uint64()
			default:
				r.Skip(wire)
			}
		}
		if field == serverMessageSpaceChangedField {
			return fmt.Sprintf("changed:%d:%d", space, transition)
		}
		return fmt.Sprintf("settled:%d", transition)
	}
	return fmt.Sprintf("field%d", field)
}

// decodeServerTokens decodes every ServerMessage field of one logical frame or
// WebSocket message in order.
func decodeServerTokens(t testing.TB, msg []byte) []string {
	t.Helper()
	var out []string
	r := pb.NewReader(msg)
	for !r.Done() {
		field, wire := r.Tag()
		if wire != 2 {
			t.Fatalf("unexpected ServerMessage wire type %d for field %d", wire, field)
		}
		out = append(out, spaceToken(t, field, r.Bytes()))
	}
	return out
}

// spaceClient reads a WebSocket in the background: a timed-out read would
// close the connection.
type spaceClient struct {
	msgs chan []byte
}

func newSpaceClient(t *testing.T, conn *websocket.Conn) *spaceClient {
	t.Helper()
	c := &spaceClient{msgs: make(chan []byte, 64)}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		conn.CloseNow()
	})
	go func() {
		defer close(c.msgs)
		for {
			_, msg, err := conn.Read(ctx)
			if err != nil {
				return
			}
			c.msgs <- msg
		}
	}()
	return c
}

// readSpaceTokens reads everything the client receives within a short quiet
// period after the first message.
func readSpaceTokens(t *testing.T, c *spaceClient) []string {
	t.Helper()
	var out []string
	timeout := 2 * time.Second
	for {
		select {
		case msg, ok := <-c.msgs:
			if !ok {
				t.Fatal("connection closed")
			}
			out = append(out, decodeServerTokens(t, msg)...)
			timeout = 150 * time.Millisecond
		case <-time.After(timeout):
			if out == nil {
				t.Fatal("no message received")
			}
			return out
		}
	}
}

func expectNoMessage(t *testing.T, c *spaceClient) {
	t.Helper()
	select {
	case msg := <-c.msgs:
		t.Fatalf("unexpected message %q", decodeServerTokens(t, msg))
	case <-time.After(150 * time.Millisecond):
	}
}

type spaceTestRig struct {
	srv          *Server
	run          func() error
	conns        []*spaceClient
	ids          []int64
	interestMode bool
}

func newSpaceTestRig(t *testing.T, interestMode bool, clients int) *spaceTestRig {
	t.Helper()
	cellSize := 0.0
	if interestMode {
		cellSize = 4
	}
	srv := NewServer(ServerConfig{Transport: golemnet.TransportWebSocket, StateUpdateLane: StateUpdateLaneStream, CellSize: cellSize})
	srv.SetRemovalSerializer(func(id int64, _ uint64) ([]byte, error) { return []byte(fmt.Sprintf("removed:%d", id)), nil })
	httpServer := httptest.NewServer(srv.Handler())
	t.Cleanup(httpServer.Close)
	rig := &spaceTestRig{srv: srv, run: srv.runBroadcastTick, interestMode: interestMode}
	if interestMode {
		rig.run = srv.runInterestTick
	}
	for i := 0; i < clients; i++ {
		rig.conns = append(rig.conns, newSpaceClient(t, mustDialGameClient(t, "ws"+httpServer.URL[4:])))
		waitForSessionIDs(t, srv, i+1)
	}
	// Session IDs are assigned in dial order.
	rig.ids = sortedInt64(srv.listener.SessionIDs())
	return rig
}

func (r *spaceTestRig) tick(t *testing.T) {
	t.Helper()
	if err := r.run(); err != nil {
		t.Fatalf("replication tick: %v", err)
	}
	r.assertKnownInsideSentSpace(t)
}

// assertKnownInsideSentSpace checks the invariant that every session's known
// set lies entirely inside its announced space.
func (r *spaceTestRig) assertKnownInsideSentSpace(t *testing.T) {
	t.Helper()
	s := r.srv
	s.interestMu.Lock()
	defer s.interestMu.Unlock()
	for _, sid := range r.ids {
		var known map[int64]struct{}
		if s.interest != nil {
			known = s.interest.Known(sid)
		} else {
			known = s.broadcastKnown[sid]
		}
		for id := range known {
			if space, ok := s.reg.Space(id); ok && space != s.sentSpace[sid] {
				t.Fatalf("session %d knows entity %d in space %d outside announced space %d", sid, id, space, s.sentSpace[sid])
			}
		}
	}
}

// spaceAvatar binds a test entity as the session's avatar. Interest mode also
// anchors the session's FOI on it.
func (r *spaceTestRig) spaceAvatar(t *testing.T, sid int64, e Entity) {
	t.Helper()
	opts := AvatarOptions{}
	if r.interestMode {
		opts.FOIRadius, opts.FOIMargin = 10, 1
	}
	if err := r.srv.SpawnAvatar(&Session{ID: sid}, e, opts); err != nil {
		t.Fatalf("SpawnAvatar: %v", err)
	}
}

func spaceModes(t *testing.T, fn func(t *testing.T, interestMode bool)) {
	for _, interestMode := range []bool{false, true} {
		name := "broadcast"
		if interestMode {
			name = "interest"
		}
		t.Run(name, func(t *testing.T) { fn(t, interestMode) })
	}
}

func indexOf(tokens []string, want string) int {
	return slices.Index(tokens, want)
}

// assertTransition checks the transition stream order: exits, SpaceChanged,
// carried full states, entered full states, SpaceSettled, and nothing else.
func assertTransition(t *testing.T, tokens []string, space SpaceID, transition uint64, exited, carried, entered []string) {
	t.Helper()
	changed := indexOf(tokens, fmt.Sprintf("changed:%d:%d", space, transition))
	settled := indexOf(tokens, fmt.Sprintf("settled:%d", transition))
	if changed < 0 || settled != len(tokens)-1 {
		t.Fatalf("tokens %q lack changed/settled for transition %d to space %d", tokens, transition, space)
	}
	want := len(exited) + 1 + len(carried) + len(entered) + 1
	if len(tokens) != want {
		t.Fatalf("tokens %q: got %d, want %d", tokens, len(tokens), want)
	}
	section := func(name string, lo, hi int, ids []string) {
		got := slices.Clone(tokens[lo:hi])
		slices.Sort(got)
		exp := slices.Clone(ids)
		slices.Sort(exp)
		if !slices.Equal(got, exp) {
			t.Fatalf("%s = %q, want %q (tokens %q)", name, got, exp, tokens)
		}
	}
	section("exited", 0, changed, exited)
	section("carried", changed+1, changed+1+len(carried), carried)
	section("entered", changed+1+len(carried), settled, entered)
}

func TestSpaceTransitionCarriesEntitiesAndRestoresSingleSpacePath(t *testing.T) {
	spaceModes(t, func(t *testing.T, interestMode bool) {
		rig := newSpaceTestRig(t, interestMode, 1)
		srv, conn, sid := rig.srv, rig.conns[0], rig.ids[0]

		avatar := &interestTickEntity{id: 1, full: []byte("avatar-full")}
		crate := &interestTickEntity{id: 2, x: 1, full: []byte("crate-full")}
		left := &interestTickEntity{id: 3, x: 2, full: []byte("left-full")}
		cave := &interestTickEntity{id: 11, x: 1, y: 1, full: []byte("cave-full")}
		rig.spaceAvatar(t, sid, avatar)
		for _, e := range []*interestTickEntity{crate, left} {
			if err := srv.CreateEntity(e); err != nil {
				t.Fatal(err)
			}
		}
		if err := srv.CreateEntityIn(5, cave); err != nil {
			t.Fatal(err)
		}
		rig.tick(t)
		first := readSpaceTokens(t, conn)
		slices.Sort(first)
		if want := []string{"avatar-full", "crate-full", "left-full"}; !slices.Equal(first, want) {
			t.Fatalf("space 0 entered %q, want %q", first, want)
		}

		if err := srv.MoveToSpace(SpaceTransfer{To: 5, Entities: []int64{avatar.id, crate.id}}); err != nil {
			t.Fatal(err)
		}
		if got := srv.SessionSpace(sid); got != 5 {
			t.Fatalf("SessionSpace before the pass = %d, want 5", got)
		}
		if got := srv.SessionsInSpace(0); !slices.Equal(got, []int64{sid}) {
			t.Fatalf("SessionsInSpace(0) before the pass = %v", got)
		}
		rig.tick(t)
		assertTransition(t, readSpaceTokens(t, conn), 5, 1,
			[]string{"removed:3"}, []string{"avatar-full", "crate-full"}, []string{"cave-full"})
		if got := srv.SessionsInSpace(5); !slices.Equal(got, []int64{sid}) {
			t.Fatalf("SessionsInSpace(5) = %v", got)
		}
		if got := srv.SessionsInSpace(0); len(got) != 0 {
			t.Fatalf("SessionsInSpace(0) = %v", got)
		}

		// Deltas of carried entities flow normally in the new space.
		crate.flush = []byte("crate-delta")
		rig.tick(t)
		if got := readSpaceTokens(t, conn); !slices.Equal(got, []string{"crate-delta"}) {
			t.Fatalf("delta after transition = %q", got)
		}
		crate.flush = nil

		if err := srv.MoveToSpace(SpaceTransfer{To: 0, Entities: []int64{avatar.id, crate.id, cave.id}}); err != nil {
			t.Fatal(err)
		}
		rig.tick(t)
		assertTransition(t, readSpaceTokens(t, conn), 0, 2,
			nil, []string{"avatar-full", "crate-full", "cave-full"}, []string{"left-full"})

		srv.interestMu.Lock()
		active := srv.spacesActiveLocked()
		filter := false
		if !interestMode {
			filter = srv.shouldFilterBroadcastLocked(srv.listener.SessionIDs(), srv.reg.All())
		}
		srv.interestMu.Unlock()
		if active || filter {
			t.Fatalf("single-space path not restored: spacesActive=%v filter=%v", active, filter)
		}
		rig.tick(t)
		expectNoMessage(t, conn)
	})
}

func TestSpaceTransitionCarriedEntityOutsideFOIExits(t *testing.T) {
	rig := newSpaceTestRig(t, true, 1)
	srv, conn, sid := rig.srv, rig.conns[0], rig.ids[0]
	avatar := &interestTickEntity{id: 1, full: []byte("avatar-full")}
	cart := &interestTickEntity{id: 2, x: 1, full: []byte("cart-full")}
	rig.spaceAvatar(t, sid, avatar)
	if err := srv.CreateEntity(cart); err != nil {
		t.Fatal(err)
	}
	rig.tick(t)
	_ = readSpaceTokens(t, conn)

	cart.x = 100
	if err := srv.MoveToSpace(SpaceTransfer{To: 3, Entities: []int64{avatar.id, cart.id}}); err != nil {
		t.Fatal(err)
	}
	rig.tick(t)
	assertTransition(t, readSpaceTokens(t, conn), 3, 1, []string{"removed:2"}, []string{"avatar-full"}, nil)
}

func TestSpaceTransitionWithVisibilityAndOwnerChangeOnSameTick(t *testing.T) {
	spaceModes(t, func(t *testing.T, interestMode bool) {
		rig := newSpaceTestRig(t, interestMode, 1)
		srv, conn, sid := rig.srv, rig.conns[0], rig.ids[0]
		avatar := &interestTickEntity{id: 1, full: []byte("avatar-full")}
		secret := &interestTickEntity{id: 2, x: 1, full: []byte("secret-full")}
		crate := &ownerScopedTickEntity{
			interestTickEntity: interestTickEntity{id: 3, x: 1, y: 1, full: []byte("crate-private")},
			publicFull:         []byte("crate-public"),
			ownerMask:          1,
		}
		rig.spaceAvatar(t, sid, avatar)
		if err := srv.CreateEntity(secret); err != nil {
			t.Fatal(err)
		}
		if err := srv.CreateEntity(crate); err != nil {
			t.Fatal(err)
		}
		rig.tick(t)
		first := readSpaceTokens(t, conn)
		slices.Sort(first)
		if want := []string{"avatar-full", "crate-public", "secret-full"}; !slices.Equal(first, want) {
			t.Fatalf("entered %q, want %q", first, want)
		}

		// One tick: everything moves, the secret becomes grouped and the crate
		// is granted to the session.
		if err := srv.MoveToSpace(SpaceTransfer{To: 8, Entities: []int64{avatar.id, secret.id, crate.id}}); err != nil {
			t.Fatal(err)
		}
		srv.SetEntityVisibilityGroup(secret.id, "hidden")
		if !srv.SetOwner(crate.id, sid) {
			t.Fatal("SetOwner failed")
		}
		rig.tick(t)
		tokens := readSpaceTokens(t, conn)
		assertTransition(t, tokens, 8, 1, []string{"removed:2"}, []string{"avatar-full", "crate-private"}, nil)
		if indexOf(tokens, "crate-public") >= 0 {
			t.Fatalf("carried owner-scoped entity used the stale public view: %q", tokens)
		}
		rig.tick(t)
		expectNoMessage(t, conn)
	})
}

func TestSpaceSessionsWithoutFOIFollowExplicitSpace(t *testing.T) {
	spaceModes(t, func(t *testing.T, interestMode bool) {
		rig := newSpaceTestRig(t, interestMode, 1)
		srv, conn, sid := rig.srv, rig.conns[0], rig.ids[0]
		lobby := &interestTickEntity{id: 1, full: []byte("lobby-full")}
		arena := &interestTickEntity{id: 2, full: []byte("arena-full")}
		if err := srv.CreateEntity(lobby); err != nil {
			t.Fatal(err)
		}
		if err := srv.CreateEntityIn(7, arena); err != nil {
			t.Fatal(err)
		}
		rig.tick(t)
		if !interestMode {
			if got := readSpaceTokens(t, conn); !slices.Equal(got, []string{"lobby-full"}) {
				t.Fatalf("space 0 broadcast = %q", got)
			}
		}

		srv.SetSessionSpace(sid, 7)
		if got := srv.SessionSpace(sid); got != 7 {
			t.Fatalf("SessionSpace = %d, want 7", got)
		}
		rig.tick(t)
		tokens := readSpaceTokens(t, conn)
		if interestMode {
			// No FOI: no entities, but the space change is still announced.
			assertTransition(t, tokens, 7, 1, nil, nil, nil)
		} else {
			assertTransition(t, tokens, 7, 1, []string{"removed:1"}, nil, []string{"arena-full"})
		}
		if got := srv.SessionsInSpace(7); !slices.Equal(got, []int64{sid}) {
			t.Fatalf("SessionsInSpace(7) = %v", got)
		}
		rig.tick(t)
		expectNoMessage(t, conn)

		srv.SetSessionSpace(sid, 0)
		rig.tick(t)
		tokens = readSpaceTokens(t, conn)
		if interestMode {
			assertTransition(t, tokens, 0, 2, nil, nil, nil)
		} else {
			assertTransition(t, tokens, 0, 2, []string{"removed:2"}, nil, []string{"lobby-full"})
		}
	})
}

func TestSpaceBroadcastServerEventReachesOnlyItsSpace(t *testing.T) {
	rig := newSpaceTestRig(t, false, 2)
	srv := rig.srv
	inCave := rig.ids[1]
	caveConn, outsideConn := rig.conns[1], rig.conns[0]
	srv.SetSessionSpace(inCave, 4)
	rig.tick(t)
	assertTransition(t, readSpaceTokens(t, caveConn), 4, 1, nil, nil, nil)
	expectNoMessage(t, outsideConn)

	if got := srv.Space(4).Sessions(); !slices.Equal(got, []int64{inCave}) {
		t.Fatalf("Space(4).Sessions = %v", got)
	}
	if err := srv.Space(4).BroadcastServerEvent(EventStream, []byte("rumble")); err != nil {
		t.Fatal(err)
	}
	if got := readSpaceTokens(t, caveConn); !slices.Equal(got, []string{"event:rumble"}) {
		t.Fatalf("cave session event = %q", got)
	}
	expectNoMessage(t, outsideConn)
	if err := srv.Space(0).BroadcastServerEvent(EventReliableOrdered, []byte("sunrise")); err != nil {
		t.Fatal(err)
	}
	if got := readSpaceTokens(t, outsideConn); !slices.Equal(got, []string{"event:sunrise"}) {
		t.Fatalf("outside session event = %q", got)
	}
	expectNoMessage(t, caveConn)
	if err := srv.Space(9).BroadcastServerEvent(EventStream, []byte("nobody")); err != nil {
		t.Fatal(err)
	}
}

func TestSpaceConnectSnapshotUsesAnnouncedSpace(t *testing.T) {
	srv := NewServer(ServerConfig{Transport: golemnet.TransportWebSocket, StateUpdateLane: StateUpdateLaneStream})
	srv.SetRemovalSerializer(func(id int64, _ uint64) ([]byte, error) { return []byte(fmt.Sprintf("removed:%d", id)), nil })
	if err := srv.CreateEntity(&interestTickEntity{id: 1, full: []byte("overworld")}); err != nil {
		t.Fatal(err)
	}
	if err := srv.CreateEntityIn(6, &interestTickEntity{id: 2, full: []byte("cave")}); err != nil {
		t.Fatal(err)
	}
	// Flush the spawns before anyone connects.
	if err := srv.runBroadcastTick(); err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(srv.Handler())
	t.Cleanup(httpServer.Close)
	conn := newSpaceClient(t, mustDialGameClient(t, "ws"+httpServer.URL[4:]))
	if got := readSpaceTokens(t, conn); !slices.Equal(got, []string{"overworld"}) {
		t.Fatalf("connect snapshot = %q, want only space 0", got)
	}
	sid := waitForSessionIDs(t, srv, 1)[0]
	if err := srv.runBroadcastTick(); err != nil {
		t.Fatal(err)
	}
	expectNoMessage(t, conn)
	if got := srv.SessionsKnowing(2); len(got) != 0 {
		t.Fatalf("session %d knows the cave entity: %v", sid, got)
	}
}

func TestSpaceDisconnectClearsSessionSpaceState(t *testing.T) {
	srv := NewServer(ServerConfig{})
	srv.SetSessionSpace(5, 2)
	srv.interestMu.Lock()
	srv.planSessionSpaceLocked(5, 2)
	srv.interestMu.Unlock()
	srv.cleanupVisibilityOnDisconnect(5)
	srv.interestMu.Lock()
	defer srv.interestMu.Unlock()
	if len(srv.sessionSpaceExplicit)+len(srv.sentSpace)+len(srv.spaceTransitionSeq)+len(srv.spaceAckPending) != 0 {
		t.Fatal("disconnect retained session space state")
	}
}

func TestMoveToSpaceValidatesBeforeMoving(t *testing.T) {
	srv := NewServer(ServerConfig{})
	for id := int64(1); id <= 2; id++ {
		if err := srv.CreateEntity(&interestTickEntity{id: id}); err != nil {
			t.Fatal(err)
		}
	}
	err := srv.MoveToSpace(SpaceTransfer{To: 3, Entities: []int64{1, 2, 99}})
	if err == nil || !strings.Contains(err.Error(), "entity 99 is not published") {
		t.Fatalf("unknown entity error = %v", err)
	}
	if err := srv.MoveToSpace(SpaceTransfer{To: 3, Entities: []int64{1, 2, 1}}); err == nil || !strings.Contains(err.Error(), "listed twice") {
		t.Fatalf("duplicate error = %v", err)
	}
	for id := int64(1); id <= 2; id++ {
		if space, _ := srv.EntitySpace(id); space != 0 {
			t.Fatalf("rejected transfer moved entity %d to %d", id, space)
		}
	}
	if err := srv.MoveToSpace(SpaceTransfer{To: 3, Entities: []int64{1, 2}}); err != nil {
		t.Fatal(err)
	}
	for id := int64(1); id <= 2; id++ {
		if space, _ := srv.EntitySpace(id); space != 3 {
			t.Fatalf("entity %d in space %d, want 3", id, space)
		}
	}
}

func TestSpaceAckGateSupersedesOlderTransitions(t *testing.T) {
	srv := NewServer(ServerConfig{})
	srv.interestMu.Lock()
	first := srv.planSessionSpaceLocked(1, 4)
	second := srv.planSessionSpaceLocked(1, 6)
	srv.interestMu.Unlock()
	if first.transition != 1 || second.transition != 2 || !second.gated || second.from != 4 {
		t.Fatalf("plans = %+v %+v", first, second)
	}
	tracker := srv.eventualTracker(1)
	tracker.markGated(eventualStateChange{id: 42, mask: 2})
	srv.applySpaceAck(1, 1) // superseded
	srv.applySpaceAck(1, 9) // unknown
	srv.interestMu.Lock()
	gated := srv.planSessionSpaceLocked(1, 6).gated
	srv.interestMu.Unlock()
	if !gated || tracker.hasDirty() {
		t.Fatal("old or unknown SpaceAck released the gate")
	}
	srv.applySpaceAck(1, 2)
	srv.interestMu.Lock()
	gated = srv.planSessionSpaceLocked(1, 6).gated
	srv.interestMu.Unlock()
	if gated {
		t.Fatal("latest SpaceAck did not release the gate")
	}
	// Entity 42 is not live, so its gated change is dropped rather than resent.
	if tracker.hasDirty() || tracker.gated != nil {
		t.Fatal("release kept gated changes for a missing entity")
	}
}
