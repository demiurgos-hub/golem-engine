package navmesh

import (
	"context"
	"reflect"
	"testing"

	"github.com/demiurgos-hub/golem-engine/golem/nav/surface"
	"github.com/demiurgos-hub/navmesh-go/dynamic"
	"github.com/demiurgos-hub/navmesh-go/geom"
)

func TestCorridorTransferPreservesCornersAndViewOwnership(t *testing.T) {
	scene, cfg := adapterScene()
	for i := range scene.Assets[0].Vertices {
		scene.Assets[0].Vertices[i].X *= 8
		scene.Assets[0].Vertices[i].Z *= 2
	}
	cfg.Settings.Bounds.Min.X, cfg.Settings.Bounds.Max.X = -32, 32
	cfg.Settings.Bounds.Min.Z, cfg.Settings.Bounds.Max.Z = -8, 8
	w, err := dynamic.New(context.Background(), scene, cfg)
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewDynamic(w)
	if err != nil {
		t.Fatal(err)
	}
	acquire := func(backend *Backend) (surface.View, surface.Query) {
		t.Helper()
		view, err := backend.Acquire(context.Background(), "human")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(view.Release)
		q, err := view.NewQuery(surface.Limits{})
		if err != nil {
			t.Fatal(err)
		}
		return view, q
	}
	project := func(q surface.Query, x float64) surface.Location {
		t.Helper()
		result := q.Project(context.Background(), surface.Vec3{X: x, Y: 3.1}, surface.ProjectionOptions{Horizontal: .3, Up: .3, Down: .3}, surface.Filter{})
		if result.Code != surface.Complete {
			t.Fatal(result.Status)
		}
		return result.Location
	}
	commit := func(id string, x float64) {
		t.Helper()
		volume := geom.Volume{ID: id, Shape: "box", Exclude: true, Bounds: geom.Bounds{Min: geom.Vec3{X: x - 1, Z: -1}, Max: geom.Vec3{X: x + 1, Y: 2, Z: 1}}}
		if _, err := w.Apply(dynamic.Changes{Volumes: []geom.Volume{volume}}); err != nil {
			t.Fatal(err)
		}
		candidate, err := w.Prepare(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if err = w.Commit(candidate); err != nil {
			t.Fatal(err)
		}
	}
	old, q := acquire(b)
	start, end := project(q, -24), project(q, -8)
	path := q.FindPath(context.Background(), start, end, surface.Filter{})
	if path.Code != surface.Complete {
		t.Fatal(path.Status)
	}
	corners := path.Route.Corners()
	moved := q.Move(context.Background(), start, surface.Vec3{X: -23.9, Y: 3.1}, surface.Filter{})
	if moved.Code != surface.Complete {
		t.Fatal(moved.Status)
	}
	commit("distant", 20)
	current, next := acquire(b)
	transfer := next.(surface.CorridorTransferQuery)
	result := transfer.TransferPath(context.Background(), path.Route, moved.Location, moved.Location.Position(), .05, surface.Filter{})
	if result.Code != surface.Complete || result.Route == nil || result.Location == nil || result.Location.Revision() != current.Revision() || !reflect.DeepEqual(corners, result.Route.Corners()) || !reflect.DeepEqual(corners, path.Route.Corners()) {
		t.Fatal("strict transfer repaired, trimmed or mutated the corridor", result.Status)
	}
	if only := transfer.TransferPath(context.Background(), nil, moved.Location, moved.Location.Position(), .05, surface.Filter{}); only.Code != surface.Complete || only.Route != nil || only.Location == nil {
		t.Fatal("support-only transfer failed", only.Status)
	}
	_, mixedQuery := acquire(b)
	if mixed := transfer.TransferPath(context.Background(), result.Route, project(mixedQuery, -23.9), moved.Location.Position(), .05, surface.Filter{}); mixed.Code != surface.Stale {
		t.Fatal("mixed view ownership accepted", mixed.Status)
	}
	foreign, err := New(w.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	_, foreignQuery := acquire(foreign)
	if got := foreignQuery.(surface.CorridorTransferQuery).TransferPath(context.Background(), path.Route, moved.Location, moved.Location.Position(), .05, surface.Filter{}); got.Code != surface.Stale {
		t.Fatal("foreign backend accepted transferred handles", got.Status)
	}
	old.Release()
	if got := transfer.TransferPath(context.Background(), path.Route, moved.Location, moved.Location.Position(), .05, surface.Filter{}); got.Code != surface.Stale {
		t.Fatal("released old view accepted", got.Status)
	}
	commit("changed-suffix", -8)
	_, latest := acquire(b)
	if got := latest.(surface.CorridorTransferQuery).TransferPath(context.Background(), result.Route, result.Location, result.Location.Position(), .05, surface.Filter{}); got.Code != surface.Stale || got.Route != nil {
		t.Fatal("strict transfer repaired a changed suffix", got.Status)
	}
	current.Release()
	if got := transfer.TransferPath(context.Background(), nil, result.Location, result.Location.Position(), .05, surface.Filter{}); got.Code != surface.Stale {
		t.Fatal("released target view accepted", got.Status)
	}
}
