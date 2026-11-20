package inspector_test

import (
	"bytes"
	"context"
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
	"github.com/demiurgos-hub/golem-engine/golem/inspector"
	"github.com/demiurgos-hub/golem-engine/golem/inspector/hub"
	"github.com/demiurgos-hub/golem-engine/golem/inspector/wire"
	"github.com/demiurgos-hub/golem-engine/golem/pb"
)

// e2eActor is a minimal generated-style entity with one tick var (hp, bit 2).
type e2eActor struct {
	mu       sync.Mutex
	id       int64
	hp       int32
	dirty    uint64
	lastMask uint64
}

func (e *e2eActor) EntityID() int64              { return e.id }
func (e *e2eActor) TypeName() string             { return "Actor" }
func (e *e2eActor) Position() (float32, float32) { return 0, 0 }
func (e *e2eActor) IsGlobal() bool               { return false }
func (e *e2eActor) LastFlushMask() uint64        { e.mu.Lock(); defer e.mu.Unlock(); return e.lastMask }

func (e *e2eActor) encode(tag int, withHP bool) []byte {
	body := &pb.Writer{}
	body.Tag(1, 0).Int64(e.id)
	if withHP {
		body.Tag(4, 0).Int32(e.hp)
	}
	w := &pb.Writer{}
	w.Tag(tag, 2).Bytes(body.Finish())
	return w.Finish()
}

func (e *e2eActor) FullUpdate() ([]byte, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.encode(1, true), nil
}

func (e *e2eActor) FlushUpdate() ([]byte, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.lastMask, e.dirty = e.dirty, 0
	if e.lastMask == 0 {
		return nil, nil
	}
	return e.encode(2, true), nil
}

func (e *e2eActor) MarshalDeltaMask(mask uint64) ([]byte, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.encode(2, mask&4 != 0), nil
}

func e2eSchema() *wire.Schema {
	return &wire.Schema{Dimensions: 2, RemovedTag: 3, Entities: []wire.Entity{{Name: "Actor", StateTag: 1, DeltaTag: 2, Fields: []wire.Field{
		{Name: "entity_id", Tag: 1, Bit: -1, Type: "int64", Role: wire.RoleIdentity},
		{Name: "pos_x", Tag: 2, Bit: 0, Type: "float", Role: wire.RolePosition},
		{Name: "pos_y", Tag: 3, Bit: 1, Type: "float", Role: wire.RolePosition},
		{Name: "revision", Tag: 1000, Bit: -1, Type: "uint64", Role: wire.RoleRevision},
		{Name: "hp", Tag: 4, Bit: 2, Sync: "tick", Type: "int32", Role: wire.RoleVariable},
	}}}}
}

func TestEndToEndServerAgentHubBrowser(t *testing.T) {
	quiet := log.New(io.Discard, "", 0)
	h, err := hub.New(hub.Config{AgentToken: "e2e-token", ObserveGrace: time.Second, Logger: quiet})
	if err != nil {
		t.Fatal(err)
	}
	hubServer := httptest.NewServer(h.Handler())
	defer hubServer.Close()
	defer h.Close(context.Background())
	wsBase := "ws" + strings.TrimPrefix(hubServer.URL, "http")

	srv := golem.NewServer(golem.ServerConfig{TickRate: 100})
	srv.SetRemovalSerializer(func(int64, uint64) ([]byte, error) { return []byte{0x1a, 0}, nil })
	hero := &e2eActor{id: 1, hp: 10}
	if err := srv.CreateEntity(hero); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.Run(ctx)

	agent, err := inspector.Attach(ctx, srv, e2eSchema(), inspector.Options{HubURL: wsBase + "/ws/agent", Token: "e2e-token", TargetID: "e2e", Labels: map[string]string{"map": "test"}, Logger: quiet, RateHz: 50})
	if err != nil {
		t.Fatal(err)
	}
	defer agent.Close()
	deadline := time.Now().Add(5 * time.Second)
	for len(h.Targets()) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("agent never registered")
		}
		time.Sleep(10 * time.Millisecond)
	}

	hdr := http.Header{}
	hdr.Set("Origin", hubServer.URL)
	browser, _, err := websocket.Dial(ctx, wsBase+"/ws/client", &websocket.DialOptions{HTTPHeader: hdr})
	if err != nil {
		t.Fatal(err)
	}
	defer browser.CloseNow()
	if err := browser.Write(ctx, websocket.MessageBinary, wire.Encode(&wire.Subscribe{TargetID: "e2e"})); err != nil {
		t.Fatal(err)
	}
	read := func() wire.Message {
		rctx, rcancel := context.WithTimeout(ctx, 5*time.Second)
		defer rcancel()
		_, data, err := browser.Read(rctx)
		if err != nil {
			t.Fatal(err)
		}
		msg, err := wire.Decode(data)
		if err != nil {
			t.Fatal(err)
		}
		return msg
	}

	var store *wire.EntityFields
	var schema *wire.Schema
	for store == nil {
		switch m := read().(type) {
		case *wire.TargetSchema:
			schema = m.Schema
		case *wire.SnapshotChunk:
			if len(m.Entities) != 1 {
				t.Fatalf("snapshot entities = %d", len(m.Entities))
			}
			store, err = wire.ParseState(m.Entities[0].TypeTag, m.Entities[0].State)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	if schema == nil {
		t.Fatal("schema not sent before the snapshot")
	}

	if err := srv.Post(func(*golem.Server) {
		hero.mu.Lock()
		hero.hp, hero.dirty = 3, 4
		hero.mu.Unlock()
	}); err != nil {
		t.Fatal(err)
	}
	for {
		b, ok := read().(*wire.Batch)
		if !ok || len(b.Deltas) == 0 {
			continue
		}
		d := b.Deltas[0]
		if err := store.ApplyDelta(schema.EntityByTag(d.TypeTag), d.Mask, d.Delta); err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(store.EncodeState(), []byte{4 << 3, 3}) {
			break
		}
	}

	// A space move reaches the browser through the hub.
	if err := srv.Post(func(s *golem.Server) {
		if err := s.SetEntitySpace(1, 2); err != nil {
			t.Error(err)
		}
	}); err != nil {
		t.Fatal(err)
	}
	for {
		b, ok := read().(*wire.Batch)
		if !ok || len(b.Moves) == 0 {
			continue
		}
		if b.Moves[0] != (wire.SpaceMove{ID: 1, Space: 2}) {
			t.Fatalf("browser move = %+v", b.Moves)
		}
		break
	}
	info := h.Targets()[0]
	if info.ObserveState != "ready" || info.ViewerCount != 1 || info.EntityCount != 1 || info.Labels["map"] != "test" {
		t.Fatalf("target info = %+v", info)
	}
}
