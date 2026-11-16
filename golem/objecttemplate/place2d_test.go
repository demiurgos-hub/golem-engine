package objecttemplate

import (
	"errors"
	"testing"

	"github.com/demiurgos-hub/golem-engine/golem/collision"
)

type recordingBackend struct {
	collision.Backend
	shapes    map[int64]collision.Shape
	positions map[int64][2]float64
	triggers  map[int64]bool
}

func newRecordingBackend() *recordingBackend {
	return &recordingBackend{shapes: map[int64]collision.Shape{}, positions: map[int64][2]float64{}, triggers: map[int64]bool{}}
}
func (b *recordingBackend) Add(id int64, shape collision.Shape, _, _ uint32, trigger bool) {
	b.shapes[id], b.triggers[id] = shape, trigger
}
func (b *recordingBackend) Update(id int64, x, y float64) { b.positions[id] = [2]float64{x, y} }
func (b *recordingBackend) Remove(id int64) {
	delete(b.shapes, id)
	delete(b.positions, id)
}

func test2DCatalog(t *testing.T) *Catalog {
	t.Helper()
	return writeCatalog(t, Catalog{Generator: Generator, Version: Version, CatalogID: "2d", Revision: 1, Dimensions: 2, Templates: []Template{
		{ID: "wall", Shapes: []Shape2D{{ID: "body", Kind: ShapeBox, OffsetX: 1, Width: 4, Height: 1, Layer: "Wall"}, {ID: "post", Kind: ShapeCircle, OffsetY: 2, Radius: .5, Layer: "Wall", Trigger: true}}},
	}}, nil)
}

func resolveLayer(name string) (uint32, uint32, bool) { return 1, 1, name == "Wall" }

func TestPlacer2DRotatesScalesAndRemoves(t *testing.T) {
	backend := newRecordingBackend()
	placer := &Placer2D{Backend: backend, Catalog: test2DCatalog(t), Resolve: resolveLayer, IDs: &AtomicAllocator{}}
	placement, err := placer.Place("wall", Transform2D{X: 10, Y: 20, RotationDegrees: 90, ScaleX: 2, ScaleY: 2})
	if err != nil {
		t.Fatal(err)
	}
	ids := placement.IDs()
	if len(ids) != 2 || ids[0] != -1 || ids[1] != -2 {
		t.Fatalf("ids %v", ids)
	}
	if box := backend.shapes[-1].(collision.AABB); box.W != 2 || box.H != 8 || backend.positions[-1] != [2]float64{10, 22} {
		t.Fatalf("rotated box %+v at %v", box, backend.positions[-1])
	}
	if circle := backend.shapes[-2].(collision.Circle); circle.R != 1 || backend.positions[-2] != [2]float64{6, 20} || !backend.triggers[-2] {
		t.Fatalf("circle %+v at %v", circle, backend.positions[-2])
	}
	placement.Remove()
	placement.Remove()
	if len(backend.shapes) != 0 {
		t.Fatal("shapes left after removal")
	}
}

func TestPlacer2DValidatesBeforeMutating(t *testing.T) {
	backend := newRecordingBackend()
	catalog := test2DCatalog(t)
	placer := &Placer2D{Backend: backend, Catalog: catalog, Resolve: func(string) (uint32, uint32, bool) { return 0, 0, false }}
	for name, transform := range map[string]Transform2D{
		"unknown layer":    {},
		"diagonal":         {RotationDegrees: 45},
		"uneven circle":    {ScaleX: 1, ScaleY: 2},
		"negative scale":   {ScaleX: -1, ScaleY: -1},
		"unknown template": {},
	} {
		templateID := "wall"
		if name == "unknown template" {
			templateID = "absent"
		}
		placer.Resolve = resolveLayer
		if name == "unknown layer" {
			placer.Resolve = func(string) (uint32, uint32, bool) { return 0, 0, false }
		}
		if _, err := placer.Place(templateID, transform); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%s: %v", name, err)
		}
		if len(backend.shapes) != 0 {
			t.Fatalf("%s mutated the backend", name)
		}
	}
	if _, err := decodeCatalog(t, Catalog{Generator: Generator, Version: Version, CatalogID: "2d", Revision: 1, Dimensions: 2,
		Templates: []Template{{ID: "empty"}}}, nil); err == nil {
		t.Fatal("accepted a 2D template without shapes")
	}
}
