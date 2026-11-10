package navmesh

import (
	"context"
	"errors"
	"math"
	"testing"

	"github.com/demiurgos-hub/golem-engine/golem/nav/surface"
	navigation "github.com/demiurgos-hub/navmesh-go"
	"github.com/demiurgos-hub/navmesh-go/bake"
	"github.com/demiurgos-hub/navmesh-go/dynamic"
	"github.com/demiurgos-hub/navmesh-go/geom"
)

func adapterScene() (geom.Scene, bake.Config) {
	asset := geom.Asset{ID: "floor", Vertices: []geom.Vec3{{X: -4, Z: -4}, {X: -4, Z: 4}, {X: 4, Z: 4}, {X: 4, Z: -4}}, Triangles: []geom.Triangle{{Indices: [3]uint32{0, 1, 2}, Area: 1}, {Indices: [3]uint32{0, 2, 3}, Area: 1}}}
	scene := geom.Scene{Assets: []geom.Asset{asset}, Instances: []geom.Instance{{ID: "ground", AssetID: "floor", Revision: 1, Transform: geom.Identity()}, {ID: "upper", AssetID: "floor", Revision: 1, Transform: geom.Translation(geom.Vec3{Y: 3})}}}
	cfg := bake.DefaultConfig("adapter", geom.Bounds{Min: geom.Vec3{X: -4, Y: -1, Z: -4}, Max: geom.Vec3{X: 4, Y: 6, Z: 4}})
	cfg.Settings.TileSize = 16
	return scene, cfg
}

func TestAdapterBakeQueriesAndViewOwnership(t *testing.T) {
	scene, cfg := adapterScene()
	result, err := bake.Build(context.Background(), scene, cfg)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := navigation.NewSnapshot(result.Bundle)
	if err != nil {
		t.Fatal(err)
	}
	backend, err := New(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	v, err := backend.Acquire(context.Background(), "human")
	if err != nil {
		t.Fatal(err)
	}
	query, err := v.NewQuery(surface.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	project := func(p surface.Vec3) surface.Location {
		r := query.Project(context.Background(), p, surface.ProjectionOptions{Horizontal: .4, Up: .3, Down: .3}, surface.Filter{})
		if r.Code != surface.Complete || r.Location == nil {
			t.Fatalf("projection %+v: %+v", p, r.Status)
		}
		return r.Location
	}
	start := project(surface.Vec3{X: -2, Y: 3.1})
	end := project(surface.Vec3{X: 2, Y: 3.1})
	ground := project(surface.Vec3{X: -2, Y: .1})
	if math.Abs(start.Position().Y-ground.Position().Y) < 2 {
		t.Fatal("stacked floors collapsed")
	}
	path := query.FindPath(context.Background(), start, end, surface.Filter{})
	if path.Code != surface.Complete || path.Route == nil || len(path.Route.Tiles()) < 2 {
		t.Fatalf("seam path: %+v", path.Status)
	}
	corners := path.Route.Corners()
	corners[0].Position.X = 100
	if path.Route.Corners()[0].Position.X == 100 {
		t.Fatal("route corners alias backend memory")
	}
	moved := query.Move(context.Background(), start, surface.Vec3{X: -1, Y: 3}, surface.Filter{})
	if moved.Code != surface.Complete || math.Abs(moved.Location.Position().Y-start.Position().Y) > .2 {
		t.Fatalf("move %+v", moved.Status)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if query.FindPath(ctx, start, end, surface.Filter{}).Code != surface.Cancelled {
		t.Fatal("cancellation lost")
	}
	v.Release()
	v.Release()
	if _, err := v.NewQuery(surface.Limits{}); err == nil {
		t.Fatal("released view created query")
	}
	if query.Height(context.Background(), start).Code != surface.Stale {
		t.Fatal("released query accepted handle")
	}
	if len(path.Route.Corners()) != 0 {
		t.Fatal("released route exposed corners")
	}
}

func TestBuildRetryClassificationIsExplicit(t *testing.T) {
	var retry interface{ RetryNavigationBuild() bool }
	busy := buildError(dynamic.ErrBusy)
	if !errors.Is(busy, dynamic.ErrBusy) || !errors.As(busy, &retry) || !retry.RetryNavigationBuild() {
		t.Fatal("capacity error lost retry marker/cause")
	}
	for _, err := range []error{dynamic.ErrSuperseded, context.Canceled, context.DeadlineExceeded, errors.New("invalid geometry")} {
		if errors.As(buildError(err), &retry) {
			t.Fatalf("deterministic/stale/cancelled error marked retryable: %v", err)
		}
	}
	if buildError(nil) != nil {
		t.Fatal("nil became an error")
	}
	if _, err := PrepareBuild(context.Background(), nil); err == nil {
		t.Fatal("nil world accepted")
	}
}

func TestPrepareBuildRetriesCapacityWithoutPublishingStaleGeometry(t *testing.T) {
	scene, cfg := adapterScene()
	world, err := dynamic.New(context.Background(), scene, cfg)
	if err != nil {
		t.Fatal(err)
	}
	initial := world.Snapshot().Revision()
	first, err := PrepareBuild(context.Background(), world)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Discard()
	_, err = PrepareBuild(context.Background(), world)
	var retry interface{ RetryNavigationBuild() bool }
	if !errors.Is(err, dynamic.ErrBusy) || !errors.As(err, &retry) || !retry.RetryNavigationBuild() {
		t.Fatalf("busy preparation is not retryable: %v", err)
	}
	if _, err = world.Apply(dynamic.Changes{RemoveInstances: []string{"upper"}}); err != nil {
		t.Fatal(err)
	}
	err = first.Commit()
	if !errors.Is(err, dynamic.ErrSuperseded) || errors.As(err, &retry) {
		t.Fatalf("obsolete candidate was not terminal: %v", err)
	}
	if world.Snapshot().Revision() != initial {
		t.Fatal("stale candidate changed committed geometry")
	}
	first.Discard()
	next, err := PrepareBuild(context.Background(), world)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Discard()
	if err = next.Commit(); err != nil {
		t.Fatal(err)
	}
	if world.Pending() || world.Snapshot().Revision() == initial {
		t.Fatal("released capacity did not permit desired publication")
	}
}

func TestAdapterDynamicPublicationRetainsOldView(t *testing.T) {
	scene, cfg := adapterScene()
	world, err := dynamic.New(context.Background(), scene, cfg)
	if err != nil {
		t.Fatal(err)
	}
	backend, err := NewDynamic(world)
	if err != nil {
		t.Fatal(err)
	}
	old, err := backend.Acquire(context.Background(), "human")
	if err != nil {
		t.Fatal(err)
	}
	defer old.Release()
	q, err := old.NewQuery(surface.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	p := surface.Vec3{Y: 3.1}
	opts := surface.ProjectionOptions{Horizontal: .4, Up: .3, Down: .3}
	before := q.Project(context.Background(), p, opts, surface.Filter{})
	if before.Code != surface.Complete {
		t.Fatal(before.Status)
	}
	if _, err := world.Apply(dynamic.Changes{RemoveInstances: []string{"upper"}}); err != nil {
		t.Fatal(err)
	}
	candidate, err := world.Prepare(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	build := &PreparedBuild{World: world, Candidate: candidate}
	if err := build.Commit(); err != nil {
		t.Fatal(err)
	}
	if backend.CurrentRevision("human") == old.Revision() {
		t.Fatal("new revision not published")
	}
	if q.Height(context.Background(), before.Location).Code != surface.Complete {
		t.Fatal("publication mutated retained snapshot")
	}
	current, err := backend.Acquire(context.Background(), "human")
	if err != nil {
		t.Fatal(err)
	}
	defer current.Release()
	q2, err := current.NewQuery(surface.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if q2.Project(context.Background(), p, opts, surface.Filter{}).Code != surface.NoSurface {
		t.Fatal("removed floor remains available")
	}
	if q2.Height(context.Background(), before.Location).Code != surface.Stale {
		t.Fatal("foreign view handle accepted")
	}
}
