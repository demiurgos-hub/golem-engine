package main

import (
	"context"
	"errors"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/demiurgos-hub/golem-engine/golem"
	"github.com/demiurgos-hub/navmesh-go/bake"
	"github.com/demiurgos-hub/navmesh-go/geom"
)

func TestFullChurnAvoidsPaddedRouteCorridorTiles(t *testing.T) {
	c, err := counts("full")
	if err != nil {
		t.Fatal(err)
	}
	columns := int(math.Ceil(math.Sqrt(float64(c.Characters))))
	for _, extent := range []int{768, 2304} {
		for _, radius := range []float64{.4, .8} {
			t.Run(fmt.Sprintf("extent%d/radius%g", extent, radius), func(t *testing.T) {
				half := float32(extent / 2)
				cfg := navigationBakeConfig(geom.Profile{ID: "test", Radius: radius, Height: 2.4, MaxSlope: 40, MaxClimb: .3}, half)
				keys, err := bake.TileKeys(cfg)
				if err != nil {
					t.Fatal(err)
				}
				_, minLaneZ := characterLane(0, columns, half)
				tileWidth := cfg.Settings.CellSize * float64(cfg.Settings.TileSize)
				corridorMinZ := cfg.Settings.Origin.Z + math.Floor((float64(minLaneZ)-cfg.Settings.Origin.Z)/tileWidth)*tileWidth
				for sequence := uint64(1); sequence <= 2; sequence++ {
					source, changed, err := churnSource(sequence, extent)
					if err != nil || source == nil {
						t.Fatalf("source %d: %v", sequence, err)
					}
					dirty := 0
					for _, key := range keys {
						padded := bake.TileBounds(cfg, key)
						if !padded.Intersects(changed) {
							continue
						}
						dirty++
						if padded.Max.Z >= corridorMinZ {
							t.Fatalf("sequence %d dirty tile %v padded to Z=%g reaches route corridor Z=%g", sequence, key, padded.Max.Z, corridorMinZ)
						}
					}
					if dirty == 0 {
						t.Fatal("door change no longer rebuilds any navigation tile")
					}
				}
			})
		}
	}
}

func TestActualServerPublishesRepeatedGeometryAndUnloads(t *testing.T) {
	c := workload{Characters: 2, DynamicBodies: 2, Projectiles: 1, StaticColliders: 2, Triangles: 8, Queries: 4, TilesX: 1, TilesZ: 1, Cells: 2}
	o := options{Extent: 64, Out: t.TempDir(), Workers: 1, PhysicsWorkers: 1, QueryWorkers: 1}
	s, err := makeScene(o, c, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var loopErr error
	if err = s.server.ObservePhysicsFrames(func(m golem.PhysicsFrameMetrics) {
		if m.Err != nil {
			loopErr = m.Err
			cancel()
		}
	}); err != nil {
		t.Fatal(err)
	}
	finished := false
	s.server.OnTick(func(_ float64, _ *golem.Server) {
		if finished {
			return
		}
		if loopErr = s.pollChurn(o.Out); loopErr != nil {
			finished = true
			cancel()
			return
		}
		if s.churn.PendingGeneration != 0 {
			return
		}
		if s.churn.Sequence == 3 {
			if _, present := s.server.PhysicsGeometry(churnKey); present {
				loopErr = errors.New("unloaded geometry lease remained live")
			}
			if len(s.server.PendingPhysicsGeometry()) != 0 {
				loopErr = errors.New("completed publication remained pending")
			}
			if metrics := s.world.Metrics(); metrics.Colliders != c.StaticColliders+c.Characters+c.DynamicBodies {
				loopErr = errors.New("geometry replacement leaked colliders")
			}
			finished = true
			cancel()
			return
		}
		loopErr = s.beginChurn(o)
		if loopErr != nil {
			finished = true
			cancel()
		}
	})
	if err = s.server.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if loopErr != nil {
		t.Fatal(loopErr)
	}
	if !finished || s.churn.Committed != 3 || s.churn.Unloads != 1 {
		t.Fatalf("churn did not complete: %+v", s.churn)
	}
}
