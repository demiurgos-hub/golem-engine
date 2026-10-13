package footprint_test

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/demiurgos-hub/golem-engine/golem/collision"
	"github.com/demiurgos-hub/golem-engine/golem/collision3d"
	"github.com/demiurgos-hub/golem-engine/golem/footprint"
)

type ordered2D struct {
	stub2D
	events *[]string
}

func (b *ordered2D) Add(id int64, s collision.Shape, l, m uint32, tr bool) {
	*b.events = append(*b.events, fmt.Sprint("add ", id))
	b.stub2D.Add(id, s, l, m, tr)
}
func (b *ordered2D) Update(id int64, x, y float64) {
	*b.events = append(*b.events, fmt.Sprint("update ", id))
	b.stub2D.Update(id, x, y)
}
func (b *ordered2D) Remove(id int64) {
	*b.events = append(*b.events, fmt.Sprint("remove ", id))
	b.stub2D.Remove(id)
}

type ordered3D struct {
	stub3D
	events *[]string
}

func (b *ordered3D) Add(id int64, s collision3d.Shape, l, m uint32, tr bool) {
	*b.events = append(*b.events, fmt.Sprint("add ", id))
	b.stub3D.Add(id, s, l, m, tr)
}
func (b *ordered3D) Update(id int64, x, y, z float64) {
	*b.events = append(*b.events, fmt.Sprint("update ", id))
	b.stub3D.Update(id, x, y, z)
}
func (b *ordered3D) Remove(id int64) {
	*b.events = append(*b.events, fmt.Sprint("remove ", id))
	b.stub3D.Remove(id)
}

type orderedAlloc struct {
	seqAlloc
	events *[]string
}

func (a *orderedAlloc) Next() int64 {
	v := a.seqAlloc.Next()
	*a.events = append(*a.events, fmt.Sprint("next ", v))
	return v
}

type placementResult interface {
	IDs() []int64
	Remove()
}

func TestPlacementPreparationOwnershipAndRollback(t *testing.T) {
	for _, dim := range []int{2, 3} {
		for _, mode := range []string{"success", "first-id", "late-id", "third-id", "late-layer", "late-shape", "empty"} {
			t.Run(fmt.Sprintf("%d/%s", dim, mode), func(t *testing.T) {
				var events []string
				kind := footprint.ShapeCircle
				if dim == 3 {
					kind = footprint.ShapeSphere
				}
				fp := &footprint.Footprint{GUID: "contract", Dimensions: dim, Shapes: []footprint.Shape{{Type: kind, R: 1, Layer: "first"}, {Type: kind, R: 1, Layer: "second"}}}
				if mode == "late-shape" {
					fp.Shapes[1].Type = "unsupported"
				}
				if mode == "empty" {
					fp.Shapes = nil
				}
				ids := []int64{-10, -11}
				if mode == "first-id" {
					ids[0] = 0
				}
				if mode == "late-id" {
					ids[1] = 7
				}
				if mode == "third-id" {
					fp.Shapes = append(fp.Shapes, footprint.Shape{Type: kind, R: 1, Layer: "third"})
					ids = append(ids, 7)
				}
				alloc := &orderedAlloc{seqAlloc: seqAlloc{ids: ids}, events: &events}
				resolve := func(name string) (uint32, uint32, bool) {
					events = append(events, "resolve "+name)
					return 1, 2, mode != "late-layer" || name != "second"
				}
				var h placementResult
				var err error
				if dim == 2 {
					p := &footprint.Placer2D{Backend: &ordered2D{events: &events}, Resolve: resolve, IDs: alloc}
					h, err = p.Place(fp, footprint.Transform2D{Scale: 1})
				} else {
					p := &footprint.Placer3D{Backend: &ordered3D{events: &events}, Resolve: resolve, IDs: alloc}
					h, err = p.Place(fp, footprint.Transform3D{Scale: 1})
				}
				want := []string{"resolve first", "resolve second"}
				switch mode {
				case "late-layer", "late-shape":
					if err == nil {
						t.Fatal("expected preparation error")
					}
				case "first-id":
					want = append(want, "next 0")
					if err == nil || err.Error() != "footprint: allocator must return negative IDs, got 0" {
						t.Fatalf("error=%v", err)
					}
				case "late-id":
					want = append(want, "next -10", "add -10", "update -10", "next 7", "remove -10")
					if err == nil || !strings.Contains(err.Error(), "got 7") {
						t.Fatalf("error=%v", err)
					}
				case "third-id":
					want = append(want, "resolve third", "next -10", "add -10", "update -10", "next -11", "add -11", "update -11", "next 7", "remove -10", "remove -11")
					if err == nil || !strings.Contains(err.Error(), "got 7") {
						t.Fatalf("error=%v", err)
					}
				case "success":
					if err != nil {
						t.Fatal(err)
					}
					want = append(want, "next -10", "add -10", "update -10", "next -11", "add -11", "update -11")
					copyIDs := h.IDs()
					copyIDs[0] = 42
					h.Remove()
					h.Remove()
					want = append(want, "remove -10", "remove -11")
					if !reflect.DeepEqual(h.IDs(), []int64{-10, -11}) {
						t.Fatalf("owned IDs changed: %v", h.IDs())
					}
				case "empty":
					want = nil
					if err != nil || h.IDs() != nil {
						t.Fatalf("empty result=%v,%v", h, err)
					}
					h.Remove()
				}
				if !reflect.DeepEqual(events, want) {
					t.Fatalf("events=%v want %v", events, want)
				}
			})
		}
	}
	var h2 *footprint.Handle2D
	var h3 *footprint.Handle3D
	h2.Remove()
	h3.Remove()
	if h2.IDs() != nil || h3.IDs() != nil {
		t.Fatal("nil IDs")
	}
	var zero2 footprint.Handle2D
	var zero3 footprint.Handle3D
	zero2.Remove()
	zero3.Remove()
}

func TestDefaultAllocatorSharedAcrossDimensions(t *testing.T) {
	resolve := func(string) (uint32, uint32, bool) { return 1, 1, true }
	p2 := &footprint.Placer2D{Backend: &stub2D{}, Resolve: resolve}
	p3 := &footprint.Placer3D{Backend: &stub3D{}, Resolve: resolve}
	seen := make(map[int64]bool)
	for i := 0; i < 2; i++ {
		h2, err := p2.Place(&footprint.Footprint{Dimensions: 2, Shapes: []footprint.Shape{{Type: footprint.ShapeCircle, R: 1}}}, footprint.Transform2D{Scale: 1})
		if err != nil {
			t.Fatal(err)
		}
		h3, err := p3.Place(&footprint.Footprint{Dimensions: 3, Shapes: []footprint.Shape{{Type: footprint.ShapeSphere, R: 1}}}, footprint.Transform3D{Scale: 1})
		if err != nil {
			t.Fatal(err)
		}
		for _, h := range []placementResult{h2, h3} {
			id := h.IDs()[0]
			if id >= 0 || seen[id] {
				t.Fatalf("default allocator reused or returned invalid ID %d", id)
			}
			seen[id] = true
			h.Remove()
		}
	}
}

type discard2D struct{}

func (discard2D) Add(int64, collision.Shape, uint32, uint32, bool) {}
func (discard2D) Set(int64, collision.Shape, uint32, uint32, bool) {}
func (discard2D) Update(int64, float64, float64)                   {}
func (discard2D) Remove(int64)                                     {}
func (discard2D) Step(float64) []collision.Contact                 { return nil }
func (discard2D) ReadBack(func(int64, float64, float64))           {}

func BenchmarkPlacement(b *testing.B) {
	fp := &footprint.Footprint{Dimensions: 2, Shapes: []footprint.Shape{{Type: footprint.ShapeCircle, R: 1, Layer: "wall"}, {Type: footprint.ShapeAABB, W: 2, H: 1, Layer: "wall"}}}
	p := &footprint.Placer2D{Backend: discard2D{}, IDs: footprint.NewAtomicAllocator(), Resolve: func(string) (uint32, uint32, bool) { return 1, 1, true }}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		h, err := p.Place(fp, footprint.Transform2D{Scale: 1})
		if err != nil {
			b.Fatal(err)
		}
		h.Remove()
	}
}
