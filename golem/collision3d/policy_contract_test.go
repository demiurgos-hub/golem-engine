package collision3d

import (
	"fmt"
	"testing"
)

func TestLayersBindingAndRegistrationSnapshot(t *testing.T) {
	first, second := &stubBackend3D{}, &stubBackend3D{}
	l := NewLayers().Bind(first).Define("a", "b")
	shape := Sphere{R: 2}
	l.Add(1, shape, "a", true)
	l.SetCollides("a", "b")
	if len(first.addCalls) != 1 || first.addCalls[0].mask != 0 || len(first.setCalls) != 0 {
		t.Fatal("policy change modified registrations")
	}
	l.Bind(second).Set(1, shape, "a", false)
	l.Remove(1)
	if len(second.setCalls) != 1 || second.setCalls[0].shape != shape || second.setCalls[0].layer != 1 || second.setCalls[0].mask != 2 || second.setCalls[0].trigger {
		t.Fatalf("rebound set=%v", second.setCalls)
	}
	if len(first.removeCalls) != 0 || len(second.removeCalls) != 1 {
		t.Fatal("rebind migrated or removed old backend")
	}
}

func wantLayerPanic(t *testing.T, want string, fn func()) {
	t.Helper()
	defer func() {
		if got := recover(); got != want {
			t.Fatalf("panic = %v, want %q", got, want)
		}
	}()
	fn()
}

func TestLayersPolicyContract(t *testing.T) {
	if (*Layers)(nil).Define() != nil || (*Layers)(nil).MaskFor() != 0 {
		t.Fatal("empty operations on nil receiver")
	}
	l := NewLayers()
	names := make([]string, 32)
	for i := range names {
		names[i] = fmt.Sprint(i)
	}
	l.Define(names...)
	for i, name := range names {
		if got := l.Layer(name); got != uint32(1)<<i {
			t.Fatalf("bit %d = %x", i, got)
		}
	}
	wantLayerPanic(t, "collision3d.Layers: layer \"0\" is already defined", func() { l.Define("0") })
	wantLayerPanic(t, "collision3d.Layers: cannot define more than 32 layers", func() { l.Define("overflow") })
	partial := NewLayers().Define("")
	wantLayerPanic(t, "collision3d.Layers: layer \"A\" is already defined", func() { partial.Define("A", "A", "B") })
	if _, _, ok := partial.Lookup("A"); !ok {
		t.Fatal("lost successful partial definition")
	}
	if _, _, ok := partial.Lookup("B"); ok {
		t.Fatal("defined after failure")
	}
	wantLayerPanic(t, "collision3d.Layers: layer \"missing\" is not defined", func() { partial.SetCollides("A", "missing") })
	if partial.Mask("A") != 0 {
		t.Fatal("failed SetCollides mutated mask")
	}
	if partial.MaskFor() != 0 {
		t.Fatal("empty mask")
	}
	if _, _, ok := (*Layers)(nil).Lookup("A"); ok {
		t.Fatal("nil lookup")
	}
	var zero Layers
	if _, _, ok := zero.Lookup("A"); ok {
		t.Fatal("zero lookup")
	}
	wantLayerPanic(t, "collision3d.Layers: layer \"missing\" is not defined", func() { zero.Layer("missing") })
	wantLayerPanic(t, "collision3d.Layers: no backend bound; call Bind before Add, Set, or Remove", func() { partial.Add(1, Sphere{R: 1}, "missing", false) })
	wantLayerPanic(t, "collision3d.Layers: no backend bound; call Bind before Add, Set, or Remove", func() { partial.Set(1, Sphere{R: 1}, "missing", false) })
}
