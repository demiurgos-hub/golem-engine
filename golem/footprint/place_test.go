package footprint_test

import (
	"strings"
	"sync"
	"testing"

	"github.com/demiurgos-hub/golem-engine/golem/collision"
	"github.com/demiurgos-hub/golem-engine/golem/collision3d"
	"github.com/demiurgos-hub/golem-engine/golem/footprint"
)

type stub2D struct {
	mu      sync.Mutex
	adds    []add2D
	updates []upd2D
	removes []int64
}

type add2D struct {
	id          int64
	shape       collision.Shape
	layer, mask uint32
	trigger     bool
}

type upd2D struct {
	id   int64
	x, y float64
}

func (s *stub2D) Add(id int64, shape collision.Shape, layer, mask uint32, trigger bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.adds = append(s.adds, add2D{id, shape, layer, mask, trigger})
}
func (s *stub2D) Remove(id int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.removes = append(s.removes, id)
}
func (s *stub2D) Set(int64, collision.Shape, uint32, uint32, bool) {
}
func (s *stub2D) Update(id int64, x, y float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.updates = append(s.updates, upd2D{id, x, y})
}
func (s *stub2D) Step(float64) []collision.Contact            { return nil }
func (s *stub2D) ReadBack(func(entityID int64, x, y float64)) {}

type stub3D struct {
	mu      sync.Mutex
	adds    []add3D
	updates []upd3D
	removes []int64
}

type add3D struct {
	id          int64
	shape       collision3d.Shape
	layer, mask uint32
	trigger     bool
}

type upd3D struct {
	id      int64
	x, y, z float64
}

func (s *stub3D) Add(id int64, shape collision3d.Shape, layer, mask uint32, trigger bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.adds = append(s.adds, add3D{id, shape, layer, mask, trigger})
}
func (s *stub3D) Remove(id int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.removes = append(s.removes, id)
}
func (s *stub3D) Set(int64, collision3d.Shape, uint32, uint32, bool) {
}
func (s *stub3D) Update(id int64, x, y, z float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.updates = append(s.updates, upd3D{id, x, y, z})
}
func (s *stub3D) Step(float64) []collision3d.Contact             { return nil }
func (s *stub3D) ReadBack(func(entityID int64, x, y, z float64)) {}

type seqAlloc struct {
	ids []int64
	i   int
}

func (a *seqAlloc) Next() int64 {
	id := a.ids[a.i]
	a.i++
	return id
}

func mustParse(t *testing.T, yaml string) *footprint.Set {
	t.Helper()
	set, err := footprint.Parse([]byte(yaml))
	if err != nil {
		t.Fatal(err)
	}
	return set
}

func TestPlace2D_addUpdateRemove_idempotent(t *testing.T) {
	set := mustParse(t, `
version: 1
dimensions: 2
footprints:
  aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa:
    alias: block
    shapes:
      - type: aabb
        w: 2
        h: 1
        offset: {x: 1, y: 0}
        trigger: false
        layer: Wall
      - type: circle
        r: 0.5
        offset: {x: 0, y: 2}
        trigger: true
        layer: Trigger
`)
	fp, _ := set.LookupAlias("block")
	backend := &stub2D{}
	layers := collision.NewLayers().Define("Wall", "Trigger").SetCollides("Wall", "Trigger")
	placer := &footprint.Placer2D{
		Backend: backend,
		Resolve: footprint.LayersResolver(layers),
		IDs:     footprint.NewAtomicAllocator(),
	}

	h, err := placer.Place(fp, footprint.Transform2D{X: 10, Y: 20, Scale: 2, RotationDegrees: 90})
	if err != nil {
		t.Fatal(err)
	}
	if len(backend.adds) != 2 || len(backend.updates) != 2 {
		t.Fatalf("adds=%d updates=%d", len(backend.adds), len(backend.updates))
	}
	// Add then Update per shape, same IDs.
	if backend.adds[0].id != backend.updates[0].id || backend.adds[1].id != backend.updates[1].id {
		t.Fatalf("Add/Update id mismatch: %+v %+v", backend.adds, backend.updates)
	}
	ids := h.IDs()
	if ids[0] >= 0 || ids[1] >= 0 || ids[0] == ids[1] {
		t.Fatalf("synthetic ids = %v", ids)
	}

	// 90° + scale 2: offset (1,0) -> scale (2,0) -> rot (-0,2)=(0,2) + translation
	if backend.updates[0].x != 10 || backend.updates[0].y != 22 {
		t.Fatalf("aabb world pos = (%v,%v)", backend.updates[0].x, backend.updates[0].y)
	}
	aabb, ok := backend.adds[0].shape.(collision.AABB)
	// Full extents: (w,h)=(2,1) * scale 2 => (4,2); 90° swaps => (2,4).
	if !ok || aabb.W != 2 || aabb.H != 4 {
		t.Fatalf("aabb extents = %+v", aabb)
	}
	// offset (0,2)*2=(0,4); 90° -> (-4,0); + (10,20) = (6,20)
	if backend.updates[1].x != 6 || backend.updates[1].y != 20 {
		t.Fatalf("circle world pos = (%v,%v)", backend.updates[1].x, backend.updates[1].y)
	}
	circle := backend.adds[1].shape.(collision.Circle)
	if circle.R != 1 {
		t.Fatalf("circle r = %v", circle.R)
	}
	if backend.adds[0].layer != layers.Layer("Wall") || backend.adds[0].mask != layers.Mask("Wall") {
		t.Fatalf("wall layer/mask = %d/%d", backend.adds[0].layer, backend.adds[0].mask)
	}
	if !backend.adds[1].trigger {
		t.Fatal("expected trigger circle")
	}

	h.Remove()
	h.Remove()
	var nilH *footprint.Handle2D
	nilH.Remove()
	if len(backend.removes) != 2 {
		t.Fatalf("removes = %v (want one Remove per id)", backend.removes)
	}
}

func TestPlace2D_uniqueIDsAcrossPlacements(t *testing.T) {
	set := mustParse(t, `
version: 1
dimensions: 2
footprints:
  aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa:
    shapes:
      - type: circle
        r: 1
        offset: {x: 0, y: 0}
        layer: Default
`)
	fp, _ := set.LookupGUID("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	backend := &stub2D{}
	alloc := footprint.NewAtomicAllocator()
	placer := &footprint.Placer2D{
		Backend: backend,
		Resolve: func(string) (uint32, uint32, bool) { return 1, 1, true },
		IDs:     alloc,
	}
	h1, err := placer.Place(fp, footprint.Transform2D{Scale: 1})
	if err != nil {
		t.Fatal(err)
	}
	h2, err := placer.Place(fp, footprint.Transform2D{Scale: 1})
	if err != nil {
		t.Fatal(err)
	}
	if h1.IDs()[0] == h2.IDs()[0] {
		t.Fatalf("expected unique ids, got %d and %d", h1.IDs()[0], h2.IDs()[0])
	}
}

func TestPlace2D_rollbackOnBadAllocator(t *testing.T) {
	set := mustParse(t, `
version: 1
dimensions: 2
footprints:
  aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa:
    shapes:
      - type: circle
        r: 1
        offset: {x: 0, y: 0}
        layer: Default
      - type: circle
        r: 1
        offset: {x: 1, y: 0}
        layer: Default
`)
	fp, _ := set.LookupGUID("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	backend := &stub2D{}
	placer := &footprint.Placer2D{
		Backend: backend,
		Resolve: func(string) (uint32, uint32, bool) { return 1, 2, true },
		IDs:     &seqAlloc{ids: []int64{-10, 5}},
	}
	_, err := placer.Place(fp, footprint.Transform2D{Scale: 1})
	if err == nil || !strings.Contains(err.Error(), "negative") {
		t.Fatalf("expected negative id error, got %v", err)
	}
	if len(backend.adds) != 1 || len(backend.removes) != 1 || backend.removes[0] != -10 {
		t.Fatalf("rollback adds=%v removes=%v", backend.adds, backend.removes)
	}
}

func TestPlace2D_unknownLayerBeforeMutations(t *testing.T) {
	set := mustParse(t, `
version: 1
dimensions: 2
footprints:
  aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa:
    shapes:
      - type: circle
        r: 1
        offset: {x: 0, y: 0}
        layer: Missing
`)
	fp, _ := set.LookupGUID("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	backend := &stub2D{}
	layers := collision.NewLayers().Define("Default")
	placer := &footprint.Placer2D{Backend: backend, Resolve: footprint.LayersResolver(layers), IDs: footprint.NewAtomicAllocator()}
	_, err := placer.Place(fp, footprint.Transform2D{Scale: 1})
	if err == nil || !strings.Contains(err.Error(), "unknown layer") {
		t.Fatalf("got %v", err)
	}
	if len(backend.adds) != 0 {
		t.Fatalf("expected no backend mutations, adds=%v", backend.adds)
	}
}

func TestPlace2D_rejectsNonQuarterRotationAndNonUniformScale(t *testing.T) {
	set := mustParse(t, `
version: 1
dimensions: 2
footprints:
  aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa:
    shapes:
      - type: circle
        r: 1
        offset: {x: 0, y: 0}
        layer: Default
`)
	fp, _ := set.LookupGUID("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	placer := &footprint.Placer2D{
		Backend: &stub2D{},
		Resolve: func(string) (uint32, uint32, bool) { return 1, 1, true },
		IDs:     footprint.NewAtomicAllocator(),
	}
	if _, err := placer.Place(fp, footprint.Transform2D{Scale: 1, RotationDegrees: 45}); err == nil {
		t.Fatal("expected rotation error")
	}
	if _, err := placer.Place(fp, footprint.Transform2D{Scale: 0}); err == nil {
		t.Fatal("expected scale error")
	}
	if _, err := placer.Place(fp, footprint.Transform2D{Scale: -1}); err == nil {
		t.Fatal("expected scale error")
	}
}

func TestPlace3D_yawScaleAndFullExtents(t *testing.T) {
	set := mustParse(t, `
version: 1
dimensions: 3
footprints:
  aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa:
    shapes:
      - type: aabb
        w: 2
        h: 1
        d: 4
        offset: {x: 1, y: 0, z: 0}
        layer: Default
      - type: sphere
        r: 0.5
        offset: {x: 0, y: 1, z: 0}
        trigger: true
        layer: Default
`)
	fp, _ := set.LookupGUID("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	backend := &stub3D{}
	placer := &footprint.Placer3D{
		Backend: backend,
		Resolve: func(string) (uint32, uint32, bool) { return 4, 8, true },
		IDs:     footprint.NewAtomicAllocator(),
	}
	h, err := placer.Place(fp, footprint.Transform3D{X: 0, Y: 0, Z: 0, Scale: 2, YawDegrees: 90})
	if err != nil {
		t.Fatal(err)
	}
	// offset (1,0,0)*2=(2,0,0); yaw90 -> (0,0,-2)
	if backend.updates[0].x != 0 || backend.updates[0].y != 0 || backend.updates[0].z != -2 {
		t.Fatalf("aabb pos = %+v", backend.updates[0])
	}
	aabb := backend.adds[0].shape.(collision3d.AABB)
	// (2,1,4)*2=(4,2,8); yaw90 swap w/d -> (8,2,4)
	if aabb.W != 8 || aabb.H != 2 || aabb.D != 4 {
		t.Fatalf("aabb = %+v", aabb)
	}
	// sphere offset (0,1,0)*2=(0,2,0); yaw keeps y
	if backend.updates[1].x != 0 || backend.updates[1].y != 2 || backend.updates[1].z != 0 {
		t.Fatalf("sphere pos = %+v", backend.updates[1])
	}
	if backend.adds[1].shape.(collision3d.Sphere).R != 1 {
		t.Fatalf("sphere r = %+v", backend.adds[1].shape)
	}
	h.Remove()
	h.Remove()
	if len(backend.removes) != 2 {
		t.Fatalf("removes = %v", backend.removes)
	}
}

func TestLayersResolver_nilAndUnknown(t *testing.T) {
	if _, _, ok := footprint.LayersResolver(nil)("Default"); ok {
		t.Fatal("nil layers should miss")
	}
	layers := collision.NewLayers().Define("Default")
	layer, mask, ok := footprint.LayersResolver(layers)("Default")
	if !ok || layer != 1 || mask != 0 {
		t.Fatalf("got %d %d %v", layer, mask, ok)
	}
	if _, _, ok := footprint.LayersResolver(layers)("Missing"); ok {
		t.Fatal("expected miss")
	}
}

func TestPlace_rejectsCrossDimension(t *testing.T) {
	fp2, ok := mustParse(t, `
version: 1
dimensions: 2
footprints:
  aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa:
    shapes:
      - type: aabb
        w: 1
        h: 1
        offset: {x: 0, y: 0}
        layer: Default
`).LookupGUID("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	if !ok {
		t.Fatal("missing 2D footprint")
	}
	fp3, ok := mustParse(t, `
version: 1
dimensions: 3
footprints:
  bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb:
    shapes:
      - type: aabb
        w: 1
        h: 1
        d: 1
        offset: {x: 0, y: 0, z: 0}
        layer: Default
`).LookupGUID("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	if !ok {
		t.Fatal("missing 3D footprint")
	}

	p2 := &footprint.Placer2D{
		Backend: &stub2D{},
		Resolve: func(string) (uint32, uint32, bool) { return 1, 1, true },
		IDs:     footprint.NewAtomicAllocator(),
	}
	if _, err := p2.Place(fp3, footprint.Transform2D{Scale: 1}); err == nil || !strings.Contains(err.Error(), "Placer2D requires dimensions=2") {
		t.Fatalf("expected 2D dimensions error, got %v", err)
	}

	p3 := &footprint.Placer3D{
		Backend: &stub3D{},
		Resolve: func(string) (uint32, uint32, bool) { return 1, 1, true },
		IDs:     footprint.NewAtomicAllocator(),
	}
	if _, err := p3.Place(fp2, footprint.Transform3D{Scale: 1}); err == nil || !strings.Contains(err.Error(), "Placer3D requires dimensions=3") {
		t.Fatalf("expected 3D dimensions error, got %v", err)
	}

	// Defense in depth: mismatched shape extents even if Dimensions is forced.
	bad3As2 := *fp3
	bad3As2.Dimensions = 2
	if _, err := p2.Place(&bad3As2, footprint.Transform2D{Scale: 1}); err == nil || !strings.Contains(err.Error(), "depth would be lost") {
		t.Fatalf("expected depth-loss rejection, got %v", err)
	}
	bad2As3 := *fp2
	bad2As3.Dimensions = 3
	if _, err := p3.Place(&bad2As3, footprint.Transform3D{Scale: 1}); err == nil || !strings.Contains(err.Error(), "depth would be zero") {
		t.Fatalf("expected zero-depth rejection, got %v", err)
	}
}

func TestHandle2D_RemoveConcurrentIdempotent(t *testing.T) {
	set := mustParse(t, `
version: 1
dimensions: 2
footprints:
  aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa:
    shapes:
      - type: circle
        r: 1
        offset: {x: 0, y: 0}
        layer: Default
      - type: circle
        r: 1
        offset: {x: 1, y: 0}
        layer: Default
`)
	fp, _ := set.LookupGUID("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	backend := &stub2D{}
	placer := &footprint.Placer2D{
		Backend: backend,
		Resolve: func(string) (uint32, uint32, bool) { return 1, 1, true },
		IDs:     footprint.NewAtomicAllocator(),
	}
	h, err := placer.Place(fp, footprint.Transform2D{Scale: 1})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			h.Remove()
		}()
	}
	wg.Wait()
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if len(backend.removes) != 2 {
		t.Fatalf("removes = %v (want exactly one pass over both ids)", backend.removes)
	}
}

func TestHandle3D_RemoveConcurrentIdempotent(t *testing.T) {
	set := mustParse(t, `
version: 1
dimensions: 3
footprints:
  aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa:
    shapes:
      - type: sphere
        r: 1
        offset: {x: 0, y: 0, z: 0}
        layer: Default
`)
	fp, _ := set.LookupGUID("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	backend := &stub3D{}
	placer := &footprint.Placer3D{
		Backend: backend,
		Resolve: func(string) (uint32, uint32, bool) { return 1, 1, true },
		IDs:     footprint.NewAtomicAllocator(),
	}
	h, err := placer.Place(fp, footprint.Transform3D{Scale: 1})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			h.Remove()
		}()
	}
	wg.Wait()
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if len(backend.removes) != 1 {
		t.Fatalf("removes = %v", backend.removes)
	}
}
