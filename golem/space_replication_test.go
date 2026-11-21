package golem

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"path/filepath"
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
	case serverMessageWorldField:
		return "world:" + string(data)
	case serverMessageWorldClearedField:
		var name string
		r := pb.NewReader(data)
		for !r.Done() {
			f, wire := r.Tag()
			if f == 1 && wire == 2 {
				name = r.String()
				continue
			}
			r.Skip(wire)
		}
		return "cleared:" + name
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
	conn *websocket.Conn
}

func newSpaceClient(t *testing.T, conn *websocket.Conn) *spaceClient {
	t.Helper()
	c := &spaceClient{msgs: make(chan []byte, 64), conn: conn}
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
	url          string
}

func newSpaceTestRig(t *testing.T, interestMode bool, clients int) *spaceTestRig {
	t.Helper()
	return newSpaceTestRigWith(t, interestMode, clients, nil)
}

// newSpaceTestRigWith lets configure adjust the server config and seed the
// server before any client connects.
func newSpaceTestRigWith(t *testing.T, interestMode bool, clients int, configure func(*ServerConfig), seed ...func(*Server)) *spaceTestRig {
	t.Helper()
	cellSize := 0.0
	if interestMode {
		cellSize = 4
	}
	cfg := ServerConfig{Transport: golemnet.TransportWebSocket, StateUpdateLane: StateUpdateLaneStream, CellSize: cellSize}
	if configure != nil {
		configure(&cfg)
	}
	srv := NewServer(cfg)
	srv.SetRemovalSerializer(func(id int64, _ uint64) ([]byte, error) { return []byte(fmt.Sprintf("removed:%d", id)), nil })
	for _, fn := range seed {
		fn(srv)
	}
	httpServer := httptest.NewServer(srv.Handler())
	t.Cleanup(httpServer.Close)
	rig := &spaceTestRig{srv: srv, run: srv.runBroadcastTick, interestMode: interestMode, url: "ws" + httpServer.URL[4:]}
	if interestMode {
		rig.run = srv.runInterestTick
	}
	for i := 0; i < clients; i++ {
		rig.connect(t)
	}
	return rig
}

// connect dials one more client and refreshes ids, which follow dial order.
func (r *spaceTestRig) connect(t *testing.T) *spaceClient {
	t.Helper()
	before := len(r.srv.listener.SessionIDs())
	c := newSpaceClient(t, mustDialGameClient(t, r.url))
	r.conns = append(r.conns, c)
	waitForSessionIDs(t, r.srv, before+1)
	r.ids = sortedInt64(r.srv.listener.SessionIDs())
	return c
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
	assertTransitionWithWorld(t, tokens, space, transition, exited, nil, carried, entered)
}

// assertTransitionWithWorld also checks the exact world frames, in order,
// right after SpaceChanged.
func assertTransitionWithWorld(t *testing.T, tokens []string, space SpaceID, transition uint64, exited, world, carried, entered []string) {
	t.Helper()
	changed := indexOf(tokens, fmt.Sprintf("changed:%d:%d", space, transition))
	settled := indexOf(tokens, fmt.Sprintf("settled:%d", transition))
	if changed < 0 || settled != len(tokens)-1 {
		t.Fatalf("tokens %q lack changed/settled for transition %d to space %d", tokens, transition, space)
	}
	want := len(exited) + 1 + len(world) + len(carried) + len(entered) + 1
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
	if got := tokens[changed+1 : changed+1+len(world)]; !slices.Equal(got, world) {
		t.Fatalf("world = %q, want %q (tokens %q)", got, world, tokens)
	}
	carriedAt := changed + 1 + len(world)
	section("carried", carriedAt, carriedAt+len(carried), carried)
	section("entered", carriedAt+len(carried), settled, entered)
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
			filter = srv.shouldFilterBroadcastLocked(srv.listener.SessionIDs(), srv.reg.All(), false)
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

// A space-scoped event sent on the tick a session's move was requested follows
// the session: it receives the event of the space it moves to, after its
// transition, and not the event of the space it leaves.
func TestSpaceBroadcastServerEventFollowsMovingSession(t *testing.T) {
	spaceModes(t, func(t *testing.T, interestMode bool) {
		rig := newSpaceTestRig(t, interestMode, 2)
		srv := rig.srv
		mover, moverConn, stayerConn := rig.ids[0], rig.conns[0], rig.conns[1]
		srv.SetSessionSpace(mover, 4)
		if got := srv.Space(0).Sessions(); len(got) != 2 {
			t.Fatalf("announced sessions of space 0 before the pass = %v", got)
		}
		for space, event := range map[SpaceID]string{0: "overworld-explosion", 4: "cave-door-opened"} {
			if err := srv.Space(space).BroadcastServerEvent(EventStream, []byte(event)); err != nil {
				t.Fatal(err)
			}
		}
		// The cave event is held until the transition; the other session gets
		// the overworld event at once.
		if got := readSpaceTokens(t, stayerConn); !slices.Equal(got, []string{"event:overworld-explosion"}) {
			t.Fatalf("staying session = %q", got)
		}
		expectNoMessage(t, moverConn)
		rig.tick(t)
		if got := readSpaceTokens(t, moverConn); !slices.Equal(got, []string{"changed:4:1", "settled:1", "event:cave-door-opened"}) {
			t.Fatalf("moving session = %q", got)
		}
		expectNoMessage(t, stayerConn)
	})
}

// The same for a session that follows its avatar through MoveToSpace.
func TestSpaceBroadcastServerEventFollowsMovedAvatar(t *testing.T) {
	spaceModes(t, func(t *testing.T, interestMode bool) {
		rig := newSpaceTestRig(t, interestMode, 2)
		srv := rig.srv
		mover, moverConn, stayerConn := rig.ids[0], rig.conns[0], rig.conns[1]
		avatar := &interestTickEntity{id: 1, full: []byte("avatar-full")}
		rig.spaceAvatar(t, mover, avatar)
		rig.tick(t)
		readSpaceTokens(t, moverConn)
		if !interestMode {
			readSpaceTokens(t, stayerConn)
		}
		if err := srv.MoveToSpace(SpaceTransfer{To: 3, Entities: []int64{avatar.id}}); err != nil {
			t.Fatal(err)
		}
		for space, event := range map[SpaceID]string{0: "overworld-explosion", 3: "cave-door-opened"} {
			if err := srv.Space(space).BroadcastServerEvent(EventStream, []byte(event)); err != nil {
				t.Fatal(err)
			}
		}
		rig.tick(t)
		got := readSpaceTokens(t, moverConn)
		if settled, event := indexOf(got, "settled:1"), indexOf(got, "event:cave-door-opened"); settled < 0 || event != len(got)-1 || event < settled || indexOf(got, "event:overworld-explosion") >= 0 {
			t.Fatalf("moving session = %q", got)
		}
		if got := readSpaceTokens(t, stayerConn); indexOf(got, "event:overworld-explosion") < 0 || indexOf(got, "event:cave-door-opened") >= 0 {
			t.Fatalf("staying session = %q", got)
		}
	})
}

// A recipient whose switch is pending holds the event even when nothing made
// it hold before (a space change that does not mark its sessions).
func TestSpaceBroadcastServerEventHoldsUnmarkedMovingSession(t *testing.T) {
	rig := newSpaceTestRig(t, false, 1)
	srv := rig.srv
	mover, conn := rig.ids[0], rig.conns[0]
	srv.interestMu.Lock()
	srv.sessionSpaceExplicit[mover] = 4 // pending switch, no hold
	srv.interestMu.Unlock()
	if err := srv.Space(4).BroadcastServerEvent(EventStream, []byte("cave-door-opened")); err != nil {
		t.Fatal(err)
	}
	expectNoMessage(t, conn)
	rig.tick(t)
	if got := readSpaceTokens(t, conn); !slices.Equal(got, []string{"changed:4:1", "settled:1", "event:cave-door-opened"}) {
		t.Fatalf("moving session = %q", got)
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

// snapshotIDs returns the entity IDs of a connect snapshot, sorted.
func snapshotIDs(t *testing.T, srv *Server, sid int64) []int64 {
	t.Helper()
	frames, err := srv.entitySnapshotForSession(sid)
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]int64, 0, len(frames))
	for _, f := range frames {
		ids = append(ids, f.EntityID)
	}
	slices.Sort(ids)
	return ids
}

// Session spaces without any entity outside space 0 take the plain entity
// list (no per-entity space) in filtered broadcast and the connect snapshot,
// and behave exactly like the spaced list.
func TestSpaceSessionSpaceWithoutSpacedEntitiesUsesPlainList(t *testing.T) {
	rig := newSpaceTestRig(t, false, 2)
	srv := rig.srv
	stay, away := rig.ids[0], rig.ids[1]
	for i := int64(1); i <= 2; i++ {
		if err := srv.CreateEntity(&interestTickEntity{id: i, full: []byte(fmt.Sprintf("full-%d", i))}); err != nil {
			t.Fatal(err)
		}
	}
	rig.tick(t)
	for _, c := range rig.conns {
		got := readSpaceTokens(t, c)
		slices.Sort(got)
		if !slices.Equal(got, []string{"full-1", "full-2"}) {
			t.Fatalf("spawns = %q", got)
		}
	}

	srv.SetSessionSpace(away, 5)
	rig.tick(t)
	if srv.reg.SpacesInUse() {
		t.Fatal("no entity left space 0")
	}
	assertTransition(t, readSpaceTokens(t, rig.conns[1]), 5, 1, []string{"removed:1", "removed:2"}, nil, nil)
	expectNoMessage(t, rig.conns[0])
	rig.tick(t)
	expectNoMessage(t, rig.conns[1])
	plainStay, plainAway := snapshotIDs(t, srv, stay), snapshotIDs(t, srv, away)
	if !slices.Equal(plainStay, []int64{1, 2}) || len(plainAway) != 0 {
		t.Fatalf("plain connect snapshots: space 0 = %v, space 5 = %v", plainStay, plainAway)
	}

	srv.SetSessionSpace(away, 0)
	rig.tick(t)
	assertTransition(t, readSpaceTokens(t, rig.conns[1]), 0, 2, nil, nil, []string{"full-1", "full-2"})
	expectNoMessage(t, rig.conns[0])

	// One entity in another space switches to the spaced list.
	if err := srv.CreateEntityIn(9, &interestTickEntity{id: 3, full: []byte("full-3")}); err != nil {
		t.Fatal(err)
	}
	if !srv.reg.SpacesInUse() {
		t.Fatal("entity 3 is in space 9")
	}
	if got := snapshotIDs(t, srv, stay); !slices.Equal(got, plainStay) {
		t.Fatalf("spaced connect snapshot = %v, plain = %v", got, plainStay)
	}
	rig.tick(t)
	expectNoMessage(t, rig.conns[0])
	expectNoMessage(t, rig.conns[1])
	scratch := srv.replicationScratch.spaced
	if cap(scratch) == 0 || len(scratch) != 0 {
		t.Fatalf("spaced scratch len %d cap %d after a spaced pass", len(scratch), cap(scratch))
	}
	for i, se := range scratch[:cap(scratch)] {
		if se.Entity != nil {
			t.Fatalf("spaced scratch[%d] still holds entity %d", i, se.Entity.EntityID())
		}
	}
}

// The tick drains of SpaceAcks and SaveSnapshotOnTick requests skip their
// locks through queued flags that every take and the shutdown failure clear.
func TestQuietTickQueuedFlags(t *testing.T) {
	srv := NewServer(ServerConfig{})
	srv.applySpaceAcks()
	srv.storeSpaceAck(4, 7)
	if !srv.spaceAckQueued.Load() {
		t.Fatal("stored SpaceAck did not set the queued flag")
	}
	srv.applySpaceAcks()
	if srv.spaceAckQueued.Load() || srv.spaceAckInbox != nil {
		t.Fatal("applySpaceAcks left the inbox queued")
	}
	if got := srv.takeEntityResyncs(); got != nil || srv.entityResyncQueued.Load() {
		t.Fatalf("empty EntityResync take = %v", got)
	}

	srv.lifeMu.Lock()
	srv.life = lifecycleRunning
	srv.lifeMu.Unlock()
	srv.drainSnapshotRequests()
	done := srv.SaveSnapshotOnTick("fp", filepath.Join(t.TempDir(), "a.snap"))
	if !srv.snapshotRequestsQueued.Load() {
		t.Fatal("queued snapshot request did not set the flag")
	}
	srv.drainSnapshotRequests()
	if srv.snapshotRequestsQueued.Load() {
		t.Fatal("drain left the snapshot flag set")
	}
	if err := <-done; err != nil {
		t.Fatalf("drained snapshot: %v", err)
	}
	pending := srv.SaveSnapshotOnTick("fp", filepath.Join(t.TempDir(), "b.snap"))
	srv.lifeMu.Lock()
	srv.life = lifecycleStopped
	srv.failSnapshotRequestsLocked()
	srv.lifeMu.Unlock()
	if srv.snapshotRequestsQueued.Load() {
		t.Fatal("failing pending snapshots left the flag set")
	}
	if err := <-pending; !errors.Is(err, ErrServerNotRunning) {
		t.Fatalf("pending snapshot at shutdown = %v", err)
	}
}

func TestSpaceDisconnectClearsSessionSpaceState(t *testing.T) {
	srv := NewServer(ServerConfig{})
	srv.interestMu.Lock()
	srv.sessionSpaceExplicit[5] = 2
	srv.sessionSpaceKept[5] = 3
	srv.planSessionSpaceLocked(5, 2)
	srv.foiOrphanKnown[5] = map[int64]uint64{1: 0}
	srv.interestMu.Unlock()
	srv.cleanupVisibilityOnDisconnect(5)
	srv.interestMu.Lock()
	defer srv.interestMu.Unlock()
	if len(srv.sessionSpaceExplicit)+len(srv.sessionSpaceKept)+len(srv.sentSpace)+len(srv.spaceTransitionSeq)+len(srv.spaceAckPending)+len(srv.spaceAnnouncing)+len(srv.foiOrphanKnown) != 0 {
		t.Fatal("disconnect retained session space state")
	}
}

// SetSessionSpace for a session that is not connected stores nothing, so it
// cannot keep broadcast replication on the filtered path for good.
func TestSetSessionSpaceIgnoresSessionsThatAreGone(t *testing.T) {
	rig := newSpaceTestRig(t, false, 2)
	srv := rig.srv
	if err := srv.CreateEntity(&interestTickEntity{id: 1, full: []byte("e-full")}); err != nil {
		t.Fatal(err)
	}
	rig.tick(t)
	state := func() (explicit int, filtered bool) {
		srv.interestMu.Lock()
		defer srv.interestMu.Unlock()
		return len(srv.sessionSpaceExplicit), srv.shouldFilterBroadcastLocked(srv.listener.SessionIDs(), srv.reg.All(), false)
	}
	check := func(what string) {
		t.Helper()
		rig.tick(t)
		if explicit, filtered := state(); explicit != 0 || filtered {
			t.Fatalf("%s: explicit entries = %d, filtered broadcast = %v", what, explicit, filtered)
		}
	}

	srv.SetSessionSpace(987654, 3)
	check("unknown session")

	// The tick's disconnect cleanup can run while the listener still lists
	// the session.
	early := rig.ids[0]
	srv.cleanupVisibilityOnDisconnect(early)
	srv.SetSessionSpace(early, 3)
	check("cleaned-up session the listener still lists")

	// A real disconnect: the listener drops the session, then the tick cleans up.
	gone := rig.ids[1]
	_ = rig.conns[1].conn.CloseNow()
	for deadline := time.Now().Add(3 * time.Second); ; {
		if _, listed := srv.listener.SessionTransport(gone); !listed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("listener kept the closed session")
		}
		time.Sleep(time.Millisecond)
	}
	srv.SetSessionSpace(gone, 3) // before the tick's cleanup
	srv.drainMessages()
	srv.SetSessionSpace(gone, 3) // after it
	check("disconnected session")
	srv.interestMu.Lock()
	_, marked := srv.closedSessions[gone]
	srv.interestMu.Unlock()
	if marked {
		t.Fatal("a session the listener dropped stayed marked closed")
	}
}

// A session whose avatar is deleted stays in the avatar's last space: the
// player's death and respawn in a cave cause no space transition.
func TestSpaceSessionKeepsDeletedAvatarSpace(t *testing.T) {
	spaceModes(t, func(t *testing.T, interestMode bool) {
		rig := newSpaceTestRig(t, interestMode, 1)
		srv, conn, sid := rig.srv, rig.conns[0], rig.ids[0]
		if err := srv.CreateEntity(&interestTickEntity{id: 2, x: 1, full: []byte("overworld-full")}); err != nil {
			t.Fatal(err)
		}
		if err := srv.CreateEntityIn(3, &interestTickEntity{id: 3, x: 1, full: []byte("cave-full")}); err != nil {
			t.Fatal(err)
		}
		spawn := func(id int64, space SpaceID) {
			t.Helper()
			opts := AvatarOptions{Space: space}
			if interestMode {
				opts.FOIRadius, opts.FOIMargin = 10, 1
			}
			if err := srv.SpawnAvatar(&Session{ID: sid}, &interestTickEntity{id: id, full: []byte(fmt.Sprintf("avatar-%d", id))}, opts); err != nil {
				t.Fatal(err)
			}
		}
		noTransition := func(what string, want []string) {
			t.Helper()
			rig.tick(t)
			got := readSpaceTokens(t, conn)
			slices.Sort(got)
			if !slices.Equal(got, want) || srv.SessionSpace(sid) != 3 {
				t.Fatalf("%s: tokens %q, session space %d", what, got, srv.SessionSpace(sid))
			}
		}
		spawn(1, 3)
		rig.tick(t)
		assertTransition(t, readSpaceTokens(t, conn), 3, 1, nil, nil, []string{"avatar-1", "cave-full"})

		srv.DeleteEntity(1)
		noTransition("avatar death", []string{"removed:1"})
		spawn(4, 3)
		noTransition("respawn", []string{"avatar-4"})

		// An explicit space still wins over the remembered one.
		srv.SetSessionSpace(sid, 2)
		srv.DeleteEntity(4)
		if interestMode {
			// An FOI keeps its last space while its anchor is missing.
			srv.RemoveFOI(sid)
		}
		rig.tick(t)
		if got := readSpaceTokens(t, conn); indexOf(got, "changed:2:2") < 0 || srv.SessionSpace(sid) != 2 {
			t.Fatalf("explicit space after avatar death: tokens %q, session space %d", got, srv.SessionSpace(sid))
		}
	})
}

// An avatar that changes space and is deleted before one replication pass
// leaves its session in the space it died in, with or without interest
// management: the session is told about the space change once.
func TestSpaceSessionFollowsAvatarMovedAndDeletedInOneTick(t *testing.T) {
	spaceModes(t, func(t *testing.T, interestMode bool) {
		rig := newSpaceTestRig(t, interestMode, 1)
		srv, conn, sid := rig.srv, rig.conns[0], rig.ids[0]
		rig.spaceAvatar(t, sid, &interestTickEntity{id: 1, full: []byte("avatar-full")})
		if err := srv.CreateEntityIn(7, &interestTickEntity{id: 2, full: []byte("cave-full")}); err != nil {
			t.Fatal(err)
		}
		rig.tick(t)
		readSpaceTokens(t, conn)
		if err := srv.SetEntitySpace(1, 7); err != nil {
			t.Fatal(err)
		}
		srv.DeleteEntity(1)
		if got := srv.SessionSpace(sid); got != 7 {
			t.Fatalf("session space after the avatar died = %d, want 7", got)
		}
		rig.tick(t)
		got := readSpaceTokens(t, conn)
		if indexOf(got, "removed:1") < 0 || indexOf(got, "changed:7:1") < 0 || got[len(got)-1] != "settled:1" || srv.SessionSpace(sid) != 7 {
			t.Fatalf("tokens %q, session space %d", got, srv.SessionSpace(sid))
		}
		rig.tick(t)
		expectNoMessage(t, conn)
	})
}

// SetSessionSpace forgets the deleted avatar's space, so space 0 can be chosen.
func TestSetSessionSpaceForgetsDeletedAvatarSpace(t *testing.T) {
	rig := newSpaceTestRig(t, false, 1)
	srv, conn, sid := rig.srv, rig.conns[0], rig.ids[0]
	rig.spaceAvatar(t, sid, &interestTickEntity{id: 1, full: []byte("avatar-full")})
	if err := srv.SetEntitySpace(1, 3); err != nil {
		t.Fatal(err)
	}
	rig.tick(t)
	readSpaceTokens(t, conn)
	srv.DeleteEntity(1)
	rig.tick(t)
	if got := readSpaceTokens(t, conn); !slices.Equal(got, []string{"removed:1"}) || srv.SessionSpace(sid) != 3 {
		t.Fatalf("avatar death: tokens %q, session space %d", got, srv.SessionSpace(sid))
	}
	srv.SetSessionSpace(sid, 0)
	rig.tick(t)
	assertTransition(t, readSpaceTokens(t, conn), 0, 2, nil, nil, nil)
	srv.interestMu.Lock()
	active := srv.spacesActiveLocked()
	srv.interestMu.Unlock()
	if active || srv.SessionSpace(sid) != 0 {
		t.Fatalf("space bookkeeping stayed active = %v, session space %d", active, srv.SessionSpace(sid))
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

// seedSpaceWorlds defines world data in the global store and in spaces 0, 4
// and 9, so a transition hits every rule: override, global fallback, clear.
func seedSpaceWorlds(srv *Server) {
	srv.World.Set(&sessionWorldData{name: "fog", payload: []byte("global-fog")})
	srv.World.Set(&sessionWorldData{name: "zone", payload: []byte("global-zone")})
	srv.World.Set(&sessionWorldData{name: "secret", payload: []byte("global-secret")})
	srv.Space(0).World().Set(&sessionWorldData{name: "light", payload: []byte("day")})
	srv.Space(4).World().Set(&sessionWorldData{name: "fog", payload: []byte("cave-fog")})
	srv.Space(4).World().Set(&sessionWorldData{name: "secret", payload: []byte("cave-secret")})
	srv.Space(9).World().Set(&sessionWorldData{name: "music", payload: []byte("arena-music")})
	srv.Space(9).World().Set(&sessionWorldData{name: "hidden", payload: []byte("arena-hidden")})
}

func excludeSecrets(cfg *ServerConfig) { cfg.WorldSnapshotExclude = []string{"secret", "hidden"} }

func TestSpaceTransitionSendsWorldDataBetweenChangedAndCarried(t *testing.T) {
	spaceModes(t, func(t *testing.T, interestMode bool) {
		rig := newSpaceTestRigWith(t, interestMode, 1, excludeSecrets, seedSpaceWorlds)
		srv, conn, sid := rig.srv, rig.conns[0], rig.ids[0]
		// Connect: Server.World merged with space 0, names only other spaces
		// define cleared, excluded names untouched.
		if got, want := readSpaceTokens(t, conn), []string{"world:global-fog", "world:day", "cleared:music", "world:global-zone"}; !slices.Equal(got, want) {
			t.Fatalf("connect world = %q, want %q", got, want)
		}

		avatar := &interestTickEntity{id: 1, full: []byte("avatar-full")}
		rig.spaceAvatar(t, sid, avatar)
		rig.tick(t)
		if got := readSpaceTokens(t, conn); !slices.Equal(got, []string{"avatar-full"}) {
			t.Fatalf("space 0 entered %q", got)
		}

		if err := srv.SetEntitySpace(avatar.id, 4); err != nil {
			t.Fatal(err)
		}
		rig.tick(t)
		// fog overridden, light and music defined by no store reachable from
		// space 4, secret excluded; zone is only global and already current.
		assertTransitionWithWorld(t, readSpaceTokens(t, conn), 4, 1, nil,
			[]string{"world:cave-fog", "cleared:light", "cleared:music"}, []string{"avatar-full"}, nil)

		if err := srv.SetEntitySpace(avatar.id, 9); err != nil {
			t.Fatal(err)
		}
		rig.tick(t)
		assertTransitionWithWorld(t, readSpaceTokens(t, conn), 9, 2, nil,
			[]string{"world:global-fog", "cleared:light", "world:arena-music"}, []string{"avatar-full"}, nil)

		if err := srv.SetEntitySpace(avatar.id, 0); err != nil {
			t.Fatal(err)
		}
		rig.tick(t)
		assertTransitionWithWorld(t, readSpaceTokens(t, conn), 0, 3, nil,
			[]string{"world:global-fog", "world:day", "cleared:music"}, []string{"avatar-full"}, nil)
		rig.tick(t)
		expectNoMessage(t, conn)
	})
}

func TestSpaceTransitionWithoutSpaceWorldsSendsNoWorldFrames(t *testing.T) {
	rig := newSpaceTestRigWith(t, false, 1, nil, func(srv *Server) {
		srv.World.Set(&sessionWorldData{name: "zone", payload: []byte("global-zone")})
		srv.Space(3).World() // an empty store defines nothing
	})
	srv, conn, sid := rig.srv, rig.conns[0], rig.ids[0]
	if got := readSpaceTokens(t, conn); !slices.Equal(got, []string{"world:global-zone"}) {
		t.Fatalf("connect world = %q", got)
	}
	srv.SetSessionSpace(sid, 3)
	rig.tick(t)
	assertTransitionWithWorld(t, readSpaceTokens(t, conn), 3, 1, nil, nil, nil, nil)
}

func TestSpaceWorldReconnectClearsPreviousSpaceData(t *testing.T) {
	rig := newSpaceTestRigWith(t, false, 1, excludeSecrets, seedSpaceWorlds)
	srv, conn, sid := rig.srv, rig.conns[0], rig.ids[0]
	readSpaceTokens(t, conn)
	srv.SetSessionSpace(sid, 9)
	rig.tick(t)
	assertTransitionWithWorld(t, readSpaceTokens(t, conn), 9, 1, nil,
		[]string{"world:global-fog", "cleared:light", "world:arena-music"}, nil, nil)

	// The client reconnects into space 0: the connect snapshot drops the
	// arena's music and restores space 0's light.
	conn.conn.CloseNow()
	deadline := time.Now().Add(2 * time.Second)
	for len(srv.listener.SessionIDs()) != 0 {
		if time.Now().After(deadline) {
			t.Fatal("session did not disconnect")
		}
		time.Sleep(10 * time.Millisecond)
	}
	again := rig.connect(t)
	if got, want := readSpaceTokens(t, again), []string{"world:global-fog", "world:day", "cleared:music", "world:global-zone"}; !slices.Equal(got, want) {
		t.Fatalf("reconnect world = %q, want %q", got, want)
	}
	// The first transition into a non-zero space resolves everything again.
	srv.SetSessionSpace(rig.ids[0], 4)
	rig.tick(t)
	assertTransitionWithWorld(t, readSpaceTokens(t, again), 4, 1, nil,
		[]string{"world:cave-fog", "cleared:light", "cleared:music"}, nil, nil)
}

func TestSpaceWorldExcludedNamesStayCallerDelivered(t *testing.T) {
	rig := newSpaceTestRigWith(t, false, 2, excludeSecrets, seedSpaceWorlds)
	srv := rig.srv
	inCave, outside := rig.ids[1], rig.ids[0]
	caveConn, outsideConn := rig.conns[1], rig.conns[0]
	readSpaceTokens(t, caveConn)
	readSpaceTokens(t, outsideConn)

	srv.SetSessionSpace(inCave, 4)
	rig.tick(t)
	// No secret frame, neither sent nor cleared.
	assertTransitionWithWorld(t, readSpaceTokens(t, caveConn), 4, 1, nil,
		[]string{"world:cave-fog", "cleared:light", "cleared:music"}, nil, nil)
	expectNoMessage(t, outsideConn)

	// Explicit delivery resolves the session's announced space.
	if err := srv.SendStoredWorldData(inCave, "secret"); err != nil {
		t.Fatal(err)
	}
	if got := readSpaceTokens(t, caveConn); !slices.Equal(got, []string{"world:cave-secret"}) {
		t.Fatalf("cave SendStoredWorldData = %q", got)
	}
	if err := srv.SendStoredWorldData(outside, "secret"); err != nil {
		t.Fatal(err)
	}
	if got := readSpaceTokens(t, outsideConn); !slices.Equal(got, []string{"world:global-secret"}) {
		t.Fatalf("outside SendStoredWorldData = %q", got)
	}
	if err := srv.Space(4).PushWorldData("secret"); err != nil {
		t.Fatal(err)
	}
	if got := readSpaceTokens(t, caveConn); !slices.Equal(got, []string{"world:cave-secret"}) {
		t.Fatalf("cave Space.PushWorldData = %q", got)
	}
	expectNoMessage(t, outsideConn)

	// Leaving the cave does not clear the caller-delivered value either.
	srv.SetSessionSpace(inCave, 0)
	rig.tick(t)
	assertTransitionWithWorld(t, readSpaceTokens(t, caveConn), 0, 2, nil,
		[]string{"world:global-fog", "world:day", "cleared:music"}, nil, nil)
}

// TestSpaceAckAppliedWhenMessageQueueIsFull: a SpaceAck bypasses the bounded
// message queue, so a full queue cannot leave the session gated forever, and
// the queued messages are not displaced.
func TestSpaceAckAppliedWhenMessageQueueIsFull(t *testing.T) {
	srv := NewServer(ServerConfig{Transport: golemnet.TransportWebSocket, StateUpdateLane: StateUpdateLaneStream})
	srv.interestMu.Lock()
	plan := srv.planSessionSpaceLocked(3, 5)
	srv.interestMu.Unlock()
	for i := 0; i < msgQueueCap; i++ {
		srv.msgQueue <- pendingMsg{kind: msgMessage, sess: &Session{ID: 3}}
	}
	srv.enqueueSpaceAck(&Session{ID: 3}, plan.transition)
	if len(srv.msgQueue) != msgQueueCap {
		t.Fatalf("queue length = %d, want %d", len(srv.msgQueue), msgQueueCap)
	}
	messages := 0
	srv.OnMessage(func(*Session, []byte) { messages++ })
	srv.drainMessages()
	if messages != msgQueueCap {
		t.Fatalf("drained %d messages, want %d", messages, msgQueueCap)
	}
	srv.interestMu.Lock()
	_, pending := srv.spaceAckPending[3]
	active := srv.spacesActiveLocked()
	srv.interestMu.Unlock()
	if pending {
		t.Fatal("SpaceAck was dropped with a full message queue")
	}
	if !active {
		t.Fatal("announced space 5 must keep spaces active")
	}
	srv.spaceAckMu.Lock()
	left := len(srv.spaceAckInbox)
	srv.spaceAckMu.Unlock()
	if left != 0 {
		t.Fatalf("SpaceAck inbox kept %d entries", left)
	}
}

// TestSpaceWorldPushFromOnUpdatesOnSwitchTick: during the pass that plans a
// transition, the session still counts as announced in its old space until
// its transition is queued. A world push from OnUpdates reaches only sessions
// announced in the destination; the switching session gets the pushed value
// inside its transition, after SpaceChanged.
func TestSpaceWorldPushFromOnUpdatesOnSwitchTick(t *testing.T) {
	spaceModes(t, func(t *testing.T, interestMode bool) {
		rig := newSpaceTestRigWith(t, interestMode, 2, nil, func(srv *Server) {
			srv.Space(4).World().Set(&sessionWorldData{name: "fog", payload: []byte("cave-fog-1")})
		})
		srv := rig.srv
		mover, resident := rig.ids[0], rig.ids[1]
		moverConn, residentConn := rig.conns[0], rig.conns[1]
		for _, c := range rig.conns {
			if got := readSpaceTokens(t, c); !slices.Equal(got, []string{"cleared:fog"}) {
				t.Fatalf("connect world = %q", got)
			}
		}
		a := &interestTickEntity{id: 1, full: []byte("a-full")}
		b := &interestTickEntity{id: 2, full: []byte("b-full")}
		rig.spaceAvatar(t, mover, a)
		rig.spaceAvatar(t, resident, b)
		if err := srv.SetEntitySpace(b.id, 4); err != nil {
			t.Fatal(err)
		}
		rig.tick(t)
		if got := readSpaceTokens(t, moverConn); !slices.Equal(got, []string{"a-full"}) {
			t.Fatalf("mover space 0 = %q", got)
		}
		assertTransitionWithWorld(t, readSpaceTokens(t, residentConn), 4, 1, nil, []string{"world:cave-fog-1"}, nil, []string{"b-full"})

		if err := srv.SetEntitySpace(a.id, 4); err != nil {
			t.Fatal(err)
		}
		// A spawn nobody sees makes OnUpdates run on the switch tick.
		if err := srv.CreateEntityIn(9, &interestTickEntity{id: 50, full: []byte("hidden-full")}); err != nil {
			t.Fatal(err)
		}
		var inCave, inOverworld []int64
		srv.OnUpdates(func([][]byte) {
			inCave, inOverworld = srv.SessionsInSpace(4), srv.SessionsInSpace(0)
			srv.Space(4).World().Set(&sessionWorldData{name: "fog", payload: []byte("cave-fog-2")})
			if err := srv.Space(4).PushWorldData("fog"); err != nil {
				t.Error(err)
			}
		})
		rig.tick(t)
		srv.OnUpdates(nil)
		if !slices.Equal(inCave, []int64{resident}) || !slices.Equal(inOverworld, []int64{mover}) {
			t.Fatalf("OnUpdates saw SessionsInSpace(4) = %v, (0) = %v", inCave, inOverworld)
		}
		if got := readSpaceTokens(t, residentConn); !slices.Equal(got, []string{"world:cave-fog-2", "a-full"}) {
			t.Fatalf("resident = %q", got)
		}
		assertTransitionWithWorld(t, readSpaceTokens(t, moverConn), 4, 1, nil, []string{"world:cave-fog-2"}, []string{"a-full"}, []string{"b-full"})
		if got := srv.SessionsInSpace(4); !slices.Equal(got, []int64{mover, resident}) {
			t.Fatalf("SessionsInSpace(4) after the pass = %v", got)
		}
	})
}

// TestSpaceSpectatorRemoveFOIThenSwitchRemovesOldEntities: a session that
// drops its FOI keeps the entities it knew frozen; its next space transition
// removes them, including one deleted meanwhile (with its removal revision).
func TestSpaceSpectatorRemoveFOIThenSwitchRemovesOldEntities(t *testing.T) {
	rig := newSpaceTestRig(t, true, 1)
	srv, conn, sid := rig.srv, rig.conns[0], rig.ids[0]
	srv.SetRemovalSerializer(func(id int64, revision uint64) ([]byte, error) {
		return []byte(fmt.Sprintf("removed:%d@%d", id, revision)), nil
	})
	anchor := &interestTickEntity{id: 1, full: []byte("anchor-full"), revision: 3}
	crate := &interestTickEntity{id: 2, x: 1, full: []byte("crate-full"), revision: 5}
	arena := &interestTickEntity{id: 3, full: []byte("arena-full")}
	for _, e := range []*interestTickEntity{anchor, crate} {
		if err := srv.CreateEntity(e); err != nil {
			t.Fatal(err)
		}
	}
	if err := srv.CreateEntityIn(7, arena); err != nil {
		t.Fatal(err)
	}
	srv.AssignFOI(sid, anchor.id, 10, 1)
	rig.tick(t)
	got := readSpaceTokens(t, conn)
	if slices.Sort(got); !slices.Equal(got, []string{"anchor-full", "crate-full"}) {
		t.Fatalf("entered = %q", got)
	}

	srv.RemoveFOI(sid)
	rig.tick(t)
	expectNoMessage(t, conn) // frozen: no removals without a transition
	srv.DeleteEntity(crate.id)
	rig.tick(t)
	expectNoMessage(t, conn)

	srv.SetSessionSpace(sid, 7)
	rig.tick(t)
	// The deleted crate exits with its removal revision (revision+1), the
	// live anchor with its current revision.
	assertTransition(t, readSpaceTokens(t, conn), 7, 1, []string{"removed:1@3", "removed:2@6"}, nil, nil)
	srv.interestMu.Lock()
	orphans := len(srv.foiOrphanKnown)
	srv.interestMu.Unlock()
	if orphans != 0 {
		t.Fatal("transition kept the orphaned known set")
	}
	rig.tick(t)
	expectNoMessage(t, conn)
}

// TestSpaceAssignFOIInAnotherSpaceExitsOrphans: RemoveFOI, then AssignFOI
// anchored in space 3 before the next pass. The pass's transition removes the
// space-0 entities the client kept from the removed FOI, then enters space 3.
func TestSpaceAssignFOIInAnotherSpaceExitsOrphans(t *testing.T) {
	rig := newSpaceTestRig(t, true, 1)
	srv, conn, sid := rig.srv, rig.conns[0], rig.ids[0]
	anchor := &interestTickEntity{id: 1, full: []byte("anchor-full")}
	crate := &interestTickEntity{id: 2, x: 1, full: []byte("crate-full")}
	beacon := &interestTickEntity{id: 3, full: []byte("beacon-full")}
	for _, e := range []*interestTickEntity{anchor, crate} {
		if err := srv.CreateEntity(e); err != nil {
			t.Fatal(err)
		}
	}
	if err := srv.CreateEntityIn(3, beacon); err != nil {
		t.Fatal(err)
	}
	srv.AssignFOI(sid, anchor.id, 10, 1)
	rig.tick(t)
	if got := readSpaceTokens(t, conn); len(got) != 2 {
		t.Fatalf("entered = %q", got)
	}

	srv.RemoveFOI(sid)
	srv.AssignFOI(sid, beacon.id, 10, 1)
	rig.tick(t)
	assertTransition(t, readSpaceTokens(t, conn), 3, 1, []string{"removed:1", "removed:2"}, nil, []string{"beacon-full"})
	srv.interestMu.Lock()
	orphans := len(srv.foiOrphanKnown)
	srv.interestMu.Unlock()
	if orphans != 0 {
		t.Fatal("the pass kept the orphaned known set")
	}
	rig.tick(t)
	expectNoMessage(t, conn)
}

// TestAssignFOIAfterRemoveFOIExitsOutOfRangeOrphans: in a single-space game,
// RemoveFOI then AssignFOI on another anchor removes the kept entities the new
// field of interest does not cover; one it covers gets a full state and no
// exit. A deleted orphan exits with its removal revision.
func TestAssignFOIAfterRemoveFOIExitsOutOfRangeOrphans(t *testing.T) {
	rig := newSpaceTestRig(t, true, 1)
	srv, conn, sid := rig.srv, rig.conns[0], rig.ids[0]
	srv.SetRemovalSerializer(func(id int64, revision uint64) ([]byte, error) {
		return []byte(fmt.Sprintf("removed:%d@%d", id, revision)), nil
	})
	a := &interestTickEntity{id: 1, full: []byte("a-full"), revision: 3}
	crate := &interestTickEntity{id: 2, x: 8, full: []byte("crate-full"), revision: 4}
	doomed := &interestTickEntity{id: 4, x: -2, full: []byte("doomed-full"), revision: 7}
	b := &interestTickEntity{id: 3, x: 16, full: []byte("b-full")}
	for _, e := range []*interestTickEntity{a, crate, doomed, b} {
		if err := srv.CreateEntity(e); err != nil {
			t.Fatal(err)
		}
	}
	srv.AssignFOI(sid, a.id, 10, 1)
	rig.tick(t)
	got := readSpaceTokens(t, conn)
	if slices.Sort(got); !slices.Equal(got, []string{"a-full", "crate-full", "doomed-full"}) {
		t.Fatalf("entered = %q", got)
	}

	srv.RemoveFOI(sid)
	rig.tick(t)
	expectNoMessage(t, conn) // frozen while the session has no FOI
	srv.DeleteEntity(doomed.id)
	rig.tick(t)
	expectNoMessage(t, conn)

	srv.AssignFOI(sid, b.id, 10, 1)
	rig.tick(t)
	got = readSpaceTokens(t, conn)
	if slices.Sort(got); !slices.Equal(got, []string{"b-full", "crate-full", "removed:1@3", "removed:4@8"}) {
		t.Fatalf("pass after AssignFOI = %q", got)
	}
	rig.tick(t)
	expectNoMessage(t, conn)
}

// TestSpaceDeletedLocalAnchorReplacedByGlobalAnchor: deleting a local FOI
// anchor and anchoring on a global entity on the same tick still removes the
// deleted anchor on the client and from the known set.
func TestSpaceDeletedLocalAnchorReplacedByGlobalAnchor(t *testing.T) {
	for _, spaced := range []bool{false, true} {
		t.Run(fmt.Sprintf("spaced=%v", spaced), func(t *testing.T) {
			rig := newSpaceTestRig(t, true, 1)
			srv, conn, sid := rig.srv, rig.conns[0], rig.ids[0]
			space := SpaceID(0)
			if spaced {
				space = 6
			}
			anchor := &interestTickEntity{id: 1, full: []byte("anchor-full")}
			global := &interestTickEntity{id: 2, x: 500, global: true, full: []byte("global-full")}
			for _, e := range []*interestTickEntity{anchor, global} {
				if err := srv.CreateEntityIn(space, e); err != nil {
					t.Fatal(err)
				}
			}
			srv.AssignFOI(sid, anchor.id, 10, 1)
			rig.tick(t)
			tokens := readSpaceTokens(t, conn)
			if spaced {
				assertTransition(t, tokens, 6, 1, nil, nil, []string{"anchor-full", "global-full"})
			} else if slices.Sort(tokens); !slices.Equal(tokens, []string{"anchor-full", "global-full"}) {
				t.Fatalf("entered = %q", tokens)
			}

			srv.DeleteEntity(anchor.id)
			srv.AssignFOI(sid, global.id, 10, 1)
			rig.tick(t)
			if got := readSpaceTokens(t, conn); !slices.Equal(got, []string{"removed:1"}) {
				t.Fatalf("after anchor swap = %q", got)
			}
			srv.interestMu.Lock()
			_, known := srv.interest.Known(sid)[anchor.id]
			srv.interestMu.Unlock()
			if known {
				t.Fatal("deleted anchor is still known")
			}
			if got := srv.SessionsKnowing(anchor.id); len(got) != 0 {
				t.Fatalf("SessionsKnowing(deleted anchor) = %v", got)
			}
		})
	}
}

// TestSpaceOffTickWorldPushOrderedWithTransition: an off-tick
// Server.PushWorldData picks a switching session as a recipient while it is
// still announced in space 0, then stalls (test hook) until after the pass has
// planned the session's transition into a space that overrides the name. The
// transition waits for the push, so the global value lands before the
// transition and the destination value it carries wins on the client.
func TestSpaceOffTickWorldPushOrderedWithTransition(t *testing.T) {
	spaceModes(t, func(t *testing.T, interestMode bool) {
		rig := newSpaceTestRigWith(t, interestMode, 1, nil, func(srv *Server) {
			srv.World.Set(&sessionWorldData{name: "fog", payload: []byte("global-fog")})
			srv.Space(4).World().Set(&sessionWorldData{name: "fog", payload: []byte("cave-fog")})
		})
		srv, conn, sid := rig.srv, rig.conns[0], rig.ids[0]
		if got := readSpaceTokens(t, conn); !slices.Equal(got, []string{"world:global-fog"}) {
			t.Fatalf("connect world = %q", got)
		}
		avatar := &interestTickEntity{id: 1, full: []byte("avatar-full")}
		rig.spaceAvatar(t, sid, avatar)
		rig.tick(t)
		readSpaceTokens(t, conn)

		selected, release := make(chan struct{}), make(chan struct{})
		srv.testHookWorldPushSelected = func() {
			close(selected)
			<-release
		}
		pushed := make(chan error, 1)
		go func() { pushed <- srv.PushWorldData("fog") }()
		<-selected
		srv.testHookWorldPushSelected = nil

		if err := srv.SetEntitySpace(avatar.id, 4); err != nil {
			t.Fatal(err)
		}
		ticked := make(chan error, 1)
		go func() { ticked <- rig.run() }()
		for deadline := time.Now().Add(3 * time.Second); ; {
			srv.interestMu.Lock()
			planned := srv.sentSpace[sid] == 4
			srv.interestMu.Unlock()
			if planned {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("the transition was not planned")
			}
			time.Sleep(time.Millisecond)
		}
		// Give the pass time to reach the transition enqueue.
		time.Sleep(50 * time.Millisecond)
		close(release)
		if err := <-pushed; err != nil {
			t.Fatal(err)
		}
		if err := <-ticked; err != nil {
			t.Fatal(err)
		}
		rig.assertKnownInsideSentSpace(t)
		got := readSpaceTokens(t, conn)
		if len(got) == 0 || got[0] != "world:global-fog" {
			t.Fatalf("tokens = %q, want the push before the transition", got)
		}
		assertTransitionWithWorld(t, got[1:], 4, 1, nil, []string{"world:cave-fog"}, []string{"avatar-full"}, nil)
	})
}

// An FOI anchor that is still provisional does not move its session: a space
// event sent while the anchor's setup runs is not routed to the session, so a
// failed setup leaves the client where it was, with nothing from the other
// space. Once the anchor is published the session follows it.
func TestSpaceEventsIgnoreProvisionalAnchor(t *testing.T) {
	rig := newSpaceTestRig(t, true, 1)
	srv, conn, sid := rig.srv, rig.conns[0], rig.ids[0]
	srv.AssignFOI(sid, 15, 10, 0)
	rig.tick(t)
	create := func(setupErr error) error {
		return srv.CreateEntityWithSetupIn(7, &interestTickEntity{id: 15, full: []byte("anchor-full")}, func(Entity) (func() error, error) {
			if got := srv.SessionSpace(sid); got != 0 {
				t.Errorf("session space during setup = %d, want 0", got)
			}
			if err := srv.Space(7).BroadcastServerEvent(EventStream, []byte("cave-during-setup")); err != nil {
				t.Error(err)
			}
			return nil, setupErr
		})
	}
	if err := create(errors.New("setup failed")); err == nil {
		t.Fatal("setup error was not returned")
	}
	rig.tick(t)
	expectNoMessage(t, conn)
	if got := srv.SessionSpace(sid); got != 0 {
		t.Fatalf("session space after the failed setup = %d", got)
	}

	if err := create(nil); err != nil {
		t.Fatal(err)
	}
	if got := srv.SessionSpace(sid); got != 7 {
		t.Fatalf("session space after publication = %d", got)
	}
	if err := srv.Space(7).BroadcastServerEvent(EventStream, []byte("cave-after-publish")); err != nil {
		t.Fatal(err)
	}
	rig.tick(t)
	want := []string{"changed:7:1", "anchor-full", "settled:1", "event:cave-after-publish"}
	if got := readSpaceTokens(t, conn); !slices.Equal(got, want) {
		t.Fatalf("tokens = %q, want %q", got, want)
	}
}

// callbackWorldData runs a callback from MarshalUpdate, like a serializer
// that takes game locks or calls server APIs.
type callbackWorldData struct {
	name    string
	payload string
	call    func()
}

func (d *callbackWorldData) WorldName() string { return d.name }
func (d *callbackWorldData) MarshalUpdate() ([]byte, error) {
	if d.call != nil {
		d.call()
	}
	return []byte(d.payload), nil
}

// runWithin fails the test instead of hanging when fn does not return.
func runWithin(t *testing.T, what string, fn func() error) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- fn() }()
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatalf("%s did not return: deadlock", what)
		return nil
	}
}

// World data is serialized without the lock that orders sends with space
// transitions, so a serializer may call the APIs that take it.
func TestSpaceTransitionWorldSerializerMayCallServerAPIs(t *testing.T) {
	spaceModes(t, func(t *testing.T, interestMode bool) {
		rig := newSpaceTestRig(t, interestMode, 1)
		srv, conn, sid := rig.srv, rig.conns[0], rig.ids[0]
		srv.World.Set(&sessionWorldData{name: "zone", payload: []byte("global-zone")})
		srv.Space(7).World().Set(&callbackWorldData{name: "fog", payload: "cave-fog", call: func() {
			if err := srv.Space(7).BroadcastServerEvent(EventStream, []byte("from-serializer")); err != nil {
				t.Error(err)
			}
			if err := srv.PushWorldData("zone"); err != nil {
				t.Error(err)
			}
			if err := srv.SendStoredWorldData(sid, "zone"); err != nil {
				t.Error(err)
			}
		}})
		srv.SetSessionSpace(sid, 7)
		if err := runWithin(t, "the replication pass", rig.run); err != nil {
			t.Fatal(err)
		}
		// The pushes from the serializer precede the transition; the space
		// event is held and follows it.
		want := []string{"world:global-zone", "world:global-zone", "changed:7:1", "world:cave-fog", "settled:1", "event:from-serializer"}
		if got := readSpaceTokens(t, conn); !slices.Equal(got, want) {
			t.Fatalf("tokens = %q, want %q", got, want)
		}

		// The same serializer through SendStoredWorldData.
		if err := runWithin(t, "SendStoredWorldData", func() error { return srv.SendStoredWorldData(sid, "fog") }); err != nil {
			t.Fatal(err)
		}
		// The session has settled, so nothing is held: the serializer's sends
		// go out in call order, before the value it serializes.
		want = []string{"event:from-serializer", "world:global-zone", "world:global-zone", "world:cave-fog"}
		if got := readSpaceTokens(t, conn); !slices.Equal(got, want) {
			t.Fatalf("stored world tokens = %q, want %q", got, want)
		}
	})
}

// A value set and pushed after the transition serialized its world frames, but
// before the transition is queued, still reaches the switching session: the
// push skipped it as being in its old space, so the transition must carry the
// new value.
func TestSpaceTransitionRebuildsWorldFramesChangedBeforeQueueing(t *testing.T) {
	rig := newSpaceTestRigWith(t, false, 1, nil, func(srv *Server) {
		srv.Space(4).World().Set(&sessionWorldData{name: "fog", payload: []byte("cave-fog")})
	})
	srv, conn, sid := rig.srv, rig.conns[0], rig.ids[0]
	if got := readSpaceTokens(t, conn); !slices.Equal(got, []string{"cleared:fog"}) {
		t.Fatalf("connect world = %q", got)
	}
	built := 0
	srv.testHookWorldFramesBuilt = func() {
		built++
		if built == 1 {
			srv.Space(4).World().Set(&sessionWorldData{name: "fog", payload: []byte("cave-fog-v2")})
			if err := srv.Space(4).PushWorldData("fog"); err != nil {
				t.Error(err)
			}
		}
	}
	srv.SetSessionSpace(sid, 4)
	rig.tick(t)
	srv.testHookWorldFramesBuilt = nil
	if built != 2 {
		t.Fatalf("world frames built %d times, want 2", built)
	}
	assertTransitionWithWorld(t, readSpaceTokens(t, conn), 4, 1, nil, []string{"world:cave-fog-v2"}, nil, nil)
}

// A serializer that sets world data on every call can never produce frames
// that are still current; the pass reports it instead of spinning.
func TestSpaceTransitionReportsWorldDataThatNeverSettles(t *testing.T) {
	rig := newSpaceTestRig(t, false, 1)
	srv, sid := rig.srv, rig.ids[0]
	calls := 0
	srv.Space(4).World().Set(&callbackWorldData{name: "fog", payload: "cave-fog", call: func() {
		calls++
		srv.World.Set(&sessionWorldData{name: "counter", payload: []byte{byte(calls)}})
	}})
	srv.SetSessionSpace(sid, 4)
	err := runWithin(t, "the replication pass", rig.run)
	if err == nil || !strings.Contains(err.Error(), "must not set world data") || calls != worldSerializeAttempts {
		t.Fatalf("error = %v after %d serializations", err, calls)
	}
	if err := runWithin(t, "SendStoredWorldData", func() error { return srv.SendStoredWorldData(sid, "fog") }); err == nil || !strings.Contains(err.Error(), "must not set world data") {
		t.Fatalf("SendStoredWorldData error = %v", err)
	}
}
