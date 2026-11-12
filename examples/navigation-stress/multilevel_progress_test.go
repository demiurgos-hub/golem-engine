package main

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/demiurgos-hub/golem-engine/golem"
	adapter "github.com/demiurgos-hub/golem-engine/golem/nav/navmesh"
	"github.com/demiurgos-hub/golem-engine/golem/nav/surface"
	navigation "github.com/demiurgos-hub/navmesh-go"
	"github.com/demiurgos-hub/navmesh-go/bake"
	"github.com/demiurgos-hub/navmesh-go/geom"
	"github.com/demiurgos-hub/navmesh-go/qualification/worldgen"
)

// capturedRampScene retains the two original buildings and the original tile,
// voxel and ground-triangle lattice around the captured start and destination.
// The central removable bridge was never part of repeated buildings.
func capturedRampScene(t *testing.T) (geom.Scene, bake.Config) {
	t.Helper()
	c := worldgen.DefaultConfig()
	c.Family, c.HalfExtent = "multilevel", 32
	f, err := worldgen.Generate(c)
	if err != nil {
		t.Fatal(err)
	}
	ground := geom.Asset{ID: "captured-ground"}
	for z := 64; z <= 128; z += 4 {
		for x := -128; x <= 0; x += 4 {
			ground.Vertices = append(ground.Vertices, geom.Vec3{X: float64(x), Z: float64(z)})
		}
	}
	for z := 0; z < 16; z++ {
		for x := 0; x < 32; x++ {
			a := uint32(z*33 + x)
			ground.Triangles = append(ground.Triangles,
				geom.Triangle{Indices: [3]uint32{a, a + 33, a + 1}, Area: 1},
				geom.Triangle{Indices: [3]uint32{a + 1, a + 33, a + 34}, Area: 1})
		}
	}
	scene := geom.Scene{Assets: []geom.Asset{ground}, Instances: []geom.Instance{{ID: "captured-ground", AssetID: ground.ID, Revision: 1, Transform: geom.Identity()}}}
	for _, asset := range f.Scene.Assets {
		if asset.ID != "ground" && asset.ID != "removable-bridge" {
			scene.Assets = append(scene.Assets, asset)
		}
	}
	for _, original := range f.Scene.Instances {
		if !strings.HasPrefix(original.ID, "building-") && original.ID != "cave-ceiling" {
			continue
		}
		for i, x := range []float64{-32, -96} {
			instance := original
			instance.ID += []string{"-start", "-destination"}[i]
			instance.Transform[3] += x
			instance.Transform[11] += 96
			scene.Instances = append(scene.Instances, instance)
		}
	}
	cfg := f.Profiles[0]
	cfg.Settings.Bounds = geom.Bounds{Min: geom.Vec3{X: -128, Y: -8, Z: 64}, Max: geom.Vec3{X: 0, Y: 24, Z: 128}}
	return scene, cfg
}

func TestCapturedMultilevelRampMakesServerProgress(t *testing.T) {
	scene, cfg := capturedRampScene(t)
	built, err := bake.Build(context.Background(), scene, cfg)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := navigation.NewSnapshot(built.Bundle)
	if err != nil {
		t.Fatal(err)
	}
	backend, err := adapter.New(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	start := surface.Vec3{X: -40.49014663696289, Y: 8.196059226989746, Z: 84.1496353149414}
	goal := surface.Vec3{X: -96, Y: 12.100000381469727, Z: 96}
	query, err := snapshot.NewQuery(navigation.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	project := func(p surface.Vec3) navigation.Surface {
		hit := query.Project(context.Background(), geom.Vec3{X: p.X, Y: p.Y, Z: p.Z}, navigation.ProjectionOptions{Horizontal: 1, Up: .8, Down: .8}, navigation.Filter{})
		if hit.Code != navigation.Complete {
			t.Fatalf("captured point must project: %+v", hit)
		}
		return hit.Surface
	}
	from, to := project(start), project(goal)
	if p := from.Position(); math.Abs(p.Y-start.Y) > 1e-6 || p.X != start.X || p.Z != start.Z {
		t.Fatalf("minimal source changed the captured support: %+v", p)
	}
	path := query.FindPath(context.Background(), from, to, navigation.Filter{})
	if path.Code != navigation.Complete {
		t.Fatalf("captured route must be reachable within ordinary limits: %+v", path)
	}
	t.Logf("captured support=%+v first route corner=%+v", from.Position(), path.Path.Corners()[0])
	srv := golem.NewServer(golem.ServerConfig{TickRate: 20})
	if err = srv.SetSurfaceNavigation(backend, golem.NavigationConfig{PathWorkers: 4, MovementAgentsPerTick: 1000, MovementBudget: time.Second, PathDrainBudget: 2 * time.Millisecond, RequiredProfiles: []surface.ProfileID{"human"}}); err != nil {
		t.Fatal(err)
	}
	a := &actor{ID: 493, PositionValue: start}
	motor := &bodyMotor{actor: a, metrics: &measurements{}, tick: func() uint64 { return srv.Tick() }, radius: .4, height: 1.8, bodies: &collisionWorld{}, tickDuration: 50 * time.Millisecond, measured: func() bool { return true }}
	if err = srv.CreateEntity(a); err != nil {
		t.Fatal(err)
	}
	if err = srv.AttachNavigation(a.ID, golem.NavigationAgentConfig{Profile: "human", Speed: 4, Acceleration: 12, StoppingDistance: .2, SupportTolerance: .2, Projection: surface.ProjectionOptions{Horizontal: 1, Up: .8, Down: .8}, Motor: motor}); err != nil {
		t.Fatal(err)
	}
	if err = srv.SetNavigationDestination(a.ID, goal); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var final golem.NavigationState
	previous, maxStep := start, 0.0
	srv.OnTickEnd(func(_ uint64, _ time.Duration) {
		final, _ = srv.NavigationAgentState(a.ID)
		p := a.PositionValue
		maxStep = math.Max(maxStep, math.Sqrt((p.X-previous.X)*(p.X-previous.X)+(p.Y-previous.Y)*(p.Y-previous.Y)+(p.Z-previous.Z)*(p.Z-previous.Z)))
		previous = p
		if math.Hypot(p.X-start.X, p.Z-start.Z) > 1 || final.Mode == golem.NavigationBlocked || final.Mode == golem.NavigationFailed || final.Mode == golem.NavigationUnsupported {
			cancel()
		}
	})
	if err = srv.Run(ctx); err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	if final.Mode != golem.NavigationFollowing || math.Hypot(a.PositionValue.X-start.X, a.PositionValue.Z-start.Z) <= 1 || maxStep > .3 {
		t.Fatalf("reachable ramp must progress at configured speed without a floor snap: state=%+v position=%+v maximum step=%g", final, a.PositionValue, maxStep)
	}
}
