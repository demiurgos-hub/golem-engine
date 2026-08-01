package golem

import "testing"

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
