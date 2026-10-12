package golem

import (
	"fmt"
	"reflect"
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
