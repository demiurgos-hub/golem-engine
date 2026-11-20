package inspector

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/demiurgos-hub/golem-engine/golem"
	"github.com/demiurgos-hub/golem-engine/golem/inspector/wire"
	"github.com/demiurgos-hub/golem-engine/golem/pb"
)

// testActor mimics a generated 2D entity: State is EntityUpdate tag 1, Delta
// tag 2, dirty bits x=0 y=1 hp=2 gold=3 (owner-only) tags=4.
type testActor struct {
	mu       sync.Mutex
	id       int64
	x, y     float32
	hp       int32
	gold     uint32
	tags     []string
	dirty    uint64
	lastMask uint64
	rev      uint64
}

func testSchema() *wire.Schema {
	return &wire.Schema{Dimensions: 2, RemovedTag: 3, Entities: []wire.Entity{{
		Name: "Actor", StateTag: 1, DeltaTag: 2,
		Fields: []wire.Field{
			{Name: "entity_id", Tag: 1, Bit: -1, Type: "int64", Role: wire.RoleIdentity},
			{Name: "pos_x", Tag: 2, Bit: 0, Type: "float", Role: wire.RolePosition},
			{Name: "pos_y", Tag: 3, Bit: 1, Type: "float", Role: wire.RolePosition},
			{Name: "revision", Tag: 1000, Bit: -1, Type: "uint64", Role: wire.RoleRevision},
			{Name: "hp", Tag: 4, Bit: 2, Sync: "tick", Type: "int32", Role: wire.RoleVariable},
			{Name: "gold", Tag: 5, Bit: 3, Sync: "tick", OwnerOnly: true, Type: "uint32", Role: wire.RoleVariable},
			{Name: "tags", Tag: 6, Bit: 4, Sync: "tick", Kind: wire.KindList, Type: "string", Role: wire.RoleVariable},
		},
	}}}
}

func (e *testActor) EntityID() int64  { return e.id }
func (e *testActor) TypeName() string { return "Actor" }
func (e *testActor) IsGlobal() bool   { return false }
func (e *testActor) Position() (float32, float32) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.x, e.y
}
func (e *testActor) StateRevision() uint64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.rev
}
func (e *testActor) LastFlushMask() uint64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.lastMask
}

func (e *testActor) set(fn func(*testActor) uint64) {
	e.mu.Lock()
	e.dirty |= fn(e)
	e.mu.Unlock()
}

func wrapUpdate(tag int, body []byte) []byte {
	w := &pb.Writer{}
	w.Tag(tag, 2).Bytes(body)
	return w.Finish()
}

func (e *testActor) encodeLocked(mask uint64, delta bool) []byte {
	w := &pb.Writer{}
	w.Tag(1, 0).Int64(e.id)
	all := !delta
	if all || mask&1 != 0 {
		w.Tag(2, 5).Float32(e.x)
	}
	if all || mask&2 != 0 {
		w.Tag(3, 5).Float32(e.y)
	}
	if all || mask&4 != 0 {
		w.Tag(4, 0).Int32(e.hp)
	}
	if all || mask&8 != 0 {
		w.Tag(5, 0).Uint32(e.gold)
	}
	if all || mask&16 != 0 {
		if delta && len(e.tags) == 0 {
			w.Tag(6, 2).Bytes(nil)
		}
		for _, t := range e.tags {
			inner := &pb.Writer{}
			inner.String(t)
			w.Tag(6, 2).Bytes(inner.Finish())
		}
	}
	w.Tag(1000, 0).Uint64(e.rev)
	return w.Finish()
}

func (e *testActor) FullUpdate() ([]byte, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return wrapUpdate(1, e.encodeLocked(0, false)), nil
}

func (e *testActor) FlushUpdate() ([]byte, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.lastMask = e.dirty
	if e.dirty == 0 {
		return nil, nil
	}
	e.dirty = 0
	e.rev++
	return wrapUpdate(2, e.encodeLocked(e.lastMask, true)), nil
}

func (e *testActor) MarshalDeltaMask(mask uint64) ([]byte, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return wrapUpdate(2, e.encodeLocked(mask, true)), nil
}

type fakeHub struct {
	t      *testing.T
	server *httptest.Server
	token  string
	conns  chan *websocket.Conn
	msgs   chan wire.Message
}

func newFakeHub(t *testing.T) *fakeHub {
	h := &fakeHub{t: t, token: "test-token", conns: make(chan *websocket.Conn, 4), msgs: make(chan wire.Message, 1024)}
	h.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+h.token {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		conn.SetReadLimit(8 << 20)
		h.conns <- conn
		for {
			_, data, err := conn.Read(context.Background())
			if err != nil {
				return
			}
			msg, err := wire.Decode(data)
			if err != nil {
				t.Errorf("hub decode: %v", err)
				return
			}
			if _, ok := msg.(*wire.Ping); ok {
				continue
			}
			h.msgs <- msg
		}
	}))
	t.Cleanup(h.server.Close)
	return h
}

func (h *fakeHub) url() string { return "ws" + strings.TrimPrefix(h.server.URL, "http") + "/ws/agent" }

func (h *fakeHub) conn() *websocket.Conn {
	h.t.Helper()
	select {
	case c := <-h.conns:
		return c
	case <-time.After(5 * time.Second):
		h.t.Fatal("agent did not connect")
		return nil
	}
}

func (h *fakeHub) send(c *websocket.Conn, msg wire.Message) {
	h.t.Helper()
	if err := c.Write(context.Background(), websocket.MessageBinary, wire.Encode(msg)); err != nil {
		h.t.Fatal(err)
	}
}

func next[T wire.Message](t *testing.T, h *fakeHub) T {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case msg := <-h.msgs:
			if m, ok := msg.(T); ok {
				return m
			}
			if _, ok := msg.(*wire.ObserveStatus); ok {
				continue // status may interleave with data
			}
			var zero T
			t.Fatalf("got %T, want %T", msg, zero)
		case <-deadline:
			var zero T
			t.Fatalf("timed out waiting for %T", zero)
		}
	}
}

// waitFor skips messages until one of type T arrives.
func waitFor[T wire.Message](t *testing.T, h *fakeHub) T {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case msg := <-h.msgs:
			if m, ok := msg.(T); ok {
				return m
			}
		case <-deadline:
			var zero T
			t.Fatalf("timed out waiting for %T", zero)
		}
	}
}

func startServer(t *testing.T, actors ...*testActor) *golem.Server {
	t.Helper()
	srv := golem.NewServer(golem.ServerConfig{TickRate: 200})
	srv.SetRemovalSerializer(func(id int64, rev uint64) ([]byte, error) { return wrapUpdate(3, nil), nil })
	for _, a := range actors {
		if err := srv.CreateEntity(a); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = srv.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return srv
}

func quietLogger() *log.Logger { return log.New(io.Discard, "", 0) }

func attach(t *testing.T, srv *golem.Server, h *fakeHub, mutate func(*Options)) *Agent {
	t.Helper()
	opts := Options{HubURL: h.url(), Token: h.token, TargetID: "cave-1", Labels: map[string]string{"map": "caves"}, RateHz: 1000, Logger: quietLogger(), MinBackoff: 10 * time.Millisecond, MaxBackoff: 50 * time.Millisecond}
	if mutate != nil {
		mutate(&opts)
	}
	a, err := Attach(context.Background(), srv, testSchema(), opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	return a
}

func post(t *testing.T, srv *golem.Server, fn func(*golem.Server)) {
	t.Helper()
	done := make(chan struct{})
	if err := srv.Post(func(s *golem.Server) { fn(s); close(done) }); err != nil {
		t.Fatal(err)
	}
	<-done
}

func TestAttachDisabledWithoutHubURL(t *testing.T) {
	a, err := Attach(context.Background(), golem.NewServer(golem.ServerConfig{}), testSchema(), Options{})
	if a != nil || err != nil {
		t.Fatalf("Attach without HubURL = %v, %v", a, err)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	for _, opts := range []Options{
		{HubURL: "ws://hub/ws/agent"},
		{HubURL: "ftp://hub", Token: "x"},
		{HubURL: "ws://hub/ws/agent?token=x", Token: "x"},
	} {
		if _, err := Attach(context.Background(), golem.NewServer(golem.ServerConfig{}), testSchema(), opts); err == nil {
			t.Errorf("Attach(%+v) accepted invalid options", opts)
		}
	}
}

func TestAgentStreamsSnapshotDeltasAndRemovals(t *testing.T) {
	hero := &testActor{id: 1, x: 1, y: 2, hp: 10, gold: 50, tags: []string{"a"}}
	rock := &testActor{id: 2, x: 5}
	srv := startServer(t, hero, rock)
	hub := newFakeHub(t)
	attach(t, srv, hub, nil)
	conn := hub.conn()

	reg := next[*wire.Register](t, hub)
	if reg.TargetID != "cave-1" || reg.Labels["map"] != "caves" || reg.Schema == nil || len(reg.Schema.Entities) != 1 {
		t.Fatalf("register = %+v", reg)
	}
	select {
	case msg := <-hub.msgs:
		t.Fatalf("agent streamed %T before observe.start", msg)
	case <-time.After(50 * time.Millisecond):
	}

	hub.send(conn, &wire.ObserveStart{})
	begin := next[*wire.SnapshotBegin](t, hub)
	if begin.Epoch != 1 || begin.Seq != 1 || begin.EntityCount != 2 {
		t.Fatalf("begin = %+v", begin)
	}
	chunk := next[*wire.SnapshotChunk](t, hub)
	if len(chunk.Entities) != 2 || chunk.Entities[0].ID != 1 || chunk.Entities[0].TypeTag != 1 || chunk.Seq != 2 {
		t.Fatalf("chunk = %+v", chunk)
	}
	store := map[int64]*wire.EntityFields{}
	for _, e := range chunk.Entities {
		f, err := wire.ParseState(e.TypeTag, e.State)
		if err != nil {
			t.Fatal(err)
		}
		store[e.ID] = f
	}
	if !bytes.Contains(store[1].EncodeState(), []byte{5 << 3, 50}) {
		t.Fatal("owner-only gold missing from snapshot")
	}
	if end := next[*wire.SnapshotEnd](t, hub); end.Seq != 3 {
		t.Fatalf("end = %+v", end)
	}

	// Change hp and clear tags across two ticks; one coalesced delta must
	// carry both bits.
	post(t, srv, func(*golem.Server) { hero.set(func(e *testActor) uint64 { e.hp = 7; return 4 }) })
	post(t, srv, func(*golem.Server) { hero.set(func(e *testActor) uint64 { e.tags = nil; return 16 }) })
	var mask uint64
	seq := uint64(3)
	for mask != 4|16 {
		b := next[*wire.Batch](t, hub)
		if b.Seq != seq+1 || b.Epoch != 1 {
			t.Fatalf("batch seq/epoch = %d/%d, want %d/1", b.Seq, b.Epoch, seq+1)
		}
		seq = b.Seq
		for _, d := range b.Deltas {
			if d.ID != 1 || d.TypeTag != 1 {
				t.Fatalf("delta = %+v", d)
			}
			mask |= d.Mask
			if err := store[1].ApplyDelta(&reg.Schema.Entities[0], d.Mask, d.Delta); err != nil {
				t.Fatal(err)
			}
		}
	}
	merged := store[1].EncodeState()
	if !bytes.Contains(merged, []byte{4 << 3, 7}) {
		t.Fatalf("hp not merged: %v", merged)
	}

	// Spawn then remove inside one window cancels out; removing rock reports it.
	post(t, srv, func(s *golem.Server) {
		ghost := &testActor{id: 9}
		if err := s.CreateEntity(ghost); err != nil {
			t.Error(err)
		}
	})
	post(t, srv, func(s *golem.Server) { s.DeleteEntity(9); s.DeleteEntity(2) })
	post(t, srv, func(s *golem.Server) {
		if err := s.CreateEntity(&testActor{id: 3, x: 9}); err != nil {
			t.Error(err)
		}
	})
	removed, spawned := false, false
	for !removed || !spawned {
		b := next[*wire.Batch](t, hub)
		for _, r := range b.Removals {
			if r.ID == 9 {
				// Spawn+remove may straddle a flush boundary; then both are reported.
				continue
			}
			removed = removed || r.ID == 2
		}
		for _, s := range b.Spawns {
			spawned = spawned || s.ID == 3
		}
	}

	hub.send(conn, &wire.ObserveStop{})
	deadline := time.After(5 * time.Second)
	for {
		select {
		case msg := <-hub.msgs:
			if st, ok := msg.(*wire.ObserveStatus); ok && st.State == wire.ObserveIdle {
				goto stopped
			}
		case <-deadline:
			t.Fatal("no idle status after observe.stop")
		}
	}
stopped:
	post(t, srv, func(*golem.Server) { hero.set(func(e *testActor) uint64 { e.hp = 1; return 4 }) })
	select {
	case msg := <-hub.msgs:
		if _, ok := msg.(*wire.Batch); ok {
			t.Fatal("batch sent after observe.stop")
		}
	case <-time.After(100 * time.Millisecond):
	}

	// A new observation starts a new epoch with a fresh snapshot.
	hub.send(conn, &wire.ObserveStart{})
	if begin := next[*wire.SnapshotBegin](t, hub); begin.Epoch != 2 || begin.Seq != 1 {
		t.Fatalf("second begin = %+v", begin)
	}
}

func TestAgentAuthFailureIsTerminal(t *testing.T) {
	srv := startServer(t)
	hub := newFakeHub(t)
	a := attach(t, srv, hub, func(o *Options) { o.Token = "wrong" })
	select {
	case <-a.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("agent kept retrying after 401")
	}
	if !errors.Is(a.Err(), errTerminal) {
		t.Fatalf("Err() = %v", a.Err())
	}
}

func TestAgentReconnectsAndResnapshots(t *testing.T) {
	srv := startServer(t, &testActor{id: 1})
	hub := newFakeHub(t)
	attach(t, srv, hub, nil)
	conn := hub.conn()
	next[*wire.Register](t, hub)
	hub.send(conn, &wire.ObserveStart{})
	waitFor[*wire.SnapshotEnd](t, hub)
	conn.Close(websocket.StatusGoingAway, "hub restart")

	conn = hub.conn()
	reg := next[*wire.Register](t, hub)
	if reg.Epoch != 1 {
		t.Fatalf("re-register epoch = %d, want 1", reg.Epoch)
	}
	hub.send(conn, &wire.ObserveStart{})
	if begin := next[*wire.SnapshotBegin](t, hub); begin.Epoch != 2 {
		t.Fatalf("begin after reconnect = %+v", begin)
	}
}

func TestAgentOverflowRestartsWithNewEpoch(t *testing.T) {
	actors := make([]*testActor, 40)
	for i := range actors {
		actors[i] = &testActor{id: int64(i + 1), tags: []string{strings.Repeat("x", 64)}}
	}
	srv := startServer(t, actors...)
	hub := newFakeHub(t)
	// A tiny queue and frame size: a multi-chunk snapshot must still complete
	// because it occupies a single queue item.
	attach(t, srv, hub, func(o *Options) { o.QueueFrames = 1; o.MaxFrameBytes = 64 })
	conn := hub.conn()
	next[*wire.Register](t, hub)
	hub.send(conn, &wire.ObserveStart{})
	// The stream must eventually complete a snapshot on some epoch > 1 or on
	// epoch 1 if the writer kept up; either way chunks never skip a seq.
	var epoch, seq uint64
	deadline := time.After(10 * time.Second)
	for {
		var msg wire.Message
		select {
		case msg = <-hub.msgs:
		case <-deadline:
			t.Fatal("snapshot never completed")
		}
		switch m := msg.(type) {
		case *wire.SnapshotBegin:
			epoch, seq = m.Epoch, m.Seq
		case *wire.SnapshotChunk:
			if m.Epoch == epoch {
				if m.Seq != seq+1 {
					t.Fatalf("seq gap within epoch %d: %d after %d", epoch, m.Seq, seq)
				}
				seq = m.Seq
			}
		case *wire.SnapshotEnd:
			if m.Epoch == epoch && m.Seq == seq+1 {
				return
			}
		}
	}
}

func TestStreamOverflowCoolsDownThenResnapshots(t *testing.T) {
	srv := golem.NewServer(golem.ServerConfig{})
	hero := &testActor{id: 1}
	if err := srv.CreateEntity(hero); err != nil {
		t.Fatal(err)
	}
	a := &Agent{srv: srv, schema: testSchema(), opts: Options{RateHz: 1e12, Logger: quietLogger()}.withDefaults(), out: make(chan [][]byte, 1)}
	a.stream.agent = a
	a.observing.Store(true)

	beginEpoch := func(item [][]byte) uint64 {
		t.Helper()
		msg, err := wire.Decode(item[0])
		if err != nil {
			t.Fatal(err)
		}
		b, ok := msg.(*wire.SnapshotBegin)
		if !ok {
			t.Fatalf("first frame %T, want SnapshotBegin", msg)
		}
		return b.Epoch
	}

	a.stream.onFrame(golem.ReplicationFrame{Tick: 1}) // snapshot fills the queue
	hero.lastMask = 4
	a.stream.onFrame(golem.ReplicationFrame{Tick: 2, Changed: []int64{1}}) // batch overflows
	if a.stream.live || !a.stream.cooldown {
		t.Fatalf("after overflow live=%v cooldown=%v", a.stream.live, a.stream.cooldown)
	}
	a.stream.onFrame(golem.ReplicationFrame{Tick: 3}) // queue still full: wait
	if len(a.out) != 1 {
		t.Fatalf("queue len %d during cooldown", len(a.out))
	}
	if e := beginEpoch(<-a.out); e != 1 {
		t.Fatalf("first snapshot epoch %d", e)
	}
	a.stream.onFrame(golem.ReplicationFrame{Tick: 4}) // drained: resnapshot
	if e := beginEpoch(<-a.out); e != 2 || !a.stream.live {
		t.Fatalf("resnapshot epoch %d live %v", e, a.stream.live)
	}
}

func TestAgentStreamsSpaces(t *testing.T) {
	srv := startServer(t, &testActor{id: 1}, &testActor{id: 2})
	hub := newFakeHub(t)
	attach(t, srv, hub, nil)
	conn := hub.conn()
	next[*wire.Register](t, hub)
	post(t, srv, func(s *golem.Server) {
		if err := s.SetEntitySpace(2, 3); err != nil {
			t.Error(err)
		}
	})

	hub.send(conn, &wire.ObserveStart{})
	chunk := waitFor[*wire.SnapshotChunk](t, hub)
	if len(chunk.Entities) != 2 || chunk.Entities[0].Space != 0 || chunk.Entities[1].Space != 3 {
		t.Fatalf("snapshot spaces = %+v", chunk.Entities)
	}
	waitFor[*wire.SnapshotEnd](t, hub)

	post(t, srv, func(s *golem.Server) {
		if err := s.SetEntitySpace(1, 5); err != nil {
			t.Error(err)
		}
		if err := s.CreateEntityIn(4, &testActor{id: 7}); err != nil {
			t.Error(err)
		}
	})
	moved, spawned := false, false
	for !moved || !spawned {
		b := next[*wire.Batch](t, hub)
		for _, m := range b.Moves {
			if m != (wire.SpaceMove{ID: 1, Space: 5}) {
				t.Fatalf("unexpected move %+v", m)
			}
			moved = true
		}
		for _, st := range b.Spawns {
			if st.ID == 7 {
				if st.Space != 4 {
					t.Fatalf("spawn space = %d, want 4", st.Space)
				}
				spawned = true
			}
		}
	}
}

// TestStreamCoalescesMoves drives the stream directly: moves coalesce to the
// latest space per window, and spawned or removed entities drop theirs.
func TestStreamCoalescesMoves(t *testing.T) {
	srv := golem.NewServer(golem.ServerConfig{})
	for id := int64(1); id <= 3; id++ {
		if err := srv.CreateEntity(&testActor{id: id}); err != nil {
			t.Fatal(err)
		}
	}
	a := &Agent{srv: srv, schema: testSchema(), opts: Options{RateHz: 1, Logger: quietLogger()}.withDefaults(), out: make(chan [][]byte, 4)}
	a.stream.agent = a
	a.observing.Store(true)

	a.stream.onFrame(golem.ReplicationFrame{Tick: 1}) // snapshot
	<-a.out
	a.stream.onFrame(golem.ReplicationFrame{Tick: 2, Moved: []golem.EntitySpaceMove{{ID: 1, From: 0, To: 2}, {ID: 4, From: 0, To: 1}}})
	a.stream.onFrame(golem.ReplicationFrame{Tick: 3, Moved: []golem.EntitySpaceMove{{ID: 1, From: 2, To: 4}}, Spawned: []int64{3}})
	a.stream.onFrame(golem.ReplicationFrame{Tick: 4, Moved: []golem.EntitySpaceMove{{ID: 3, From: 0, To: 5}}, Removed: []golem.RemovedEntity{{ID: 4, Revision: 1}}})
	if len(a.out) != 0 {
		t.Fatal("stream flushed before its rate window elapsed")
	}
	a.stream.lastFlush = time.Time{}
	a.stream.onFrame(golem.ReplicationFrame{Tick: 5})

	item := <-a.out
	if len(item) != 1 {
		t.Fatalf("flush produced %d frames", len(item))
	}
	msg, err := wire.Decode(item[0])
	if err != nil {
		t.Fatal(err)
	}
	b := msg.(*wire.Batch)
	if len(b.Moves) != 1 || b.Moves[0] != (wire.SpaceMove{ID: 1, Space: 4}) {
		t.Fatalf("moves = %+v, want only entity 1 at its latest space", b.Moves)
	}
	if len(b.Spawns) != 1 || b.Spawns[0].ID != 3 || len(b.Removals) != 1 || b.Removals[0].ID != 4 {
		t.Fatalf("spawns = %+v removals = %+v", b.Spawns, b.Removals)
	}
}
