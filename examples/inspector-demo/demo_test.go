package main

import (
	"context"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/demiurgos-hub/golem-engine/examples/inspector-demo/generated"
	"github.com/demiurgos-hub/golem-engine/golem"
	"github.com/demiurgos-hub/golem-engine/golem/inspector"
	"github.com/demiurgos-hub/golem-engine/golem/inspector/hub"
	"github.com/demiurgos-hub/golem-engine/golem/inspector/wire"
)

// TestGeneratedSchemaMatchesEncoders checks the baked InspectorSchema against
// the baked encoders: State tags, Delta tags and dirty bits must line up, and
// a state merged from masked deltas must equal the authoritative state.
func TestGeneratedSchemaMatchesEncoders(t *testing.T) {
	schema := generated.InspectorSchema()
	if err := schema.Validate(); err != nil {
		t.Fatal(err)
	}
	w := generated.NewSyncedWanderer(1, 2, 3, "Ada", 100, 7, []string{"a", "b"}, map[string]int32{"str": 9}, []generated.Item{{Name: "torch", Qty: 1}}, 42)
	full, _ := w.FullUpdate()
	tag, state, err := wire.UnwrapUpdate(full)
	if err != nil {
		t.Fatal(err)
	}
	ent := schema.EntityByTag(tag)
	if ent == nil || ent.Name != "Wanderer" || ent.StateTag != tag {
		t.Fatalf("state tag %d maps to %+v", tag, ent)
	}
	fields, err := wire.ParseState(tag, state)
	if err != nil {
		t.Fatal(err)
	}

	// Change every tick var across separate flushes, accumulating the mask as
	// the agent does, then merge one masked delta.
	_, _ = w.FlushUpdate()
	var mask uint64
	for _, change := range []func(){
		func() { w.SetPosition3D(5, 6, 7) },
		func() { w.SetHp(0) },
		func() { w.SetGold(99) },
		func() { w.SetTags(nil) },
		func() { w.SetStats(map[string]int32{}) },
		func() { w.SetBag([]generated.Item{{Name: "rope", Qty: 2}}) },
	} {
		change()
		if _, err := w.FlushUpdate(); err != nil {
			t.Fatal(err)
		}
		mask |= w.LastFlushMask()
	}
	for _, f := range ent.Fields {
		if f.Bit >= 0 && mask&(1<<uint(f.Bit)) == 0 {
			t.Errorf("field %s bit %d not set by its setter (mask %b)", f.Name, f.Bit, mask)
		}
	}
	update, err := w.MarshalDeltaMask(mask)
	if err != nil {
		t.Fatal(err)
	}
	dtag, delta, err := wire.UnwrapUpdate(update)
	if err != nil || dtag != ent.DeltaTag {
		t.Fatalf("delta tag %d, want %d (%v)", dtag, ent.DeltaTag, err)
	}
	if err := fields.ApplyDelta(ent, mask, delta); err != nil {
		t.Fatal(err)
	}
	var merged, want generated.WandererState
	if err := merged.Unmarshal(fields.EncodeState()); err != nil {
		t.Fatal(err)
	}
	want = *w.FullState()
	// Unmarshal leaves an absent collection nil while FullState may hold an
	// empty one; both mean "empty".
	if len(merged.Tags) == 0 && len(want.Tags) == 0 {
		merged.Tags, want.Tags = nil, nil
	}
	if len(merged.Stats) == 0 && len(want.Stats) == 0 {
		merged.Stats, want.Stats = nil, nil
	}
	if !reflect.DeepEqual(merged, want) {
		t.Fatalf("merged state differs\n got %+v\nwant %+v", merged, want)
	}
}

func TestDemoStreamsThroughHub(t *testing.T) {
	quiet := log.New(io.Discard, "", 0)
	h, err := hub.New(hub.Config{AgentToken: "demo", Logger: quiet})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	defer h.Close(context.Background())
	ws := "ws" + strings.TrimPrefix(ts.URL, "http")

	srv, _ := generated.NewServer(golem.ServerConfig{TickRate: 50})
	world := newSim(7)
	if err := world.populate(srv, 30, 3, 2); err != nil {
		t.Fatal(err)
	}
	srv.OnTick(world.step)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.Run(ctx)
	agent, err := inspector.Attach(ctx, srv, generated.InspectorSchema(), inspector.Options{HubURL: ws + "/ws/agent", Token: "demo", TargetID: "demo", Logger: quiet})
	if err != nil {
		t.Fatal(err)
	}
	defer agent.Close()
	for deadline := time.Now().Add(5 * time.Second); len(h.Targets()) == 0; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("agent never registered")
		}
	}

	hdr := http.Header{}
	hdr.Set("Origin", ts.URL)
	conn, _, err := websocket.Dial(ctx, ws+"/ws/client", &websocket.DialOptions{HTTPHeader: hdr})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	conn.SetReadLimit(16 << 20)
	if err := conn.Write(ctx, websocket.MessageBinary, wire.Encode(&wire.Subscribe{TargetID: "demo"})); err != nil {
		t.Fatal(err)
	}
	var schema *wire.Schema
	entities := map[int64]*wire.EntityFields{}
	batches := 0
	for batches < 5 {
		rctx, rcancel := context.WithTimeout(ctx, 5*time.Second)
		_, data, err := conn.Read(rctx)
		rcancel()
		if err != nil {
			t.Fatal(err)
		}
		msg, err := wire.Decode(data)
		if err != nil {
			t.Fatal(err)
		}
		switch m := msg.(type) {
		case *wire.TargetSchema:
			schema = m.Schema
		case *wire.SnapshotChunk:
			for _, e := range m.Entities {
				f, err := wire.ParseState(e.TypeTag, e.State)
				if err != nil {
					t.Fatal(err)
				}
				entities[e.ID] = f
			}
		case *wire.Batch:
			batches++
			for _, s := range m.Spawns {
				f, _ := wire.ParseState(s.TypeTag, s.State)
				entities[s.ID] = f
			}
			for _, d := range m.Deltas {
				if f := entities[d.ID]; f != nil {
					if err := f.ApplyDelta(schema.EntityByTag(d.TypeTag), d.Mask, d.Delta); err != nil {
						t.Fatal(err)
					}
				}
			}
			for _, r := range m.Removals {
				delete(entities, r.ID)
			}
		}
	}
	if schema == nil || len(entities) != 35 {
		t.Fatalf("schema=%v entities=%d, want 35", schema != nil, len(entities))
	}
}
