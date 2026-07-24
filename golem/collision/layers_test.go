package collision

import "testing"

// stubBackend records calls made to Add, Set, and Remove for test assertions.
type stubBackend struct {
	addCalls    []stubAddCall
	setCalls    []stubAddCall
	removeCalls []int64
}

type stubAddCall struct {
	id      int64
	shape   Shape
	layer   uint32
	mask    uint32
	trigger bool
}

func (s *stubBackend) Add(id int64, shape Shape, layer, mask uint32, trigger bool) {
	s.addCalls = append(s.addCalls, stubAddCall{id, shape, layer, mask, trigger})
}
func (s *stubBackend) Set(id int64, shape Shape, layer, mask uint32, trigger bool) {
	s.setCalls = append(s.setCalls, stubAddCall{id, shape, layer, mask, trigger})
}
func (s *stubBackend) Remove(id int64)                        { s.removeCalls = append(s.removeCalls, id) }
func (s *stubBackend) Update(int64, float64, float64)         {}
func (s *stubBackend) Step(float64) []Contact                 { return nil }
func (s *stubBackend) ReadBack(func(int64, float64, float64)) {}

func TestLayers_Define_assignsConsecutiveBits(t *testing.T) {
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

func TestLayers_Define_chainable(t *testing.T) {
	l := NewLayers().Define("X").Define("Y")
	if got := l.Layer("Y"); got != 1<<1 {
		t.Errorf("Layer(Y) = %d, want %d", got, 1<<1)
	}
}

func TestLayers_SetCollides_symmetric(t *testing.T) {
	l := NewLayers().Define("Player", "Wall").SetCollides("Player", "Wall")

	if got := l.Mask("Player"); got != l.Layer("Wall") {
		t.Errorf("Mask(Player) = %d, want %d (Layer(Wall))", got, l.Layer("Wall"))
	}
	if got := l.Mask("Wall"); got != l.Layer("Player") {
		t.Errorf("Mask(Wall) = %d, want %d (Layer(Player))", got, l.Layer("Player"))
	}
}

func TestLayers_SetCollides_selfCollision(t *testing.T) {
	l := NewLayers().Define("Enemy").SetCollides("Enemy", "Enemy")
	if got := l.Mask("Enemy"); got != l.Layer("Enemy") {
		t.Errorf("Mask(Enemy) after self-collide = %d, want %d", got, l.Layer("Enemy"))
	}
}

func TestLayers_Mask_noCollisions(t *testing.T) {
	l := NewLayers().Define("Ghost")
	if got := l.Mask("Ghost"); got != 0 {
		t.Errorf("Mask(Ghost) = %d, want 0 (no SetCollides called)", got)
	}
}

func TestLayers_Mask_multipleCollisions(t *testing.T) {
	l := NewLayers().
		Define("Player", "Enemy", "Wall").
		SetCollides("Player", "Enemy").
		SetCollides("Player", "Wall")

	want := l.Layer("Enemy") | l.Layer("Wall")
	if got := l.Mask("Player"); got != want {
		t.Errorf("Mask(Player) = %b, want %b", got, want)
	}
}

func TestLayers_MaskFor_combinesBits(t *testing.T) {
	l := NewLayers().Define("A", "B", "C")
	want := l.Layer("A") | l.Layer("C")
	if got := l.MaskFor("A", "C"); got != want {
		t.Errorf("MaskFor(A,C) = %b, want %b", got, want)
	}
}

func TestLayers_Lookup(t *testing.T) {
	l := NewLayers().Define("Player", "Wall").SetCollides("Player", "Wall")
	layer, mask, ok := l.Lookup("Player")
	if !ok || layer != l.Layer("Player") || mask != l.Mask("Player") {
		t.Fatalf("Lookup(Player) = %d,%d,%v", layer, mask, ok)
	}
	if _, _, ok := l.Lookup("Missing"); ok {
		t.Fatal("Lookup(Missing) should fail")
	}
	if _, _, ok := (*Layers)(nil).Lookup("Player"); ok {
		t.Fatal("nil Layers Lookup should fail")
	}
}

func TestLayers_MaskFor_singleName(t *testing.T) {
	l := NewLayers().Define("X")
	if got := l.MaskFor("X"); got != l.Layer("X") {
		t.Errorf("MaskFor(X) = %d, want %d", got, l.Layer("X"))
	}
}

func TestLayers_Define_panicOnDuplicate(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on duplicate Define, got none")
		}
	}()
	NewLayers().Define("A", "A")
}

func TestLayers_Define_panicOnDuplicateAcrossCalls(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on duplicate Define across calls, got none")
		}
	}()
	NewLayers().Define("A").Define("A")
}

func TestLayers_Define_panicBeyond32(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic when exceeding 32 layers, got none")
		}
	}()
	names := make([]string, 33)
	for i := range names {
		names[i] = string(rune('A' + i%26 + i/26*100))
	}
	// Use unique names
	uniqueNames := make([]string, 33)
	for i := range uniqueNames {
		uniqueNames[i] = string([]byte{byte('A' + i/26), byte('A' + i%26)})
	}
	NewLayers().Define(uniqueNames...)
}

func TestLayers_Layer_panicOnUnknown(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for unknown layer, got none")
		}
	}()
	NewLayers().Layer("Unknown")
}

func TestLayers_Mask_panicOnUnknown(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for unknown layer, got none")
		}
	}()
	NewLayers().Mask("Unknown")
}

func TestLayers_MaskFor_panicOnUnknown(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for unknown layer, got none")
		}
	}()
	NewLayers().Define("A").MaskFor("A", "Unknown")
}

func TestLayers_SetCollides_panicOnUnknownA(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for unknown layer a, got none")
		}
	}()
	NewLayers().Define("B").SetCollides("Unknown", "B")
}

func TestLayers_SetCollides_panicOnUnknownB(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for unknown layer b, got none")
		}
	}()
	NewLayers().Define("A").SetCollides("A", "Unknown")
}

func TestLayers_fullScenario(t *testing.T) {
	l := NewLayers().
		Define("Player", "Enemy", "Wall", "Projectile").
		SetCollides("Player", "Wall").
		SetCollides("Enemy", "Wall").
		SetCollides("Projectile", "Enemy").
		SetCollides("Projectile", "Wall")

	// Player collides with Wall only
	if got, want := l.Mask("Player"), l.Layer("Wall"); got != want {
		t.Errorf("Mask(Player) = %b, want %b", got, want)
	}
	// Enemy collides with Wall and Projectile (symmetric with SetCollides("Projectile","Enemy"))
	if got, want := l.Mask("Enemy"), l.Layer("Wall")|l.Layer("Projectile"); got != want {
		t.Errorf("Mask(Enemy) = %b, want %b", got, want)
	}
	// Wall collides with Player, Enemy, Projectile
	wantWall := l.Layer("Player") | l.Layer("Enemy") | l.Layer("Projectile")
	if got := l.Mask("Wall"); got != wantWall {
		t.Errorf("Mask(Wall) = %b, want %b", got, wantWall)
	}
	// Projectile collides with Enemy and Wall
	wantProj := l.Layer("Enemy") | l.Layer("Wall")
	if got := l.Mask("Projectile"); got != wantProj {
		t.Errorf("Mask(Projectile) = %b, want %b", got, wantProj)
	}
	// MaskFor spatial query
	if got := l.MaskFor("Enemy", "Wall"); got != l.Layer("Enemy")|l.Layer("Wall") {
		t.Errorf("MaskFor(Enemy,Wall) = %b, want %b", got, l.Layer("Enemy")|l.Layer("Wall"))
	}
}

// --- Bind / Add / Set / Remove tests ---

func TestLayers_Add_routesLayerAndMask(t *testing.T) {
	stub := &stubBackend{}
	l := NewLayers().
		Bind(stub).
		Define("Player", "Monster").
		SetCollides("Player", "Monster")

	shape := Circle{R: 1}
	l.Add(42, shape, "Player", false)

	if len(stub.addCalls) != 1 {
		t.Fatalf("expected 1 Add call, got %d", len(stub.addCalls))
	}
	c := stub.addCalls[0]
	if c.id != 42 {
		t.Errorf("Add entityID = %d, want 42", c.id)
	}
	if c.layer != l.Layer("Player") {
		t.Errorf("Add layer = %b, want %b", c.layer, l.Layer("Player"))
	}
	if c.mask != l.Mask("Player") {
		t.Errorf("Add mask = %b, want %b", c.mask, l.Mask("Player"))
	}
	if c.trigger != false {
		t.Error("Add trigger = true, want false")
	}
}

func TestLayers_Set_routesLayerAndMask(t *testing.T) {
	stub := &stubBackend{}
	l := NewLayers().
		Bind(stub).
		Define("Player", "Monster").
		SetCollides("Player", "Monster")

	shape := AABB{W: 2, H: 4}
	l.Set(7, shape, "Monster", true)

	if len(stub.setCalls) != 1 {
		t.Fatalf("expected 1 Set call, got %d", len(stub.setCalls))
	}
	c := stub.setCalls[0]
	if c.id != 7 {
		t.Errorf("Set entityID = %d, want 7", c.id)
	}
	if c.layer != l.Layer("Monster") {
		t.Errorf("Set layer = %b, want %b", c.layer, l.Layer("Monster"))
	}
	if c.mask != l.Mask("Monster") {
		t.Errorf("Set mask = %b, want %b", c.mask, l.Mask("Monster"))
	}
	if c.trigger != true {
		t.Error("Set trigger = false, want true")
	}
}

func TestLayers_Remove_delegates(t *testing.T) {
	stub := &stubBackend{}
	l := NewLayers().Bind(stub).Define("Player")

	l.Remove(99)

	if len(stub.removeCalls) != 1 || stub.removeCalls[0] != 99 {
		t.Errorf("Remove calls = %v, want [99]", stub.removeCalls)
	}
}

func TestLayers_Add_panicWithNoBackend(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic calling Add without Bind, got none")
		}
	}()
	NewLayers().Define("A").Add(1, Circle{R: 1}, "A", false)
}

func TestLayers_Set_panicWithNoBackend(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic calling Set without Bind, got none")
		}
	}()
	NewLayers().Define("A").Set(1, Circle{R: 1}, "A", false)
}

func TestLayers_Remove_panicWithNoBackend(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic calling Remove without Bind, got none")
		}
	}()
	NewLayers().Remove(1)
}
