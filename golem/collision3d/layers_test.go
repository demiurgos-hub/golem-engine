package collision3d

import "testing"

type stubBackend3D struct {
	addCalls    []stubAddCall3D
	setCalls    []stubAddCall3D
	removeCalls []int64
}

type stubAddCall3D struct {
	id      int64
	shape   Shape
	layer   uint32
	mask    uint32
	trigger bool
}

func (s *stubBackend3D) Add(id int64, shape Shape, layer, mask uint32, trigger bool) {
	s.addCalls = append(s.addCalls, stubAddCall3D{id, shape, layer, mask, trigger})
}
func (s *stubBackend3D) Set(id int64, shape Shape, layer, mask uint32, trigger bool) {
	s.setCalls = append(s.setCalls, stubAddCall3D{id, shape, layer, mask, trigger})
}
func (s *stubBackend3D) Remove(id int64)                                 { s.removeCalls = append(s.removeCalls, id) }
func (s *stubBackend3D) Update(int64, float64, float64, float64)         {}
func (s *stubBackend3D) Step(float64) []Contact                          { return nil }
func (s *stubBackend3D) ReadBack(func(int64, float64, float64, float64)) {}

func TestLayers3D_Define_assignsConsecutiveBits(t *testing.T) {
	l := NewLayers().Define("A", "B", "C")
	if got := l.Layer("A"); got != 1<<0 {
		t.Errorf("Layer(A) = %d, want %d", got, 1<<0)
	}
	if got := l.Layer("B"); got != 1<<1 {
		t.Errorf("Layer(B) = %d, want %d", got, 1<<1)
	}
	if got := l.Layer("C"); got != 1<<2 {
		t.Errorf("Layer(C) = %d, want %d", got, 1<<2)
	}
}

func TestLayers3D_SetCollides_symmetric(t *testing.T) {
	l := NewLayers().Define("Player", "Wall").SetCollides("Player", "Wall")
	if got := l.Mask("Player"); got != l.Layer("Wall") {
		t.Errorf("Mask(Player) = %d, want %d", got, l.Layer("Wall"))
	}
	if got := l.Mask("Wall"); got != l.Layer("Player") {
		t.Errorf("Mask(Wall) = %d, want %d", got, l.Layer("Player"))
	}
}

func TestLayers3D_Add_routesLayerAndMask(t *testing.T) {
	stub := &stubBackend3D{}
	l := NewLayers().
		Bind(stub).
		Define("Player", "Monster").
		SetCollides("Player", "Monster")

	shape := Sphere{R: 1}
	l.Add(42, shape, "Player", false)

	if len(stub.addCalls) != 1 {
		t.Fatalf("expected 1 Add call, got %d", len(stub.addCalls))
	}
	c := stub.addCalls[0]
	if c.id != 42 || c.layer != l.Layer("Player") || c.mask != l.Mask("Player") || c.trigger {
		t.Fatalf("unexpected Add call: %+v", c)
	}
}

func TestLayers3D_Set_routesLayerAndMask(t *testing.T) {
	stub := &stubBackend3D{}
	l := NewLayers().
		Bind(stub).
		Define("Player", "Monster").
		SetCollides("Player", "Monster")

	shape := AABB{W: 2, H: 4, D: 6}
	l.Set(7, shape, "Monster", true)

	if len(stub.setCalls) != 1 {
		t.Fatalf("expected 1 Set call, got %d", len(stub.setCalls))
	}
	c := stub.setCalls[0]
	if c.id != 7 || c.layer != l.Layer("Monster") || c.mask != l.Mask("Monster") || !c.trigger {
		t.Fatalf("unexpected Set call: %+v", c)
	}
}

func TestLayers3D_Remove_delegates(t *testing.T) {
	stub := &stubBackend3D{}
	l := NewLayers().Bind(stub).Define("Player")
	l.Remove(99)
	if len(stub.removeCalls) != 1 || stub.removeCalls[0] != 99 {
		t.Errorf("Remove calls = %v, want [99]", stub.removeCalls)
	}
}

func TestLayers3D_MaskFor_combinesBits(t *testing.T) {
	l := NewLayers().Define("A", "B", "C")
	want := l.Layer("A") | l.Layer("C")
	if got := l.MaskFor("A", "C"); got != want {
		t.Errorf("MaskFor(A,C) = %b, want %b", got, want)
	}
}

func TestLayers3D_Lookup(t *testing.T) {
	l := NewLayers().Define("Player", "Wall").SetCollides("Player", "Wall")
	layer, mask, ok := l.Lookup("Player")
	if !ok || layer != l.Layer("Player") || mask != l.Mask("Player") {
		t.Fatalf("Lookup(Player) = %d,%d,%v", layer, mask, ok)
	}
	if _, _, ok := l.Lookup("Missing"); ok {
		t.Fatal("Lookup(Missing) should fail")
	}
}

func TestLayers3D_Add_panicWithNoBackend(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic calling Add without Bind")
		}
	}()
	NewLayers().Define("A").Add(1, Sphere{R: 1}, "A", false)
}
