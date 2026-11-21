package golem

import (
	"slices"
	"testing"
)

// overlapMob mimics a generated Synced* pointer entity.
type overlapMob struct {
	id int64
}

func (e *overlapMob) EntityID() int64              { return e.id }
func (e *overlapMob) SetEntityID(id int64)         { e.id = id }
func (e *overlapMob) TypeName() string             { return "mob" }
func (e *overlapMob) Position() (float32, float32) { return 0, 0 }
func (e *overlapMob) IsGlobal() bool               { return false }
func (e *overlapMob) FlushUpdate() ([]byte, error) { return nil, nil }
func (e *overlapMob) FullUpdate() ([]byte, error)  { return nil, nil }

// wrappedOverlapMob mimics a gameplay wrapper around a generated entity.
type wrappedOverlapMob struct {
	*overlapMob
}

func (e *wrappedOverlapMob) TypeName() string { return "wrapped-mob" }

// overlapProp is a non-matching entity type for filter tests.
type overlapProp struct {
	id int64
}

func (e *overlapProp) EntityID() int64              { return e.id }
func (e *overlapProp) SetEntityID(id int64)         { e.id = id }
func (e *overlapProp) TypeName() string             { return "prop" }
func (e *overlapProp) Position() (float32, float32) { return 0, 0 }
func (e *overlapProp) IsGlobal() bool               { return false }
func (e *overlapProp) FlushUpdate() ([]byte, error) { return nil, nil }
func (e *overlapProp) FullUpdate() ([]byte, error)  { return nil, nil }

// bareOverlapEntity is a value-type Entity (non-pointer) for assertion coverage.
type bareOverlapEntity struct {
	id int64
}

func (e bareOverlapEntity) EntityID() int64              { return e.id }
func (e bareOverlapEntity) TypeName() string             { return "bare" }
func (e bareOverlapEntity) Position() (float32, float32) { return 0, 0 }
func (e bareOverlapEntity) IsGlobal() bool               { return false }
func (e bareOverlapEntity) FlushUpdate() ([]byte, error) { return nil, nil }
func (e bareOverlapEntity) FullUpdate() ([]byte, error)  { return nil, nil }

func TestOverlapOfType_FiltersPreservesOrderSkipsMissing(t *testing.T) {
	srv := NewServer(ServerConfig{})
	m1 := &overlapMob{}
	p1 := &overlapProp{}
	m2 := &overlapMob{}
	for _, e := range []Entity{m1, p1, m2} {
		if err := srv.CreateEntity(e); err != nil {
			t.Fatalf("CreateEntity: %v", err)
		}
	}

	ids := []int64{m2.EntityID(), 999, p1.EntityID(), m1.EntityID(), m2.EntityID()}
	got := OverlapOfType[*overlapMob](srv, ids)
	want := []*overlapMob{m2, m1, m2}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got[%d] = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestOverlapOfType_NilOrEmptyInput(t *testing.T) {
	srv := NewServer(ServerConfig{})
	if got := OverlapOfType[*overlapMob](srv, nil); got != nil {
		t.Fatalf("nil input = %v, want nil", got)
	}
	if got := OverlapOfType[*overlapMob](srv, []int64{}); got != nil {
		t.Fatalf("empty input = %v, want nil", got)
	}
}

func TestOverlapOfType_NoMatches(t *testing.T) {
	srv := NewServer(ServerConfig{})
	p := &overlapProp{}
	if err := srv.CreateEntity(p); err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}
	got := OverlapOfType[*overlapMob](srv, []int64{p.EntityID(), 42})
	if got != nil {
		t.Fatalf("no matches = %v, want nil", got)
	}
}

func TestOverlapOfType_WrapperAndBare(t *testing.T) {
	srv := NewServer(ServerConfig{})
	inner := &overlapMob{}
	wrap := &wrappedOverlapMob{overlapMob: inner}
	bare := bareOverlapEntity{id: 50}
	if err := srv.CreateEntity(wrap); err != nil {
		t.Fatalf("CreateEntity wrap: %v", err)
	}
	if err := srv.CreateEntity(bare); err != nil {
		t.Fatalf("CreateEntity bare: %v", err)
	}

	ids := []int64{bare.EntityID(), wrap.EntityID()}

	wrappers := OverlapOfType[*wrappedOverlapMob](srv, ids)
	if len(wrappers) != 1 || wrappers[0] != wrap {
		t.Fatalf("wrapper filter = %v, want [%p]", wrappers, wrap)
	}
	// Wrapper embeds *overlapMob but dynamic type is *wrappedOverlapMob.
	if got := OverlapOfType[*overlapMob](srv, []int64{wrap.EntityID()}); got != nil {
		t.Fatalf("inner pointer type must not match wrapper dynamic type, got %v", got)
	}

	bares := OverlapOfType[bareOverlapEntity](srv, ids)
	if len(bares) != 1 || bares[0] != bare {
		t.Fatalf("bare filter = %v, want [%v]", bares, bare)
	}
}

func TestOverlapOfType_NilServerPanics(t *testing.T) {
	for _, ids := range [][]int64{nil, {}, {1}} {
		func() {
			defer func() {
				got := recover()
				if got == nil {
					t.Fatalf("expected panic for nil Server with ids=%v", ids)
				}
				if got != "golem: OverlapOfType: Server must be non-nil" {
					t.Fatalf("panic = %v, want targeted message (ids=%v)", got, ids)
				}
			}()
			_ = OverlapOfType[*overlapMob](nil, ids)
		}()
	}
}

func TestSpaceQueriesOnlySeeTheirSpace(t *testing.T) {
	s, _, _ := twoSpaceCollisionServer(t)
	createIn(t, s, 0, newSpaceCollider(1))
	createIn(t, s, 3, newSpaceCollider(2))
	s.stepCollision(0.05)

	if got := s.OverlapCircle(0, 0, 1, ^uint32(0)); len(got) != 1 || got[0] != 1 {
		t.Fatalf("Server.OverlapCircle = %v, want space 0's entity 1", got)
	}
	if got := s.Space(0).OverlapBox(0, 0, 1, 1, ^uint32(0)); len(got) != 1 || got[0] != 1 {
		t.Fatalf("Space(0).OverlapBox = %v, want entity 1", got)
	}
	if got := s.Space(3).OverlapCircle(0, 0, 1, ^uint32(0)); len(got) != 1 || got[0] != 2 {
		t.Fatalf("Space(3).OverlapCircle = %v, want only entity 2", got)
	}
	if got := s.Space(3).OverlapBox(0, 0, 1, 1, ^uint32(0)); len(got) != 1 || got[0] != 2 {
		t.Fatalf("Space(3).OverlapBox = %v, want only entity 2", got)
	}
	mobs := OverlapOfType[*spaceCollider](s, s.Space(3).OverlapCircle(0, 0, 1, ^uint32(0)))
	if len(mobs) != 1 || mobs[0].id != 2 {
		t.Fatalf("OverlapOfType over a space query = %v", mobs)
	}
}

func TestQueriesOnSpacesWithoutServicesCreateNothing(t *testing.T) {
	s := NewServer(ServerConfig{})
	sp := s.Space(9)
	if sp.OverlapBox(0, 0, 1, 1, 1) != nil || sp.OverlapCircle(0, 0, 1, 1) != nil {
		t.Fatal("overlap on a space without backend returned hits")
	}
	if _, ok := sp.Raycast(0, 0, 1, 1, 1); ok {
		t.Fatal("raycast hit")
	}
	if sp.RaycastAll(0, 0, 1, 1, 1) != nil || sp.BoxCastAll(0, 0, 1, 1, 1, 1, 1) != nil || sp.CircleCastAll(0, 0, 1, 1, 1, 1) != nil {
		t.Fatal("cast-all hit")
	}
	if _, ok := sp.BoxCast(0, 0, 1, 1, 1, 1, 1); ok {
		t.Fatal("box cast hit")
	}
	if _, ok := sp.CircleCast(0, 0, 1, 1, 1, 1); ok {
		t.Fatal("circle cast hit")
	}
	if sp.OverlapBox3D(0, 0, 0, 1, 1, 1, 1) != nil || sp.OverlapSphere(0, 0, 0, 1, 1) != nil || sp.RaycastAll3D(CollisionVec3{}, CollisionVec3{X: 1}, 1) != nil {
		t.Fatal("3D query hit")
	}
	if _, ok := sp.Raycast3D(CollisionVec3{}, CollisionVec3{X: 1}, 1); ok {
		t.Fatal("3D raycast hit")
	}
	if _, ok := sp.FindPath(0, 0, 1, 1); ok {
		t.Fatal("FindPath without backend succeeded")
	}
	sp.SetNavWalkable(0, 0, false)
	sp.SetCollisionBackend(nil)
	sp.SetCollisionLayers(nil)
	sp.SetCollision3DBackend(nil)
	sp.SetCollisionLayers3D(nil)
	sp.SetNavBackend(nil)
	if len(s.spaces) != 0 || len(s.spaceOrder) != 0 {
		t.Fatalf("queries or nil setters created runtimes: %v", s.spaces)
	}
}

func TestSpaceRuntimesStayInAscendingOrder(t *testing.T) {
	s := NewServer(ServerConfig{})
	for _, id := range []SpaceID{7, 2, 9, 4} {
		s.Space(id).SetCollisionBackend(newPointBackend2D())
	}
	s.Space(4).SetNavBackend(&navAgentBackend{})
	var got []SpaceID
	for _, rt := range s.spaceOrder {
		got = append(got, rt.id)
	}
	if want := []SpaceID{2, 4, 7, 9}; !slices.Equal(got, want) || len(s.spaces) != 4 {
		t.Fatalf("runtime order %v, want %v", got, want)
	}
	if s.runtime(0) != &s.space0 || s.Space(0).OverlapBox(0, 0, 1, 1, 1) != nil {
		t.Fatal("space 0 runtime is not the embedded one")
	}
}
