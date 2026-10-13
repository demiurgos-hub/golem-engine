package layerpolicy

import "testing"

func TestTableErrorsAndSymmetry(t *testing.T) {
	table := New()
	if err := table.Define("a", "b"); err != nil {
		t.Fatal(err)
	}
	if err := table.SetCollides("a", "b"); err != nil {
		t.Fatal(err)
	}
	if err := table.SetCollides("a", "a"); err != nil {
		t.Fatal(err)
	}
	if err := table.SetCollides("b", "a"); err != nil {
		t.Fatal(err)
	}
	if l, m, ok := table.Lookup("a"); !ok || l != 1 || m != 3 {
		t.Fatalf("a=%d,%d,%v", l, m, ok)
	}
	if l, m, ok := table.Lookup("b"); !ok || l != 2 || m != 1 {
		t.Fatalf("b=%d,%d,%v", l, m, ok)
	}
	if m, err := table.MaskFor("a", "b", "a"); err != nil || m != 3 {
		t.Fatalf("mask=%d,%v", m, err)
	}
	for _, read := range []func(string) (uint32, error){table.Layer, table.Mask, func(s string) (uint32, error) { return table.MaskFor("a", s) }} {
		if v, err := read("missing"); v != 0 || err == nil || err.Error() != "layer \"missing\" is not defined" {
			t.Fatalf("read=%d,%v", v, err)
		}
	}
	if err := table.SetCollides("missing", "b"); err == nil {
		t.Fatal("missing first name")
	}
	if _, _, ok := (*Table)(nil).Lookup("a"); ok {
		t.Fatal("nil table")
	}
}
