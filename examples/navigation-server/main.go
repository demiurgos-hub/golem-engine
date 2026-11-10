// Command navigation-server runs a server-only, Go-authored navigation lifecycle.
// It writes source geometry, a portable baked artifact and a restart checkpoint.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/demiurgos-hub/golem-engine/golem"
	adapter "github.com/demiurgos-hub/golem-engine/golem/nav/navmesh"
	"github.com/demiurgos-hub/golem-engine/golem/nav/navmesh/navbuild"
	"github.com/demiurgos-hub/golem-engine/golem/nav/surface"
	navigation "github.com/demiurgos-hub/navmesh-go"
	"github.com/demiurgos-hub/navmesh-go/artifact"
	"github.com/demiurgos-hub/navmesh-go/bake"
	"github.com/demiurgos-hub/navmesh-go/dynamic"
	"github.com/demiurgos-hub/navmesh-go/geom"
)

type actor struct {
	ID            int64
	PositionValue surface.Vec3
}

func (a *actor) EntityID() int64  { return a.ID }
func (a *actor) TypeName() string { return "Actor" }
func (a *actor) Position() (float32, float32) {
	return float32(a.PositionValue.X), float32(a.PositionValue.Z)
}
func (a *actor) Position3D() (float32, float32, float32) {
	return float32(a.PositionValue.X), float32(a.PositionValue.Y), float32(a.PositionValue.Z)
}
func (a *actor) SetPosition3D(x, y, z float32) {
	a.PositionValue = surface.Vec3{X: float64(x), Y: float64(y), Z: float64(z)}
}
func (a *actor) IsGlobal() bool               { return false }
func (a *actor) FlushUpdate() ([]byte, error) { return nil, nil }
func (a *actor) FullUpdate() ([]byte, error)  { return nil, nil }

type checkpoint struct {
	Version    uint32
	Actor      actor
	Navigation golem.NavigationCheckpoint
}

func main() {
	output := flag.String("out", "", "output directory (default: a new temporary directory)")
	flag.Parse()
	if err := run(*output); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(output string) error {
	if output == "" {
		var err error
		output, err = os.MkdirTemp("", "golem-navigation-demo-*")
		if err != nil {
			return err
		}
	}
	if err := os.MkdirAll(output, 0755); err != nil {
		return err
	}
	scene, cfg := sourceScene()
	sourceBytes, err := json.MarshalIndent(navbuild.Source{Scene: scene, Config: cfg}, "", "  ")
	if err != nil {
		return err
	}
	sourcePath, artifactPath := filepath.Join(output, "source.json"), filepath.Join(output, "world.nav")
	if err := os.WriteFile(sourcePath, sourceBytes, 0644); err != nil {
		return err
	}
	started := time.Now()
	result, err := navbuild.BakeFile(context.Background(), sourcePath, artifactPath)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(artifactPath)
	if err != nil {
		return err
	}
	bundle, err := artifact.Decode(data, artifact.DefaultLimits())
	if err != nil {
		return err
	}
	loaded, err := navigation.NewSnapshot(bundle)
	if err != nil {
		return err
	}
	q, err := loaded.NewQuery(navigation.Limits{})
	if err != nil {
		return err
	}
	upper := q.Project(context.Background(), geom.Vec3{Y: 3.1}, navigation.ProjectionOptions{Horizontal: .3, Up: .3, Down: .3}, navigation.Filter{})
	if upper.Code != navigation.Complete {
		return fmt.Errorf("initial bridge projection: %+v", upper.Status)
	}
	fmt.Printf("baked and loaded %d tiles, %d bytes in %s\n", len(result.Bundle.Tiles), len(data), time.Since(started))
	world, err := dynamic.New(context.Background(), scene, cfg)
	if err != nil {
		return err
	}
	entity := &actor{ID: 1, PositionValue: surface.Vec3{X: -5}}
	srv, err := runServer(world, entity, nil)
	if err != nil {
		return err
	}
	desired, _, revision := world.Desired()
	fingerprint, err := desired.Fingerprint()
	if err != nil {
		return err
	}
	worldBytes, err := world.Checkpoint("navigation-demo")
	if err != nil {
		return err
	}
	navState, err := srv.CaptureNavigationCheckpoint("navigation-demo", fingerprint, revision, worldBytes)
	if err != nil {
		return err
	}
	state := checkpoint{Version: 1, Actor: *entity, Navigation: navState}
	checkpointBytes, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	checkpointPath := filepath.Join(output, "checkpoint.json")
	if err := os.WriteFile(checkpointPath+".tmp", checkpointBytes, 0644); err != nil {
		return err
	}
	if err := os.Rename(checkpointPath+".tmp", checkpointPath); err != nil {
		return err
	}
	stored, err := os.ReadFile(checkpointPath)
	if err != nil {
		return err
	}
	var restored checkpoint
	if err := json.Unmarshal(stored, &restored); err != nil {
		return err
	}
	if restored.Version != 1 {
		return errors.New("unsupported demo checkpoint")
	}
	restoredWorld, err := dynamic.Restore(context.Background(), restored.Navigation.DesiredWorld, restored.Navigation.CheckpointID)
	if err != nil {
		return err
	}
	if _, err := runServer(restoredWorld, &restored.Actor, &restored.Navigation); err != nil {
		return err
	}
	check, err := restoredWorld.Snapshot().NewQuery(navigation.Limits{})
	if err != nil {
		return err
	}
	if check.Project(context.Background(), geom.Vec3{Y: 3.1}, navigation.ProjectionOptions{Horizontal: .3, Up: .3, Down: .3}, navigation.Filter{}).Code != navigation.NoSurface {
		return errors.New("removed bridge reappeared after restart")
	}
	fmt.Printf("restart restored desired geometry and arrived at %+v\nartifacts: %s\n", restored.Actor.PositionValue, output)
	return nil
}

func runServer(world *dynamic.World, entity *actor, restore *golem.NavigationCheckpoint) (*golem.Server, error) {
	backend, err := adapter.NewDynamic(world)
	if err != nil {
		return nil, err
	}
	srv := golem.NewServer(golem.ServerConfig{TickRate: 100})
	if err := srv.SetSurfaceNavigation(backend, golem.NavigationConfig{RequiredProfiles: []surface.ProfileID{"human"}}); err != nil {
		return nil, err
	}
	if err := srv.CreateEntity(entity); err != nil {
		return nil, err
	}
	if err := srv.AttachNavigation(entity.ID, golem.NavigationAgentConfig{Profile: "human", Speed: 8, Acceleration: 20, StoppingDistance: .02, Projection: surface.ProjectionOptions{Horizontal: .5, Up: .4, Down: .4}}); err != nil {
		return nil, err
	}
	if restore == nil {
		err = srv.SetNavigationDestination(entity.ID, surface.Vec3{X: 5})
	} else {
		if err = srv.BeginNavigationRestore(); err == nil {
			desired, _, _ := world.Desired()
			fingerprint, e := desired.Fingerprint()
			if e != nil {
				return nil, e
			}
			err = srv.RestoreNavigationIntents(*restore, fingerprint)
		}
		if err == nil {
			err = srv.CompleteNavigationRestore(nil)
		}
	}
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	changed, done := false, false
	var tickError error
	var maxTick time.Duration
	var measuredTicks uint64
	srv.OnTickEnd(func(_ uint64, wall time.Duration) {
		measuredTicks++
		if wall > maxTick {
			maxTick = wall
		}
	})
	srv.OnTick(func(_ float64, s *golem.Server) {
		if restore == nil && !changed && s.Tick() >= 20 {
			changed = true
			_, tickError = world.Apply(dynamic.Changes{RemoveInstances: []string{"bridge-instance"}, Volumes: []geom.Volume{{ID: "construction-blocker", Revision: 1, Shape: "box", Exclude: true, Bounds: geom.Bounds{Min: geom.Vec3{X: -.6, Y: -.2, Z: -1.4}, Max: geom.Vec3{X: .6, Y: 2, Z: 1.4}}}}})
			if tickError == nil {
				_, tickError = s.QueueNavigationBuild("world-change", 1, func(ctx context.Context) (golem.NavigationBuildResult, error) {
					return adapter.PrepareBuild(ctx, world)
				})
			}
			if tickError != nil {
				cancel()
				return
			}
		}
		state, _ := s.NavigationAgentState(entity.ID)
		if state.Mode == golem.NavigationFailed || state.Mode == golem.NavigationUnsupported {
			tickError = fmt.Errorf("agent state: %+v", state)
			cancel()
			return
		}
		if restore == nil && changed && state.WorldRevision >= 2 && entity.PositionValue.X > .3 {
			done = true
			cancel()
		}
		if restore != nil && state.Mode == golem.NavigationArrived {
			done = true
			cancel()
		}
	})
	err = srv.Run(ctx)
	if tickError != nil {
		return nil, tickError
	}
	if !done {
		return nil, fmt.Errorf("navigation scenario did not finish: %w", err)
	}
	maxTickText := maxTick.String()
	if maxTick == 0 {
		maxTickText = "below-clock-resolution"
	}
	fmt.Printf("server restore=%v revision=%d queries=%d measured_ticks=%d max_tick=%s position=%+v\n", restore != nil, world.Snapshot().Revision(), srv.NavigationStats().Paths.Accepted, measuredTicks, maxTickText, entity.PositionValue)
	return srv, nil
}

func sourceScene() (geom.Scene, bake.Config) {
	plane := func(id string, min, max geom.Vec3) geom.Asset {
		return geom.Asset{ID: id, Vertices: []geom.Vec3{{X: min.X, Y: min.Y, Z: min.Z}, {X: min.X, Y: min.Y, Z: max.Z}, {X: max.X, Y: max.Y, Z: max.Z}, {X: max.X, Y: max.Y, Z: min.Z}}, Triangles: []geom.Triangle{{Indices: [3]uint32{0, 1, 2}, Area: 1}, {Indices: [3]uint32{0, 2, 3}, Area: 1}}}
	}
	scene := geom.Scene{Assets: []geom.Asset{plane("ground", geom.Vec3{X: -8, Z: -8}, geom.Vec3{X: 8, Z: 8}), plane("bridge", geom.Vec3{X: -4, Y: 3, Z: -1}, geom.Vec3{X: 4, Y: 3, Z: 1})}, Instances: []geom.Instance{{ID: "terrain", AssetID: "ground", Revision: 1, Transform: geom.Identity()}, {ID: "bridge-instance", AssetID: "bridge", Revision: 1, Transform: geom.Identity()}}}
	cfg := bake.DefaultConfig("golem-navigation-demo", geom.Bounds{Min: geom.Vec3{X: -8, Y: -1, Z: -8}, Max: geom.Vec3{X: 8, Y: 6, Z: 8}})
	cfg.Settings.TileSize = 32
	return scene, cfg
}
