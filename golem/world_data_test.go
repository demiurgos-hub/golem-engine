package golem

import (
	"bytes"
	"context"
	"errors"
	"net/http/httptest"
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
