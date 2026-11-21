package world

import (
	"bytes"
	"errors"
	"testing"
)

type testData struct {
	name    string
	payload []byte
	err     error
}

func (d *testData) WorldName() string { return d.name }
func (d *testData) MarshalUpdate() ([]byte, error) {
	if d.err != nil {
		return nil, d.err
	}
	return d.payload, nil
}

func TestMarshalAllExceptOmitsNamesAndKeepsSortedOrder(t *testing.T) {
	store := NewStore()
	store.Set(&testData{name: "zeta", payload: []byte("z")})
	store.Set(&testData{name: "alpha", payload: []byte("a")})
	store.Set(&testData{name: "secret", payload: []byte("s")})

	got, err := store.MarshalAllExcept(map[string]struct{}{"secret": {}})
	if err != nil {
		t.Fatalf("MarshalAllExcept: %v", err)
	}
	want := [][]byte{[]byte("a"), []byte("z")}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if !bytes.Equal(got[i], want[i]) {
			t.Fatalf("got[%d] = %q, want %q", i, got[i], want[i])
		}
	}

	all, err := store.MarshalAll()
	if err != nil {
		t.Fatalf("MarshalAll: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("MarshalAll len = %d, want 3", len(all))
	}
	if !bytes.Equal(all[0], []byte("a")) || !bytes.Equal(all[1], []byte("s")) || !bytes.Equal(all[2], []byte("z")) {
		t.Fatalf("MarshalAll order = %v", all)
	}
}

func TestMarshalAllExceptNilExcludeMatchesMarshalAll(t *testing.T) {
	store := NewStore()
	store.Set(&testData{name: "b", payload: []byte("b")})
	store.Set(&testData{name: "a", payload: []byte("a")})

	all, err := store.MarshalAll()
	if err != nil {
		t.Fatalf("MarshalAll: %v", err)
	}
	except, err := store.MarshalAllExcept(nil)
	if err != nil {
		t.Fatalf("MarshalAllExcept(nil): %v", err)
	}
	if len(all) != len(except) {
		t.Fatalf("len mismatch: %d vs %d", len(all), len(except))
	}
	for i := range all {
		if !bytes.Equal(all[i], except[i]) {
			t.Fatalf("index %d: %q vs %q", i, all[i], except[i])
		}
	}
}

func TestMarshalAllExceptPropagatesMarshalError(t *testing.T) {
	store := NewStore()
	boom := errors.New("marshal boom")
	store.Set(&testData{name: "a", payload: []byte("a")})
	store.Set(&testData{name: "b", err: boom})

	_, err := store.MarshalAllExcept(nil)
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want %v", err, boom)
	}
}

func TestNamesReturnsSortedNames(t *testing.T) {
	store := NewStore()
	if got := store.Names(); got != nil {
		t.Fatalf("empty Names = %v, want nil", got)
	}
	store.Set(&testData{name: "zeta"})
	store.Set(&testData{name: "alpha"})
	store.Set(&testData{name: "mid"})
	store.Set(&testData{name: "alpha", payload: []byte("again")})
	got := store.Names()
	want := []string{"alpha", "mid", "zeta"}
	if len(got) != len(want) {
		t.Fatalf("Names = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Names = %v, want %v", got, want)
		}
	}
	got[0] = "mutated"
	if store.Names()[0] != "alpha" {
		t.Fatal("Names returned the store's own slice")
	}
}

func TestStoreVersionAdvancesOnSet(t *testing.T) {
	s := NewStore()
	if s.Version() != 0 {
		t.Fatalf("new store version = %d", s.Version())
	}
	before := s.Version()
	_ = s.Get("zone")
	_ = s.Names()
	if s.Version() != before {
		t.Fatal("reading changed the version")
	}
	s.Set(&testData{name: "zone"})
	afterFirst := s.Version()
	s.Set(&testData{name: "zone"})
	if afterFirst == before || s.Version() == afterFirst {
		t.Fatalf("versions %d, %d, %d: every Set must advance", before, afterFirst, s.Version())
	}
}
