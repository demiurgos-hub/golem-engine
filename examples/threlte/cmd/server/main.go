package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"math"
	"net/http"

	"github.com/demiurgos-hub/golem-engine/golem"
	"github.com/demiurgos-hub/golem-engine/golem/host"

	"example.com/golem-threlte/internal/generated"
)

type actorState struct {
	entity *generated.SyncedActor
	phase  float64
}

type demo struct {
	server  *golem.Server
	runtime *generated.Runtime
	actors  map[int64]*actorState
	stats   *generated.SyncedSessionStats
	beacon  *generated.SyncedBeacon
	elapsed float64
	pulses  uint32
}

func main() {
	addr := flag.String("addr", "127.0.0.1:8080", "HTTP and WebSocket address")
	assets := flag.String("assets", "web/dist", "built browser assets directory")
	flag.Parse()
	d := newDemo()
	mux := http.NewServeMux()
	mux.Handle("/api/ws", d.server.Handler())
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("ready\n"))
	})
	log.Printf("Signal Garden: http://%s (WebSocket /api/ws)", *addr)
	if err := host.Run(context.Background(), host.RunOptions{
		Server: d.server,
		HTTPServer: &http.Server{
			Addr: *addr,
			Handler: host.SPAHandler(host.SPAOptions{
				APIHandler: mux,
				DiskDir:    *assets,
			}),
		},
	}); err != nil {
		log.Fatal(err)
	}
}

func newDemo() *demo {
	srv, rt := generated.NewServer(golem.ServerConfig{
		TickRate:        20,
		Transport:       golem.TransportWebSocket,
		StateUpdateLane: golem.StateUpdateLaneStream,
		Path:            "/api/ws",
	})
	d := &demo{
		server: srv, runtime: rt,
		actors: make(map[int64]*actorState),
		stats:  generated.NewSyncedSessionStats(0, 0, 0, 0, 0),
	}
	must(srv.CreateEntity(d.stats))
	d.toggleBeacon()
	srv.OnConnect(d.connect)
	srv.OnDisconnect(func(sess *golem.Session) {
		delete(d.actors, sess.ID)
		d.stats.SetConnectedCount(int32(len(d.actors)))
		// SpawnAvatar owns removal of the session's Actor after this hook.
	})
	srv.OnTick(d.tick)
	rt.Commands.OnPulse(func(_ int64, actor *generated.SyncedActor, _ *generated.PulseCommand) {
		actor.SetPulseCount(actor.PulseCount() + 1)
		d.pulses++
		d.publishArena()
		if err := rt.Events.BroadcastActorPulsed(actor.EntityID(), 1); err != nil {
			log.Printf("pulse event: %v", err)
		}
	})
	rt.Commands.OnWarp(func(senderID int64, actor *generated.SyncedActor, _ *generated.WarpCommand) {
		if state := d.actors[senderID]; state != nil {
			state.phase += math.Pi
			d.placeActor(state)
			actor.SetWarpCount(actor.WarpCount() + 1)
		}
	})
	rt.Commands.OnToggleBeacon(func(_ int64, _ *generated.ToggleBeaconCommand) { d.toggleBeacon() })
	rt.Commands.BindAllHandlers(func(sess *golem.Session, err error) {
		log.Printf("session %d command: %v", sess.ID, err)
	})
	return d
}

func (d *demo) connect(sess *golem.Session) {
	actor := generated.NewSyncedActor(0, 0.55, 0, fmt.Sprintf("Actor %02d", sess.ID), 0, 0)
	state := &actorState{entity: actor, phase: float64(sess.ID-1) * 1.5}
	d.placeActor(state)
	if err := d.server.SpawnAvatar(sess, actor, golem.AvatarOptions{}); err != nil {
		log.Printf("spawn actor: %v", err)
		return
	}
	d.actors[sess.ID] = state
	d.stats.SetConnectedCount(int32(len(d.actors)))
	if err := d.runtime.Events.SendAssigned(sess.ID, actor.EntityID(), sess.ID); err != nil {
		log.Printf("assign actor: %v", err)
	}
}

func (d *demo) tick(dt float64, _ *golem.Server) {
	d.elapsed += dt
	d.stats.SetUptimeSeconds(uint32(d.elapsed))
	for _, state := range d.actors {
		state.phase += dt * 0.35
		d.placeActor(state)
	}
	if d.beacon != nil {
		d.beacon.SetIntensity(float32(0.65 + 0.35*math.Sin(d.elapsed*1.4)))
	}
}

func (d *demo) placeActor(state *actorState) {
	state.entity.SetPosition3D(float32(math.Cos(state.phase)*2.8), 0.55, float32(math.Sin(state.phase)*2.8))
}

func (d *demo) toggleBeacon() {
	if d.beacon != nil {
		d.server.DeleteEntity(d.beacon.EntityID())
		d.beacon = nil
	} else {
		d.beacon = generated.NewSyncedBeacon(0, 0.65, 0, 1)
		must(d.server.CreateEntity(d.beacon))
	}
	d.publishArena()
}

func (d *demo) publishArena() {
	// Replace values so a connect snapshot never races a mutation of world data.
	d.server.World.Set(&generated.ArenaData{
		Title: "Signal Garden", BeaconActive: d.beacon != nil, PulseTotal: d.pulses,
	})
	if err := d.server.PushWorldData("Arena"); err != nil {
		log.Printf("arena world update: %v", err)
	}
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
