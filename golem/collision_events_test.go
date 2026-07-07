package golem

import (
	"testing"

	"github.com/demiurgos-hub/golem-engine/golem/registry"
	"github.com/demiurgos-hub/golem-engine/golem/collision"
)

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
