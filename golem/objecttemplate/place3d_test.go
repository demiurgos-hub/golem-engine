package objecttemplate

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
)

func TestPlacementsShareShapesAndReleaseThemOnRemoval(t *testing.T) {
	world := newWorld(t)
	placer, err := NewPlacer3D(world, testCatalog(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	rotation := yaw(90)
	a, err := placer.Place("a", "rock", At(p.Vec3{X: 10}, rotation), 7)
	if err != nil {
		t.Fatal(err)
	}
	shapes := len(placer.shapes)
	b, err := placer.Place("b", "rock", At(p.Vec3{X: -10}, p.Quat{W: 1}), 8)
	if err != nil {
		t.Fatal(err)
	}
	c, err := placer.Place("c", "rock-copy", At(p.Vec3{Z: 30}, p.Quat{W: 1}), 9)
	if err != nil {
		t.Fatal(err)
	}
	if len(placer.shapes) != shapes || shapes != 3 {
		t.Fatalf("placements registered %d shapes, want 3 shared across all", len(placer.shapes))
	}
	if _, err := placer.Place("a", "rock", At(p.Vec3{}, p.Quat{W: 1}), 0); !errors.Is(err, ErrInvalid) {
		t.Fatalf("duplicate placement ID: %v", err)
	}

	hit, ok := rayDown(t, world, 10, 0)
	if !ok || hit.Content != "a/root/convex" || !near(hit.Point.Y, 1) || hit.Owner != 7 {
		t.Fatalf("convex hit %+v", hit)
	}
	capsule := p.Vec3{X: 10}.Add(rotation.Rotate(p.Vec3{X: 3, Y: 1}))
	if hit, ok = rayDown(t, world, capsule.X, capsule.Z); !ok || hit.Content != "a/root/capsule" || !near(hit.Point.Y, 2) {
		t.Fatalf("rotated capsule hit %+v", hit)
	}
	if hit, ok = rayDown(t, world, 0, 30); !ok || hit.Content != "c/root/convex" {
		t.Fatalf("copy hit %+v", hit)
	}

	if err = a.Remove(); err != nil {
		t.Fatal(err)
	}
	if err = a.Remove(); err != nil {
		t.Fatal("second removal", err)
	}
	if len(placer.shapes) != shapes {
		t.Fatal("removing one placement released shapes others use")
	}
	if _, ok = rayDown(t, world, 10, 0); ok {
		t.Fatal("removed placement still hit")
	}
	if err = b.Remove(); err != nil {
		t.Fatal(err)
	}
	if err = c.Remove(); err != nil {
		t.Fatal(err)
	}
	if len(placer.shapes) != 0 || len(placer.placements) != 0 {
		t.Fatalf("leaked %d shapes and %d placements", len(placer.shapes), len(placer.placements))
	}
	if _, err = placer.Place("a", "rock", At(p.Vec3{}, p.Quat{W: 1}), 0); err != nil {
		t.Fatal("placement ID not reusable after removal", err)
	}
}

func TestScaledPlacementsScaleGeometryOrRejectShear(t *testing.T) {
	world := newWorld(t)
	placer, err := NewPlacer3D(world, testCatalog(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = placer.Place("big", "rock", Transform{Rotation: p.Quat{W: 1}, Scale: p.Vec3{X: 2, Y: 2, Z: 2}}, 0); err != nil {
		t.Fatal(err)
	}
	if hit, ok := rayDown(t, world, 0, 0); !ok || hit.Content != "big/root/convex" || !near(hit.Point.Y, 2) {
		t.Fatalf("scaled convex hit %+v", hit)
	}
	if hit, ok := rayDown(t, world, 6, 0); !ok || hit.Content != "big/root/capsule" || !near(hit.Point.Y, 4) {
		t.Fatalf("scaled capsule hit %+v", hit)
	}

	tilted := Transform{Position: p.Vec3{X: 40}, Rotation: p.Quat{W: 1}, Scale: p.Vec3{X: 1, Y: 3, Z: 1}}
	before := len(placer.shapes)
	if _, err = placer.Place("sheared", "tilted-capsule", tilted, 0); !errors.Is(err, p.ErrUnsupported) {
		t.Fatalf("sheared capsule: %v", err)
	}
	if _, ok := placer.Placement("sheared"); ok || len(placer.shapes) != before {
		t.Fatal("failed placement left state behind")
	}
	if _, ok := rayDown(t, world, 40, 0); ok {
		t.Fatal("failed placement left geometry behind")
	}
	if _, err = placer.Place("baked", "tilted-rock", tilted, 0); err != nil {
		t.Fatal("convex under shear should bake its vertices", err)
	}
	if hit, ok := rayDown(t, world, 40, 0); !ok || hit.Content != "baked/root/convex" {
		t.Fatalf("baked convex hit %+v", hit)
	}
	if _, err = placer.Place("terrain", "terrain", Transform{Rotation: p.Quat{W: 1}, Scale: p.Vec3{X: 2, Y: 1, Z: 2}}, 0); !errors.Is(err, p.ErrUnsupported) {
		t.Fatalf("scaled heightfield: %v", err)
	}
}

func TestTerrainMeshAndSensorTemplates(t *testing.T) {
	world := newWorld(t)
	placer, err := NewPlacer3D(world, testCatalog(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	terrain, err := placer.Place("t", "terrain", At(p.Vec3{X: 100, Z: 100}, p.Quat{W: 1}), 0)
	if err != nil {
		t.Fatal(err)
	}
	if hit, ok := rayDown(t, world, 104, 104); !ok || hit.Content != "t/root/terrain" || !near(hit.Point.Y, 0) {
		t.Fatalf("terrain hit %+v", hit)
	}
	if source := terrain.Colliders()[0].Source; source.Metadata[0].Key != "game.terrain" {
		t.Fatal("placed collider lost its template metadata")
	}

	building, err := placer.Place("house", "building", At(p.Vec3{X: -50}, p.Quat{W: 1}), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(building.Bodies()) != 2 || building.Colliders()[0].Body == building.Bodies()[0] {
		t.Fatal("offset static mesh did not get its own body")
	}
	if hit, ok := rayDown(t, world, -50, 5); !ok || hit.Content != "house/root/mesh" || !near(hit.Point.Y, .5) {
		t.Fatalf("mesh hit %+v", hit)
	}

	portal, err := placer.Place("portal", "portal", At(p.Vec3{X: 200}, p.Quat{W: 1}), 0)
	if err != nil {
		t.Fatal(err)
	}
	state, err := world.Body(portal.Bodies()[0])
	if err != nil || state.Mode != p.Kinematic {
		t.Fatalf("portal body %+v %v", state, err)
	}
}

func TestCookedCatalogPlacesWithoutRecooking(t *testing.T) {
	world := newWorld(t)
	catalog := testCatalog(t)
	cooked, err := catalog.Cook(context.Background(), world.ShapePreparer(), 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(cooked.shapes) != len(catalog.baseOrder) {
		t.Fatal("cook skipped shapes")
	}
	placer, err := NewPlacer3D(world, catalog, cooked)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"rock", "terrain", "building", "portal"} {
		if _, err = placer.Place(id, id, At(p.Vec3{X: 300}, p.Quat{W: 1}), 0); err != nil {
			t.Fatal(id, err)
		}
	}
	if hit, ok := rayDown(t, world, 300, 0); !ok || hit.Content != "rock/root/convex" || !near(hit.Point.Y, 1) {
		t.Fatalf("cooked convex hit %+v", hit)
	}
	other := testCatalog(t)
	if _, err = NewPlacer3D(world, other, cooked); !errors.Is(err, ErrInvalid) {
		t.Fatal("cooked shapes accepted for another catalog")
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = catalog.Cook(cancelled, world.ShapePreparer(), 1); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

type countingPreparer struct {
	p.ShapePreparer
	calls atomic.Int32
}

func (c *countingPreparer) PrepareShape(source p.ShapeDesc) (p.PreparedShape, error) {
	c.calls.Add(1)
	return c.ShapePreparer.PrepareShape(source)
}

func TestCookSelectedTemplatesLeavesOthersToPlacement(t *testing.T) {
	world := newWorld(t)
	catalog := testCatalog(t)
	preparer := &countingPreparer{ShapePreparer: world.ShapePreparer()}
	cooked, err := catalog.Cook(context.Background(), preparer, 2, "rock", "rock")
	if err != nil {
		t.Fatal(err)
	}
	rock, _ := catalog.Template("rock")
	keys := map[string]bool{}
	for _, plan := range rock.colliders {
		keys[plan.key] = true
	}
	if int(preparer.calls.Load()) != len(keys) || len(cooked.shapes) != len(keys) || len(keys) >= len(catalog.baseOrder) {
		t.Fatalf("cooked %d shapes for %d rock shapes of %d catalog shapes", preparer.calls.Load(), len(keys), len(catalog.baseOrder))
	}
	placer, err := NewPlacer3D(world, catalog, cooked)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"rock", "terrain"} {
		if _, err = placer.Place(id, id, At(p.Vec3{X: 300}, p.Quat{W: 1}), 0); err != nil {
			t.Fatal(id, err)
		}
	}
	if _, err = catalog.Cook(context.Background(), preparer, 1, "missing"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown template cooked: %v", err)
	}
}
