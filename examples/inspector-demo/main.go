// Command inspector-demo runs a headless Golem server with a moving world and
// attaches the Golem Inspector agent from GOLEM_INSPECTOR_* variables:
//
//	golem-inspector &                                    # hub on 127.0.0.1:8787
//	GOLEM_INSPECTOR_HUB_URL=ws://127.0.0.1:8787/ws/agent \
//	GOLEM_INSPECTOR_TOKEN=dev-token \
//	GOLEM_INSPECTOR_TARGET_ID=cave-1 GOLEM_INSPECTOR_LABELS=map=caves \
//	go run .
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/demiurgos-hub/golem-engine/examples/inspector-demo/generated"
	"github.com/demiurgos-hub/golem-engine/golem"
	"github.com/demiurgos-hub/golem-engine/golem/inspector"
)

func main() {
	wanderers := flag.Int("wanderers", 200, "wandering entities")
	merchants := flag.Int("merchants", 12, "merchant entities")
	beacons := flag.Int("beacons", 3, "global beacon entities")
	tickRate := flag.Int("tick-rate", 20, "server ticks per second")
	seed := flag.Uint64("seed", 1, "simulation seed")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	srv, _ := generated.NewServer(golem.ServerConfig{TickRate: *tickRate})
	world := newSim(*seed)
	if err := world.populate(srv, *wanderers, *merchants, *beacons); err != nil {
		log.Fatalf("inspector-demo: %v", err)
	}
	srv.OnTick(world.step)

	agent, err := inspector.Attach(ctx, srv, generated.InspectorSchema(), inspector.OptionsFromEnv())
	if err != nil {
		log.Fatalf("inspector-demo: %v", err)
	}
	if agent == nil {
		log.Printf("inspector-demo: %s is not set; running without the inspector agent", inspector.EnvHubURL)
	}
	defer agent.Close()

	log.Printf("inspector-demo: running %d entities at %d Hz", *wanderers+*merchants+*beacons, *tickRate)
	if err := srv.Run(ctx); err != nil && ctx.Err() == nil {
		log.Fatalf("inspector-demo: %v", err)
	}
}
