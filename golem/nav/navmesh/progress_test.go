package navmesh

import (
	"context"
	"testing"

	"github.com/demiurgos-hub/golem-engine/golem/nav/surface"
	navigation "github.com/demiurgos-hub/navmesh-go"
	"github.com/demiurgos-hub/navmesh-go/bake"
)

func TestSupportIdentityRequiresLiveSameViewAndPolygon(t *testing.T) {
	scene, config := adapterScene()
	built, err := bake.Build(context.Background(), scene, config)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := navigation.NewSnapshot(built.Bundle)
	if err != nil {
		t.Fatal(err)
	}
	backend, err := New(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	view, err := backend.Acquire(context.Background(), "human")
	if err != nil {
		t.Fatal(err)
	}
	defer view.Release()
	query, err := view.NewQuery(surface.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	project := func(q surface.Query, p surface.Vec3) surface.Location {
		r := q.Project(context.Background(), p, surface.ProjectionOptions{Horizontal: .3, Up: .3, Down: .3}, surface.Filter{})
		if r.Code != surface.Complete || r.Location == nil {
			t.Fatalf("projection %v: %+v", p, r.Status)
		}
		return r.Location
	}
	p := surface.Vec3{X: -2, Y: 3.1, Z: 1}
	start := project(query, p)
	identity := start.(surface.SupportIdentity)
	moved := query.Move(context.Background(), start, surface.Vec3{X: -1.9, Y: 3.1, Z: 1}, surface.Filter{})
	if moved.Code != surface.Complete || !identity.SameSupport(start) || !identity.SameSupport(moved.Location) {
		t.Fatal("straight progress lost exact support identity", moved.Status)
	}
	if identity.SameSupport(project(query, surface.Vec3{X: -2, Y: .1, Z: 1})) || identity.SameSupport(project(query, surface.Vec3{X: 2, Y: 3.1, Z: 1})) || identity.SameSupport(nil) {
		t.Fatal("floor, polygon or invalid handle conflated")
	}
	otherView, err := backend.Acquire(context.Background(), "human")
	if err != nil {
		t.Fatal(err)
	}
	defer otherView.Release()
	otherQuery, err := otherView.NewQuery(surface.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if identity.SameSupport(project(otherQuery, p)) {
		t.Fatal("independent view ownership conflated")
	}
	foreign, err := New(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	foreignView, err := foreign.Acquire(context.Background(), "human")
	if err != nil {
		t.Fatal(err)
	}
	defer foreignView.Release()
	foreignQuery, err := foreignView.NewQuery(surface.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if identity.SameSupport(project(foreignQuery, p)) {
		t.Fatal("foreign backend ownership conflated")
	}
	view.Release()
	if identity.SameSupport(start) || identity.SameSupport(moved.Location) {
		t.Fatal("released support accepted")
	}
}
