package golem

import (
	"bytes"
	"context"
	"errors"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	golemnet "github.com/demiurgos-hub/golem-engine/golem/net"
	"github.com/demiurgos-hub/golem-engine/golem/pb"
)

type sessionWorldData struct {
	name    string
	payload []byte
	err     error
}

func (d *sessionWorldData) WorldName() string { return d.name }
func (d *sessionWorldData) MarshalUpdate() ([]byte, error) {
	if d.err != nil {
		return nil, d.err
	}
	return append([]byte(nil), d.payload...), nil
}

type worldOrderEntity struct {
	id   int64
	full []byte
}

func (e *worldOrderEntity) EntityID() int64              { return e.id }
func (e *worldOrderEntity) TypeName() string             { return "world-order" }
func (e *worldOrderEntity) Position() (float32, float32) { return 0, 0 }
func (e *worldOrderEntity) IsGlobal() bool               { return false }
func (e *worldOrderEntity) FlushUpdate() ([]byte, error) { return nil, nil }
func (e *worldOrderEntity) FullUpdate() ([]byte, error)  { return e.full, nil }

func decodeServerMessageFields(t *testing.T, batch []byte) (worlds, entities [][]byte) {
	t.Helper()
	rd := pb.NewReader(batch)
	for !rd.Done() {
		field, wire := rd.Tag()
		if wire != 2 {
			t.Fatalf("unexpected wire type %d", wire)
		}
		payload := rd.Bytes()
		switch field {
		case 1:
			entities = append(entities, payload)
		case 2:
			worlds = append(worlds, payload)
		default:
			t.Fatalf("unexpected server message field %d", field)
		}
	}
	return worlds, entities
}

func readServerMessagesUntil(t *testing.T, conn *websocket.Conn, wantWorlds, wantEntities int) (worlds, entities [][]byte) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		_, msg, err := conn.Read(ctx)
		cancel()
		if err != nil {
			continue
		}
		w, e := decodeServerMessageFields(t, msg)
		worlds = append(worlds, w...)
		entities = append(entities, e...)
		if len(worlds) >= wantWorlds && len(entities) >= wantEntities {
			return worlds, entities
		}
	}
	t.Fatalf("timed out waiting for worlds=%d entities=%d (got %d/%d)", wantWorlds, wantEntities, len(worlds), len(entities))
	return nil, nil
}

func TestSendWorldDataDistinctValuesSameName(t *testing.T) {
	srv := NewServer(ServerConfig{
		Transport:       golemnet.TransportWebSocket,
		StateUpdateLane: StateUpdateLaneStream,
	})
	srv.World.Set(&sessionWorldData{name: "zone", payload: []byte("stored-global")})

	httpSrv := httptest.NewServer(srv.Handler())
	defer httpSrv.Close()

	c1 := mustDialGameClient(t, "ws"+httpSrv.URL[4:])
	defer c1.CloseNow()
	c2 := mustDialGameClient(t, "ws"+httpSrv.URL[4:])
	defer c2.CloseNow()
	ids := waitForSessionIDs(t, srv, 2)

	// Drain connect snapshots (stored global for each client).
	snap1, _ := readServerMessagesUntil(t, c1, 1, 0)
	snap2, _ := readServerMessagesUntil(t, c2, 1, 0)
	if !bytes.Equal(snap1[0], []byte("stored-global")) || !bytes.Equal(snap2[0], []byte("stored-global")) {
		t.Fatalf("connect snapshots = %q / %q, want stored-global", snap1[0], snap2[0])
	}

	v1 := &sessionWorldData{name: "zone", payload: []byte("map-for-session-1")}
	v2 := &sessionWorldData{name: "zone", payload: []byte("map-for-session-2")}
	if err := srv.SendWorldData(ids[0], v1); err != nil {
		t.Fatalf("SendWorldData session1: %v", err)
	}
	if err := srv.SendWorldData(ids[1], v2); err != nil {
		t.Fatalf("SendWorldData session2: %v", err)
	}

	got1, _ := readServerMessagesUntil(t, c1, 1, 0)
	got2, _ := readServerMessagesUntil(t, c2, 1, 0)
	if !bytes.Equal(got1[0], []byte("map-for-session-1")) {
		t.Fatalf("session1 got %q, want map-for-session-1", got1[0])
	}
	if !bytes.Equal(got2[0], []byte("map-for-session-2")) {
		t.Fatalf("session2 got %q, want map-for-session-2", got2[0])
	}

	stored := srv.World.Get("zone")
	payload, err := stored.MarshalUpdate()
	if err != nil {
		t.Fatalf("stored MarshalUpdate: %v", err)
	}
	if !bytes.Equal(payload, []byte("stored-global")) {
		t.Fatalf("Server.World mutated: got %q, want stored-global", payload)
	}
}

func TestWorldSnapshotExcludeOmitsNamesKeepsOrderAndCallerCopy(t *testing.T) {
	exclude := []string{"secret", "", "secret"}
	srv := NewServer(ServerConfig{
		Transport:            golemnet.TransportWebSocket,
		StateUpdateLane:      StateUpdateLaneStream,
		WorldSnapshotExclude: exclude,
	})
	exclude[0] = "alpha" // caller mutation must not affect server config

	srv.World.Set(&sessionWorldData{name: "zeta", payload: []byte("zeta-data")})
	srv.World.Set(&sessionWorldData{name: "alpha", payload: []byte("alpha-data")})
	srv.World.Set(&sessionWorldData{name: "secret", payload: []byte("secret-data")})
	if err := srv.CreateEntity(&worldOrderEntity{id: 9, full: []byte("entity-full")}); err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}

	httpSrv := httptest.NewServer(srv.Handler())
	defer httpSrv.Close()
	client := mustDialGameClient(t, "ws"+httpSrv.URL[4:])
	defer client.CloseNow()
	_ = waitForSessionIDs(t, srv, 1)

	worlds, entities := readServerMessagesUntil(t, client, 2, 1)
	if len(worlds) != 2 {
		t.Fatalf("world frames = %d, want 2 (%v)", len(worlds), worlds)
	}
	if !bytes.Equal(worlds[0], []byte("alpha-data")) || !bytes.Equal(worlds[1], []byte("zeta-data")) {
		t.Fatalf("world order/payloads = %q, %q", worlds[0], worlds[1])
	}
	for _, w := range worlds {
		if bytes.Equal(w, []byte("secret-data")) {
			t.Fatal("excluded secret world appeared in connect snapshot")
		}
	}
	if len(entities) != 1 || !bytes.Equal(entities[0], []byte("entity-full")) {
		t.Fatalf("entities = %v, want [entity-full]", entities)
	}
	if got := srv.World.Get("secret"); got == nil {
		t.Fatal("excluded name must remain in Server.World")
	}
	if len(srv.config.WorldSnapshotExclude) != 1 || srv.config.WorldSnapshotExclude[0] != "secret" {
		t.Fatalf("normalized exclude = %v, want [secret]", srv.config.WorldSnapshotExclude)
	}
}

func TestWorldSnapshotWorldBeforeEntity(t *testing.T) {
	srv := NewServer(ServerConfig{
		Transport:       golemnet.TransportWebSocket,
		StateUpdateLane: StateUpdateLaneStream,
	})
	srv.World.Set(&sessionWorldData{name: "zone", payload: []byte("world-first")})
	if err := srv.CreateEntity(&worldOrderEntity{id: 1, full: []byte("entity-second")}); err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}

	httpSrv := httptest.NewServer(srv.Handler())
	defer httpSrv.Close()
	client := mustDialGameClient(t, "ws"+httpSrv.URL[4:])
	defer client.CloseNow()
	_ = waitForSessionIDs(t, srv, 1)

	deadline := time.Now().Add(2 * time.Second)
	var sawWorld, sawEntity bool
	for time.Now().Before(deadline) && !(sawWorld && sawEntity) {
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		_, msg, err := client.Read(ctx)
		cancel()
		if err != nil {
			continue
		}
		rd := pb.NewReader(msg)
		for !rd.Done() {
			field, wire := rd.Tag()
			if wire != 2 {
				t.Fatalf("unexpected wire %d", wire)
			}
			payload := rd.Bytes()
			switch field {
			case 2:
				if sawEntity {
					t.Fatal("entity frame arrived before world frame")
				}
				if !bytes.Equal(payload, []byte("world-first")) {
					t.Fatalf("world payload = %q", payload)
				}
				sawWorld = true
			case 1:
				if !sawWorld {
					t.Fatal("entity frame arrived without prior world frame")
				}
				if !bytes.Equal(payload, []byte("entity-second")) {
					t.Fatalf("entity payload = %q", payload)
				}
				sawEntity = true
			default:
				t.Fatalf("unexpected field %d", field)
			}
		}
	}
	if !sawWorld || !sawEntity {
		t.Fatalf("sawWorld=%v sawEntity=%v", sawWorld, sawEntity)
	}
}

func TestSendWorldDataRejectsNilAndTypedNil(t *testing.T) {
	srv := NewServer(ServerConfig{})
	if err := srv.SendWorldData(1, nil); err == nil {
		t.Fatal("nil data returned nil error")
	}
	var typed *sessionWorldData
	if err := srv.SendWorldData(1, typed); err == nil {
		t.Fatal("typed-nil data returned nil error")
	}
}

func TestSendStoredWorldDataMissingNameNoop(t *testing.T) {
	srv := NewServer(ServerConfig{})
	if err := srv.SendStoredWorldData(1, "missing"); err != nil {
		t.Fatalf("missing name: %v", err)
	}
}

func TestSendWorldDataDisconnectedSession(t *testing.T) {
	srv := NewServer(ServerConfig{})
	err := srv.SendWorldData(42, &sessionWorldData{name: "zone", payload: []byte("x")})
	if !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("err = %v, want ErrSessionNotFound", err)
	}
}

func TestSendStoredWorldDataDisconnectedSession(t *testing.T) {
	srv := NewServer(ServerConfig{})
	srv.World.Set(&sessionWorldData{name: "zone", payload: []byte("x")})
	err := srv.SendStoredWorldData(42, "zone")
	if !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("err = %v, want ErrSessionNotFound", err)
	}
}

func TestSendWorldDataPropagatesMarshalError(t *testing.T) {
	srv := NewServer(ServerConfig{})
	boom := errors.New("marshal failed")
	err := srv.SendWorldData(1, &sessionWorldData{name: "zone", err: boom})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want %v", err, boom)
	}
}

func TestSpaceWorldStorePersistsAcrossHandles(t *testing.T) {
	srv := NewServer(ServerConfig{})
	if srv.spaceWorld(3) != nil || srv.spaceWorldsUsed.Load() {
		t.Fatal("a per-space store exists before first use")
	}
	stores := make([]any, 16)
	var wg sync.WaitGroup
	for i := range stores {
		wg.Add(1)
		go func() {
			defer wg.Done()
			stores[i] = srv.Space(3).World()
		}()
	}
	wg.Wait()
	cave := srv.Space(3).World()
	for _, store := range stores {
		if store != any(cave) {
			t.Fatal("handles for one space returned different stores")
		}
	}
	cave.Set(&sessionWorldData{name: "fog", payload: []byte("cave")})
	if srv.Space(3).World().Get("fog") == nil {
		t.Fatal("value set through one handle is missing from another")
	}
	if srv.Space(4).World() == cave || srv.Space(0).World() == cave || srv.Space(0).World() == srv.World {
		t.Fatal("spaces share a store, or space 0 aliases Server.World")
	}
	if srv.World.Get("fog") != nil {
		t.Fatal("a per-space value leaked into Server.World")
	}
}

func TestSpaceWorldUnusedKeepsConnectSnapshotByteIdentical(t *testing.T) {
	build := func(seed func(*Server)) [][]byte {
		srv := NewServer(ServerConfig{WorldSnapshotExclude: []string{"secret"}})
		srv.World.Set(&sessionWorldData{name: "zeta", payload: []byte("z")})
		srv.World.Set(&sessionWorldData{name: "alpha", payload: []byte("a")})
		srv.World.Set(&sessionWorldData{name: "secret", payload: []byte("s")})
		if seed != nil {
			seed(srv)
		}
		frames, err := srv.worldSnapshotFrames()
		if err != nil {
			t.Fatal(err)
		}
		return frames
	}
	want := [][]byte{WrapWorldUpdate([]byte("a")), WrapWorldUpdate([]byte("z"))}
	for name, seed := range map[string]func(*Server){
		"unused":      nil,
		"empty store": func(srv *Server) { srv.Space(0).World(); srv.Space(5).World() },
	} {
		got := build(seed)
		if !slices.EqualFunc(got, want, bytes.Equal) {
			t.Fatalf("%s: snapshot = %q, want %q", name, got, want)
		}
	}
}

func TestSpaceWorldConnectSnapshotMergesSpaceZero(t *testing.T) {
	srv := NewServer(ServerConfig{WorldSnapshotExclude: []string{"secret"}})
	srv.World.Set(&sessionWorldData{name: "alpha", payload: []byte("global-alpha")})
	srv.World.Set(&sessionWorldData{name: "zone", payload: []byte("global-zone")})
	srv.World.Set(&sessionWorldData{name: "secret", payload: []byte("global-secret")})
	srv.Space(0).World().Set(&sessionWorldData{name: "zone", payload: []byte("space0-zone")})
	srv.Space(0).World().Set(&sessionWorldData{name: "beta", payload: []byte("space0-beta")})
	srv.Space(0).World().Set(&sessionWorldData{name: "secret", payload: []byte("space0-secret")})
	srv.Space(5).World().Set(&sessionWorldData{name: "alpha", payload: []byte("cave-alpha")})
	srv.Space(5).World().Set(&sessionWorldData{name: "cave", payload: []byte("cave-only")})
	frames, err := srv.worldSnapshotFrames()
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, frame := range frames {
		got = append(got, decodeServerTokens(t, frame)...)
	}
	want := []string{"world:global-alpha", "world:space0-beta", "cleared:cave", "world:space0-zone"}
	if !slices.Equal(got, want) {
		t.Fatalf("snapshot = %q, want %q", got, want)
	}

	boom := errors.New("marshal failed")
	srv.Space(0).World().Set(&sessionWorldData{name: "beta", err: boom})
	if _, err := srv.worldSnapshotFrames(); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want %v", err, boom)
	}
}

func TestSpacePushWorldDataTargetsAnnouncedSessions(t *testing.T) {
	rig := newSpaceTestRig(t, false, 2)
	srv := rig.srv
	inCave := rig.ids[1]
	caveConn, outsideConn := rig.conns[1], rig.conns[0]
	srv.SetSessionSpace(inCave, 4)
	rig.tick(t)
	assertTransition(t, readSpaceTokens(t, caveConn), 4, 1, nil, nil, nil)

	// Missing store or name: no-op.
	if err := srv.Space(4).PushWorldData("weather"); err != nil {
		t.Fatal(err)
	}
	srv.Space(4).World().Set(&sessionWorldData{name: "weather", payload: []byte("storm")})
	if err := srv.Space(4).PushWorldData("missing"); err != nil {
		t.Fatal(err)
	}
	if err := srv.Space(4).PushWorldData("weather"); err != nil {
		t.Fatal(err)
	}
	if got := readSpaceTokens(t, caveConn); !slices.Equal(got, []string{"world:storm"}) {
		t.Fatalf("cave session = %q", got)
	}
	expectNoMessage(t, outsideConn)
	// A space with a store but no session sends nothing.
	srv.Space(7).World().Set(&sessionWorldData{name: "weather", payload: []byte("calm")})
	if err := srv.Space(7).PushWorldData("weather"); err != nil {
		t.Fatal(err)
	}
	expectNoMessage(t, caveConn)
	expectNoMessage(t, outsideConn)

	boom := errors.New("marshal failed")
	srv.Space(4).World().Set(&sessionWorldData{name: "broken", err: boom})
	if err := srv.Space(4).PushWorldData("broken"); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want %v", err, boom)
	}
	srv.Space(4).World().Set(&sessionWorldData{name: "huge", payload: make([]byte, 300<<10)})
	if err := srv.Space(4).PushWorldData("huge"); err == nil {
		t.Fatal("oversized frame returned nil")
	}
}

func TestPushWorldDataSkipsSessionsWhoseSpaceOverrides(t *testing.T) {
	rig := newSpaceTestRig(t, false, 2)
	srv := rig.srv
	inCave, outside := rig.ids[1], rig.ids[0]
	caveConn, outsideConn := rig.conns[1], rig.conns[0]
	srv.World.Set(&sessionWorldData{name: "weather", payload: []byte("sunny")})
	srv.World.Set(&sessionWorldData{name: "zone", payload: []byte("zone")})
	srv.Space(4).World().Set(&sessionWorldData{name: "weather", payload: []byte("storm")})
	srv.SetSessionSpace(inCave, 4)
	rig.tick(t)
	readSpaceTokens(t, caveConn)

	if err := srv.PushWorldData("weather"); err != nil {
		t.Fatal(err)
	}
	if got := readSpaceTokens(t, outsideConn); !slices.Equal(got, []string{"world:sunny"}) {
		t.Fatalf("outside session = %q", got)
	}
	expectNoMessage(t, caveConn)

	// Names no space overrides still reach everyone.
	if err := srv.PushWorldData("zone"); err != nil {
		t.Fatal(err)
	}
	for _, c := range rig.conns {
		if got := readSpaceTokens(t, c); !slices.Equal(got, []string{"world:zone"}) {
			t.Fatalf("zone push = %q", got)
		}
	}

	// A space 0 override skips the space 0 session instead.
	srv.Space(0).World().Set(&sessionWorldData{name: "zone", payload: []byte("space0-zone")})
	if err := srv.PushWorldData("zone"); err != nil {
		t.Fatal(err)
	}
	if got := readSpaceTokens(t, caveConn); !slices.Equal(got, []string{"world:zone"}) {
		t.Fatalf("cave zone push = %q", got)
	}
	expectNoMessage(t, outsideConn)
	if err := srv.SendStoredWorldData(outside, "zone"); err != nil {
		t.Fatal(err)
	}
	if got := readSpaceTokens(t, outsideConn); !slices.Equal(got, []string{"world:space0-zone"}) {
		t.Fatalf("outside SendStoredWorldData = %q", got)
	}
}
