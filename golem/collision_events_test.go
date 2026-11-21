package golem

import (
	"fmt"
	"math"
	"reflect"
	"slices"
	"testing"

	"github.com/demiurgos-hub/golem-engine/golem/collision"
	"github.com/demiurgos-hub/golem-engine/golem/registry"
)

type contactCallbackEntity struct {
	stubEntity
	observe func(string, Entity)
}

func (e *contactCallbackEntity) OnTriggerEnter(other Entity) { e.observe("trigger enter", other) }
func (e *contactCallbackEntity) OnTriggerStay(other Entity)  { e.observe("trigger stay", other) }
func (e *contactCallbackEntity) OnTriggerExit(other Entity)  { e.observe("trigger exit", other) }
func (e *contactCallbackEntity) OnCollisionEnter(other Entity, _ CollisionVec2, _ float64) {
	e.observe("solid enter", other)
}
func (e *contactCallbackEntity) OnCollisionStay(other Entity, _ CollisionVec2, _ float64) {
	e.observe("solid stay", other)
}
func (e *contactCallbackEntity) OnCollisionExit(other Entity) { e.observe("solid exit", other) }

func TestContactDuplicateKeepsLastOrientation(t *testing.T) {
	a, b := &stubEntity{id: 1}, &stubEntity{id: 2}
	s := newTestServer(a, b)
	s.dispatchContactEvents([]collision.Contact{
		{A: 1, B: 2, Normal: collision.Vec2{X: 1}, Depth: 1},
		{A: 2, B: 1, Normal: collision.Vec2{Y: 1}, Depth: 2},
	})
	if len(a.collisionEnters) != 1 || len(b.collisionEnters) != 1 {
		t.Fatal("duplicate contacts dispatched more than once")
	}
	if a.collisionEnters[0].normal != (CollisionVec2{Y: -1}) || b.collisionEnters[0].normal != (CollisionVec2{Y: 1}) || a.collisionEnters[0].depth != 2 {
		t.Fatalf("canonical pair changed normal orientation: A=%+v B=%+v", a.collisionEnters, b.collisionEnters)
	}
}

func TestContactCallbacksResolveBothEndpointsBeforeDispatch(t *testing.T) {
	for _, depth := range []float64{0, 1} {
		t.Run(fmt.Sprint(depth), func(t *testing.T) {
			s := NewServer(ServerConfig{})
			s.EnableContactEvents()
			a := &contactCallbackEntity{stubEntity: stubEntity{id: 1}}
			b := &contactCallbackEntity{stubEntity: stubEntity{id: 2}}
			var gotA, gotB []Entity
			a.observe = func(_ string, other Entity) {
				gotA = append(gotA, other)
				s.DeleteEntity(2)
			}
			b.observe = func(_ string, other Entity) { gotB = append(gotB, other) }
			for _, e := range []Entity{a, b} {
				if err := s.CreateEntity(e); err != nil {
					t.Fatal(err)
				}
			}
			s.dispatchContactEvents([]collision.Contact{{A: 1, B: 2, Depth: depth}})
			if len(gotA) != 1 || gotA[0] != b || len(gotB) != 1 || gotB[0] != a {
				t.Fatalf("deletion changed already resolved endpoints: A=%v B=%v", gotA, gotB)
			}
			s.dispatchContactEvents(nil)
			if len(gotA) != 2 || gotA[1] != nil || len(gotB) != 1 {
				t.Fatalf("deleted partner exit: A=%v B=%v", gotA, gotB)
			}
		})
	}
}

func TestContactPhaseOrderAndPairPublication(t *testing.T) {
	s := NewServer(ServerConfig{})
	s.EnableContactEvents()
	var order []string
	previousCount := 0
	for _, id := range []int64{1, 2} {
		e := &contactCallbackEntity{stubEntity: stubEntity{id: id}}
		e.observe = func(phase string, _ Entity) {
			order = append(order, fmt.Sprintf("%d %s", id, phase))
			if len(s.triggerPairs) != previousCount || len(s.solidPairs) != previousCount {
				t.Error("pair sets published before both phases finished")
			}
		}
		if err := s.CreateEntity(e); err != nil {
			t.Fatal(err)
		}
	}
	solid := collision.Contact{A: 1, B: 2, Depth: 1}
	s.dispatchContactEvents([]collision.Contact{solid, {A: 1, B: 2}})
	previousCount = 1
	s.dispatchContactEvents([]collision.Contact{solid})
	want := []string{"1 trigger enter", "2 trigger enter", "1 solid enter", "2 solid enter", "1 trigger exit", "2 trigger exit", "1 solid stay", "2 solid stay"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("order=%v want=%v", order, want)
	}
}

func TestContactCallbackCanResetTracking(t *testing.T) {
	s := NewServer(ServerConfig{})
	s.EnableContactEvents()
	var phases []string
	a := &contactCallbackEntity{stubEntity: stubEntity{id: 1}, observe: func(phase string, _ Entity) {
		phases = append(phases, phase)
		if phase == "trigger stay" {
			s.EnableContactEvents()
		}
	}}
	for _, e := range []Entity{a, &stubEntity{id: 2}, &stubEntity{id: 3}} {
		if err := s.CreateEntity(e); err != nil {
			t.Fatal(err)
		}
	}
	contacts := []collision.Contact{{A: 1, B: 2}, {A: 1, B: 3}}
	s.dispatchContactEvents(contacts)
	phases = nil
	s.dispatchContactEvents(contacts)
	// Whichever pair visits first is a stay; its callback resets tracking for
	// the other pair. Pair iteration order is deliberately not prescribed.
	if !reflect.DeepEqual(phases, []string{"trigger stay", "trigger enter"}) {
		t.Fatalf("tracking reset was ignored: %v", phases)
	}
}

// stubEntity is a minimal Entity for collision event tests.
type stubEntity struct {
	id int64

	// recorded calls — one entry per callback invocation
	triggerEnters []Entity
	triggerStays  []Entity
	triggerExits  []Entity

	collisionEnters []collisionCall
	collisionStays  []collisionCall
	collisionExits  []Entity
}

type collisionCall struct {
	other  Entity
	normal CollisionVec2
	depth  float64
}

func (e *stubEntity) EntityID() int64              { return e.id }
func (e *stubEntity) TypeName() string             { return "stub" }
func (e *stubEntity) Position() (float32, float32) { return 0, 0 }
func (e *stubEntity) IsGlobal() bool               { return false }
func (e *stubEntity) FlushUpdate() ([]byte, error) { return nil, nil }
func (e *stubEntity) FullUpdate() ([]byte, error)  { return nil, nil }

func (e *stubEntity) OnTriggerEnter(other Entity) { e.triggerEnters = append(e.triggerEnters, other) }
func (e *stubEntity) OnTriggerStay(other Entity)  { e.triggerStays = append(e.triggerStays, other) }
func (e *stubEntity) OnTriggerExit(other Entity)  { e.triggerExits = append(e.triggerExits, other) }
func (e *stubEntity) OnCollisionEnter(other Entity, normal CollisionVec2, depth float64) {
	e.collisionEnters = append(e.collisionEnters, collisionCall{other, normal, depth})
}
func (e *stubEntity) OnCollisionStay(other Entity, normal CollisionVec2, depth float64) {
	e.collisionStays = append(e.collisionStays, collisionCall{other, normal, depth})
}
func (e *stubEntity) OnCollisionExit(other Entity) {
	e.collisionExits = append(e.collisionExits, other)
}

// newTestServer creates a server with contact events enabled and the given
// entities pre-registered. It does not start the tick loop.
func newTestServer(entities ...*stubEntity) *Server {
	s := &Server{reg: registry.NewRegistry()}
	s.EnableContactEvents()
	for _, e := range entities {
		_ = s.reg.Add(e)
	}
	return s
}

// --- trigger events ---

func TestTriggerEnterAndStayAndExit(t *testing.T) {
	eA := &stubEntity{id: 1}
	eB := &stubEntity{id: 2}
	s := newTestServer(eA, eB)

	triggerContact := collision.Contact{A: 1, B: 2, Depth: 0}

	// tick 1 — both entities newly overlap: Enter fires
	s.dispatchContactEvents([]collision.Contact{triggerContact})
	if len(eA.triggerEnters) != 1 || eA.triggerEnters[0] != Entity(eB) {
		t.Errorf("tick1: eA OnTriggerEnter expected eB, got %v", eA.triggerEnters)
	}
	if len(eB.triggerEnters) != 1 || eB.triggerEnters[0] != Entity(eA) {
		t.Errorf("tick1: eB OnTriggerEnter expected eA, got %v", eB.triggerEnters)
	}
	if len(eA.triggerStays) != 0 {
		t.Errorf("tick1: unexpected OnTriggerStay on eA")
	}

	// tick 2 — still overlapping: Stay fires, not Enter
	s.dispatchContactEvents([]collision.Contact{triggerContact})
	if len(eA.triggerEnters) != 1 {
		t.Errorf("tick2: eA OnTriggerEnter called again unexpectedly")
	}
	if len(eA.triggerStays) != 1 {
		t.Errorf("tick2: eA OnTriggerStay expected 1 call, got %d", len(eA.triggerStays))
	}
	if eA.triggerStays[0] != Entity(eB) {
		t.Errorf("tick2: eA OnTriggerStay expected eB")
	}

	// tick 3 — no contacts: Exit fires
	s.dispatchContactEvents(nil)
	if len(eA.triggerExits) != 1 || eA.triggerExits[0] != Entity(eB) {
		t.Errorf("tick3: eA OnTriggerExit expected eB, got %v", eA.triggerExits)
	}
	if len(eB.triggerExits) != 1 || eB.triggerExits[0] != Entity(eA) {
		t.Errorf("tick3: eB OnTriggerExit expected eA, got %v", eB.triggerExits)
	}
	// no extra Stay after Exit
	if len(eA.triggerStays) != 1 {
		t.Errorf("tick3: unexpected extra OnTriggerStay on eA")
	}

	// tick 4 — still no contacts: nothing new fires
	s.dispatchContactEvents(nil)
	if len(eA.triggerExits) != 1 {
		t.Errorf("tick4: eA OnTriggerExit called again when no overlap")
	}
}

// --- solid collision events ---

func TestCollisionEnterStayExit(t *testing.T) {
	eA := &stubEntity{id: 10}
	eB := &stubEntity{id: 20}
	s := newTestServer(eA, eB)

	normal := collision.Vec2{X: 1, Y: 0}
	solidContact := collision.Contact{A: 10, B: 20, Normal: normal, Depth: 2.5}

	// tick 1 — Enter
	s.dispatchContactEvents([]collision.Contact{solidContact})

	if len(eA.collisionEnters) != 1 {
		t.Fatalf("tick1: eA OnCollisionEnter expected 1 call")
	}
	callA := eA.collisionEnters[0]
	if callA.other != Entity(eB) {
		t.Errorf("tick1: eA OnCollisionEnter other expected eB")
	}
	if callA.normal != (CollisionVec2{X: 1, Y: 0}) {
		t.Errorf("tick1: eA normal expected {1,0}, got %v", callA.normal)
	}
	if callA.depth != 2.5 {
		t.Errorf("tick1: eA depth expected 2.5, got %v", callA.depth)
	}

	// B receives negated normal
	if len(eB.collisionEnters) != 1 {
		t.Fatalf("tick1: eB OnCollisionEnter expected 1 call")
	}
	callB := eB.collisionEnters[0]
	if callB.normal != (CollisionVec2{X: -1, Y: 0}) {
		t.Errorf("tick1: eB normal expected {-1,0}, got %v", callB.normal)
	}

	// tick 2 — Stay
	s.dispatchContactEvents([]collision.Contact{solidContact})
	if len(eA.collisionEnters) != 1 {
		t.Errorf("tick2: eA OnCollisionEnter called again unexpectedly")
	}
	if len(eA.collisionStays) != 1 {
		t.Errorf("tick2: eA OnCollisionStay expected 1 call, got %d", len(eA.collisionStays))
	}

	// tick 3 — Exit (zero contacts)
	s.dispatchContactEvents(nil)
	if len(eA.collisionExits) != 1 {
		t.Fatalf("tick3: eA OnCollisionExit expected 1 call")
	}
	if eA.collisionExits[0] != Entity(eB) {
		t.Errorf("tick3: eA OnCollisionExit other expected eB")
	}
}

// --- zero-contact tick fires Exit when active pairs exist ---

func TestZeroContactTickFiresExit(t *testing.T) {
	eA := &stubEntity{id: 1}
	eB := &stubEntity{id: 2}
	s := newTestServer(eA, eB)

	// establish an active trigger overlap
	s.dispatchContactEvents([]collision.Contact{{A: 1, B: 2, Depth: 0}})
	if len(eA.triggerEnters) != 1 {
		t.Fatal("setup: Enter not fired")
	}

	// passing a nil slice must still fire Exit
	s.dispatchContactEvents(nil)
	if len(eA.triggerExits) != 1 {
		t.Errorf("nil contacts: eA OnTriggerExit expected 1, got %d", len(eA.triggerExits))
	}

	// passing empty (non-nil) slice must also fire Exit (re-enter first)
	s.dispatchContactEvents([]collision.Contact{{A: 1, B: 2, Depth: 0}}) // re-enter
	s.dispatchContactEvents([]collision.Contact{})                       // explicit empty
	if len(eA.triggerExits) != 2 {
		t.Errorf("empty contacts: eA OnTriggerExit expected 2 total, got %d", len(eA.triggerExits))
	}
}

// --- deleted entity: other is nil in Exit callback ---

func TestDeletedEntityNilOtherOnExit(t *testing.T) {
	eA := &stubEntity{id: 1}
	eB := &stubEntity{id: 2}
	s := newTestServer(eA, eB)

	// establish overlap
	s.dispatchContactEvents([]collision.Contact{{A: 1, B: 2, Depth: 0}})

	// remove eB from registry before next tick
	s.reg.DeleteEntity(2)

	// next tick no contacts — eA should receive Exit with nil other
	s.dispatchContactEvents(nil)
	if len(eA.triggerExits) != 1 {
		t.Fatalf("eA OnTriggerExit expected 1 call after partner deleted")
	}
	if eA.triggerExits[0] != nil {
		t.Errorf("eA OnTriggerExit: other expected nil for deleted entity, got %v", eA.triggerExits[0])
	}
}

// --- canonical pair key: {A,B} same as {B,A} ---

func TestContactKeySymmetric(t *testing.T) {
	k1 := contactKey(1, 2)
	k2 := contactKey(2, 1)
	if k1 != k2 {
		t.Errorf("contactKey(1,2)=%v != contactKey(2,1)=%v", k1, k2)
	}
	if k1[0] > k1[1] {
		t.Errorf("contactKey: smaller ID should be first, got %v", k1)
	}
}

// --- trigger and solid pairs tracked independently ---

func TestTriggerAndSolidTrackedSeparately(t *testing.T) {
	eA := &stubEntity{id: 1}
	eB := &stubEntity{id: 2}
	s := newTestServer(eA, eB)

	triggerC := collision.Contact{A: 1, B: 2, Depth: 0}
	solidC := collision.Contact{A: 1, B: 2, Normal: collision.Vec2{X: 0, Y: 1}, Depth: 1}

	// both contacts in tick 1 — Enter for both types
	s.dispatchContactEvents([]collision.Contact{triggerC, solidC})
	if len(eA.triggerEnters) != 1 {
		t.Errorf("expected 1 TriggerEnter, got %d", len(eA.triggerEnters))
	}
	if len(eA.collisionEnters) != 1 {
		t.Errorf("expected 1 CollisionEnter, got %d", len(eA.collisionEnters))
	}

	// tick 2: only trigger remains — trigger Stay, collision Exit
	s.dispatchContactEvents([]collision.Contact{triggerC})
	if len(eA.triggerStays) != 1 {
		t.Errorf("tick2: expected 1 TriggerStay, got %d", len(eA.triggerStays))
	}
	if len(eA.collisionExits) != 1 {
		t.Errorf("tick2: expected 1 CollisionExit, got %d", len(eA.collisionExits))
	}
	if len(eA.triggerExits) != 0 {
		t.Errorf("tick2: unexpected TriggerExit")
	}
}

// --- per-space legacy collision ---

// pointBackend2D is a tiny 2D backend: registered entities collide when their
// synced positions are within 1 unit and their layer masks match.
type pointBackend2D struct {
	shapes    map[int64]pointShape
	pos       map[int64][2]float64
	updated   []int64
	steps     int
	writeBack map[int64][2]float64
}

type pointShape struct {
	layer, mask uint32
	trigger     bool
}

func newPointBackend2D() *pointBackend2D {
	return &pointBackend2D{shapes: map[int64]pointShape{}, pos: map[int64][2]float64{}, writeBack: map[int64][2]float64{}}
}

func (b *pointBackend2D) Add(id int64, _ collision.Shape, layer, mask uint32, trigger bool) {
	b.shapes[id] = pointShape{layer, mask, trigger}
}
func (b *pointBackend2D) Set(id int64, _ collision.Shape, layer, mask uint32, trigger bool) {
	b.shapes[id] = pointShape{layer, mask, trigger}
}
func (b *pointBackend2D) Remove(id int64) { delete(b.shapes, id); delete(b.pos, id) }
func (b *pointBackend2D) Update(id int64, x, y float64) {
	b.pos[id] = [2]float64{x, y}
	b.updated = append(b.updated, id)
}
func (b *pointBackend2D) ids() []int64 {
	var ids []int64
	for id := range b.shapes {
		if _, ok := b.pos[id]; ok {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	return ids
}
func (b *pointBackend2D) near(id int64, x, y, r float64) bool {
	p := b.pos[id]
	return math.Hypot(p[0]-x, p[1]-y) <= r
}
func (b *pointBackend2D) Step(float64) []collision.Contact {
	b.steps++
	ids := b.ids()
	var out []collision.Contact
	for i, a := range ids {
		for _, c := range ids[i+1:] {
			sa, sc := b.shapes[a], b.shapes[c]
			if sa.layer&sc.mask == 0 && sc.layer&sa.mask == 0 {
				continue
			}
			if p := b.pos[c]; !b.near(a, p[0], p[1], 1) {
				continue
			}
			depth := 1.0
			if sa.trigger || sc.trigger {
				depth = 0
			}
			out = append(out, collision.Contact{A: a, B: c, Normal: collision.Vec2{X: 1}, Depth: depth})
		}
	}
	return out
}
func (b *pointBackend2D) ReadBack(fn func(int64, float64, float64)) {
	for id, p := range b.writeBack {
		fn(id, p[0], p[1])
	}
}
func (b *pointBackend2D) OverlapCircle(cx, cy, r float64, mask uint32) []int64 {
	var out []int64
	for _, id := range b.ids() {
		if b.shapes[id].layer&mask != 0 && b.near(id, cx, cy, r) {
			out = append(out, id)
		}
	}
	return out
}
func (b *pointBackend2D) OverlapBox(cx, cy, hw, hh float64, mask uint32) []int64 {
	var out []int64
	for _, id := range b.ids() {
		p := b.pos[id]
		if b.shapes[id].layer&mask != 0 && math.Abs(p[0]-cx) <= hw && math.Abs(p[1]-cy) <= hh {
			out = append(out, id)
		}
	}
	return out
}

// spaceCollider is a ColliderProvider with a settable position that records
// its contact events.
type spaceCollider struct {
	stubEntity
	x, y  float32
	layer string
}

func (e *spaceCollider) Position() (float32, float32) { return e.x, e.y }
func (e *spaceCollider) SetPosition(x, y float32)     { e.x, e.y = x, y }
func (e *spaceCollider) Collider() (CollisionShape, string, bool) {
	return CollisionCircle{R: 0.5}, e.layer, false
}

func newSpaceCollider(id int64) *spaceCollider {
	return &spaceCollider{stubEntity: stubEntity{id: id}, layer: "Body"}
}

// twoSpaceCollisionServer configures 2D collision on space 0 and space 3.
func twoSpaceCollisionServer(t *testing.T) (*Server, *pointBackend2D, *pointBackend2D) {
	t.Helper()
	s := NewServer(ServerConfig{})
	b0, b3 := newPointBackend2D(), newPointBackend2D()
	s.SetCollisionBackend(b0)
	s.SetCollisionLayers(NewCollisionLayers().Bind(b0).Define("Body").SetCollides("Body", "Body"))
	s.Space(3).SetCollisionBackend(b3)
	s.Space(3).SetCollisionLayers(NewCollisionLayers().Bind(b3).Define("Body").SetCollides("Body", "Body"))
	return s, b0, b3
}

func createIn(t *testing.T, s *Server, space SpaceID, e Entity) {
	t.Helper()
	if err := s.CreateEntityIn(space, e); err != nil {
		t.Fatal(err)
	}
}

func TestCollisionStaysWithinEachSpace(t *testing.T) {
	s, b0, b3 := twoSpaceCollisionServer(t)
	a, b, c := newSpaceCollider(1), newSpaceCollider(2), newSpaceCollider(3)
	createIn(t, s, 0, a)
	createIn(t, s, 3, b)
	createIn(t, s, 3, c)
	var calls [][]collision.Contact
	s.OnContact(func(contacts []collision.Contact) { calls = append(calls, slices.Clone(contacts)) })

	s.stepCollision(0.05)
	if !reflect.DeepEqual(b0.updated, []int64{1}) {
		t.Fatalf("space 0 backend updated %v, want only entity 1", b0.updated)
	}
	if got := slices.Sorted(slices.Values(b3.updated)); !reflect.DeepEqual(got, []int64{2, 3}) {
		t.Fatalf("space 3 backend updated %v, want entities 2 and 3", got)
	}
	want := []collision.Contact{{A: 2, B: 3, Normal: collision.Vec2{X: 1}, Depth: 1}}
	if len(calls) != 1 || !reflect.DeepEqual(calls[0], want) {
		t.Fatalf("contacts %v, want one call with %v (entity 1 shares coordinates but not the space)", calls, want)
	}
}

func TestCollisionMergesContactsOfAllSpacesIntoOneCallback(t *testing.T) {
	s, _, _ := twoSpaceCollisionServer(t)
	s.EnableContactEvents()
	es := []*spaceCollider{newSpaceCollider(1), newSpaceCollider(2), newSpaceCollider(3), newSpaceCollider(4)}
	createIn(t, s, 3, es[0])
	createIn(t, s, 3, es[1])
	createIn(t, s, 0, es[2])
	createIn(t, s, 0, es[3])
	var calls [][]collision.Contact
	s.OnContact(func(contacts []collision.Contact) { calls = append(calls, slices.Clone(contacts)) })

	s.stepCollision(0.05)
	want := []collision.Contact{
		{A: 3, B: 4, Normal: collision.Vec2{X: 1}, Depth: 1},
		{A: 1, B: 2, Normal: collision.Vec2{X: 1}, Depth: 1},
	}
	if len(calls) != 1 || !reflect.DeepEqual(calls[0], want) {
		t.Fatalf("contacts %v, want one call with space 0 then space 3: %v", calls, want)
	}
	for _, e := range es {
		if len(e.collisionEnters) != 1 {
			t.Fatalf("entity %d got %d collision enters, want 1", e.id, len(e.collisionEnters))
		}
	}
}

func TestMovedColliderExitsOldPairsAndEntersNewOnes(t *testing.T) {
	s, b0, b3 := twoSpaceCollisionServer(t)
	s.EnableContactEvents()
	a, mover, b := newSpaceCollider(1), newSpaceCollider(2), newSpaceCollider(3)
	createIn(t, s, 0, a)
	createIn(t, s, 0, mover)
	createIn(t, s, 3, b)
	s.stepCollision(0.05)
	if len(a.collisionEnters) != 1 || a.collisionEnters[0].other != Entity(mover) {
		t.Fatalf("space 0 pair did not enter: %+v", a.collisionEnters)
	}

	if err := s.SetEntitySpace(2, 3); err != nil {
		t.Fatal(err)
	}
	if _, ok := b0.shapes[2]; ok {
		t.Fatal("moved collider stayed in space 0's backend")
	}
	if _, ok := b3.shapes[2]; !ok {
		t.Fatal("moved collider is missing from space 3's backend")
	}
	s.stepCollision(0.05)
	if len(a.collisionExits) != 1 || a.collisionExits[0] != Entity(mover) {
		t.Fatalf("old pair did not exit: %+v", a.collisionExits)
	}
	if len(mover.collisionExits) != 1 || len(mover.collisionEnters) != 2 || mover.collisionEnters[1].other != Entity(b) {
		t.Fatalf("mover exits %v enters %+v, want exit from 1 and enter with 3", mover.collisionExits, mover.collisionEnters)
	}
	if len(b.collisionEnters) != 1 || b.collisionEnters[0].other != Entity(mover) {
		t.Fatalf("new pair did not enter: %+v", b.collisionEnters)
	}
}

func TestCollisionReadBackOnlyWritesEntitiesOfTheSpace(t *testing.T) {
	s, b0, b3 := twoSpaceCollisionServer(t)
	a, b := newSpaceCollider(1), newSpaceCollider(2)
	createIn(t, s, 0, a)
	createIn(t, s, 3, b)
	b3.writeBack[1] = [2]float64{9, 9} // a space-0 entity reported by space 3
	b3.writeBack[2] = [2]float64{4, 5}
	b0.writeBack[2] = [2]float64{7, 7} // a space-3 entity reported by space 0
	s.stepCollision(0.05)
	if a.x != 0 || a.y != 0 {
		t.Fatalf("space 3 backend moved space 0 entity to (%g,%g)", a.x, a.y)
	}
	if b.x != 4 || b.y != 5 {
		t.Fatalf("space 3 entity at (%g,%g), want its own backend result (4,5)", b.x, b.y)
	}
}

func TestCollisionWithoutSpacesUsesThePlainEntityList(t *testing.T) {
	s, b0, b3 := twoSpaceCollisionServer(t)
	createIn(t, s, 0, newSpaceCollider(1))
	createIn(t, s, 0, newSpaceCollider(2))
	s.stepCollision(0.05)
	if s.spacedScratch != nil {
		t.Fatal("single-space tick built the spaced entity list")
	}
	if len(b0.updated) != 2 || len(b3.updated) != 0 {
		t.Fatalf("updates space0=%v space3=%v", b0.updated, b3.updated)
	}
	if b3.steps != 1 {
		t.Fatalf("empty space 3 stepped %d times, want 1", b3.steps)
	}
}

func TestCollision3DStaysWithinEachSpace(t *testing.T) {
	s := NewServer(ServerConfig{})
	b0, b7 := NewCollisionSimple3DBackend(), NewCollisionSimple3DBackend()
	s.SetCollision3DBackend(b0)
	s.Space(7).SetCollision3DBackend(b7)
	b0.Add(1, CollisionSphere{R: 1}, 1, 1, false)
	b7.Add(2, CollisionSphere{R: 1}, 1, 1, false)
	b7.Add(3, CollisionSphere{R: 1}, 1, 1, false)
	createIn(t, s, 0, &entity3D{id: 1})
	createIn(t, s, 7, &entity3D{id: 2})
	createIn(t, s, 7, &entity3D{id: 3, x: 0.5})
	var calls [][]CollisionContact3D
	s.OnContact3D(func(contacts []CollisionContact3D) { calls = append(calls, slices.Clone(contacts)) })
	s.stepCollision(0.05)
	if len(calls) != 1 || len(calls[0]) == 0 {
		t.Fatalf("got OnContact3D calls %v, want one call with the space 7 pair", calls)
	}
	for _, c := range calls[0] {
		if c.A == 1 || c.B == 1 {
			t.Fatalf("space 0 entity collided across spaces: %+v", calls[0])
		}
	}
	if got := s.Space(7).OverlapSphere(0, 0, 0, 0.1, 1); slices.Contains(got, 1) || !slices.Contains(got, 2) {
		t.Fatalf("space 7 OverlapSphere = %v", got)
	}
	if got := s.OverlapSphere(0, 0, 0, 0.1, 1); !reflect.DeepEqual(got, []int64{1}) {
		t.Fatalf("space 0 OverlapSphere = %v", got)
	}
}

// With many spaces, one pass over the entities feeds every backend: each
// backend is updated with exactly its own entities, each steps once, and each
// reads back only into its own entities. Entities in a space without a
// backend are skipped.
func TestCollisionManySpacesUpdateOnlyTheirOwnEntities(t *testing.T) {
	const spaces = 7
	s := NewServer(ServerConfig{})
	backends := make([]*pointBackend2D, spaces)
	for space := range backends {
		b := newPointBackend2D()
		backends[space] = b
		sp := s.Space(SpaceID(space))
		sp.SetCollisionBackend(b)
		sp.SetCollisionLayers(NewCollisionLayers().Bind(b).Define("Body").SetCollides("Body", "Body"))
	}
	// Interleaved, so entities of one space do not follow each other.
	want := make([][]int64, spaces)
	colliders := map[int64]*spaceCollider{}
	for id := int64(1); id <= 5*spaces; id++ {
		space := int(id) % spaces
		c := newSpaceCollider(id)
		c.x = float32(id) * 10
		colliders[id] = c
		createIn(t, s, SpaceID(space), c)
		want[space] = append(want[space], id)
	}
	stray := newSpaceCollider(1000)
	createIn(t, s, 99, stray) // a space without collision services
	for space, b := range backends {
		// Each backend reports a neighbour space's entity too.
		b.writeBack[want[space][0]] = [2]float64{float64(space), 1}
		b.writeBack[want[(space+1)%spaces][1]] = [2]float64{-5, -5}
	}

	for tick := 1; tick <= 2; tick++ {
		for _, b := range backends {
			b.updated = nil
		}
		s.stepCollision(0.05)
		for space, b := range backends {
			if got := slices.Sorted(slices.Values(b.updated)); !slices.Equal(got, want[space]) {
				t.Fatalf("tick %d: space %d backend updated %v, want %v", tick, space, got, want[space])
			}
			if b.steps != tick {
				t.Fatalf("tick %d: space %d backend stepped %d times", tick, space, b.steps)
			}
			if first := colliders[want[space][0]]; first.x != float32(space) || first.y != 1 {
				t.Fatalf("tick %d: space %d entity at (%g,%g), want its backend's (%d,1)", tick, space, first.x, first.y, space)
			}
			if second := colliders[want[space][1]]; second.x == -5 {
				t.Fatalf("tick %d: space %d entity was moved by another space's backend", tick, space)
			}
		}
	}
	if stray.x != 0 || stray.y != 0 {
		t.Fatal("an entity in a space without a backend was moved")
	}
}
