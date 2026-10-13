package navkelindar

import (
	"reflect"
	"strings"
	"testing"

	"github.com/demiurgos-hub/golem-engine/golem/nav"
)

func TestMapInputAndOrigin(t *testing.T) {
	for _, ldtk := range []bool{false, true} {
		var calls []int
		blocked := func(v int) bool { calls = append(calls, v); return v == 9 }
		data := []int{1, 2, 0, 3, 9, 4, 99}
		var b *Backend
		var err error
		ox, oy := 0.0, 0.0
		if ldtk {
			ox, oy = 10, -20
			b, err = NewFromLDtkIntGrid(3, 2, 4, ox, oy, data, blocked)
		} else {
			b, err = NewFromTiledLayer(3, 2, 4, 6, data, blocked)
		}
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(calls, []int{1, 2, 3, 9, 4}) {
			t.Fatalf("calls=%v", calls)
		}
		h := 6.0
		if ldtk {
			h = 4
		}
		// Both zero and explicitly blocked cells are unreachable as goals.
		for _, goal := range []nav.Point{{X: ox + 9, Y: oy + 1}, {X: ox + 5, Y: oy + h + 1}} {
			if path, ok := b.FindPath(ox+1, oy+1, goal.X, goal.Y); ok || path != nil {
				t.Fatalf("blocked goal: %v,%v", path, ok)
			}
		}
		points, ok := b.FindPath(ox+.25, oy+.75, ox+5, oy+1)
		want := []nav.Point{{X: ox + .25, Y: oy + .75}, {X: ox + 6, Y: oy + h/2}}
		if !ok || !reflect.DeepEqual(points, want) {
			t.Fatalf("path=%v,%v want %v", points, ok, want)
		}
	}
}

func TestMapInputErrorAndCallbackContract(t *testing.T) {
	called := false
	if _, err := NewFromTiledLayer(0, 1, 0, 0, nil, func(int) bool { called = true; return false }); err == nil || !strings.Contains(err.Error(), "grid dimensions") || called {
		t.Fatalf("error=%v called=%v", err, called)
	}
	if _, err := NewFromLDtkIntGrid(1, 1, 0, 0, 0, nil, nil); err == nil || !strings.Contains(err.Error(), "cell dimensions") {
		t.Fatalf("error=%v", err)
	}
	if _, err := NewFromTiledLayer(1, 1, 2, 2, []int{0}, nil); err != nil {
		t.Fatal(err)
	}
	for _, data := range [][]int{nil, {1}} {
		func() {
			defer func() {
				if recover() == nil {
					t.Fatal("expected short-data or nil-callback panic")
				}
			}()
			_, _ = NewFromTiledLayer(1, 1, 2, 2, data, nil)
		}()
	}
}

func TestEndpointPolicyContract(t *testing.T) {
	b, err := NewFromLDtkIntGrid(3, 1, 3, 10, 20, []int{0, 1, 1}, func(int) bool { return false })
	if err != nil {
		t.Fatal(err)
	}
	// Preserve the existing same-cell shortcut even on blocked cells.
	points, ok := b.FindPath(10.25, 20.5, 11, 21)
	if !ok || !reflect.DeepEqual(points, []nav.Point{{X: 10.25, Y: 20.5}}) {
		t.Fatalf("same blocked cell=%v,%v", points, ok)
	}
	// Preserve leaving an impassable starting cell.
	points, ok = b.FindPath(10.25, 20.5, 17, 21)
	if !ok || points[0] != (nav.Point{X: 10.25, Y: 20.5}) {
		t.Fatalf("blocked start=%v,%v", points, ok)
	}
	wantGoal := nav.Point{X: 17.5, Y: 21.5}
	if points[len(points)-1] != wantGoal {
		t.Fatalf("odd-size centre=%v", points)
	}
	points, ok = b.FindPath(9.9, 19.9, 10.1, 20.1)
	if !ok || points[0] != (nav.Point{X: 9.9, Y: 19.9}) {
		t.Fatalf("truncation=%v,%v", points, ok)
	}
	if path, ok := b.FindPath(6, 20, 17, 21); ok || path != nil {
		t.Fatalf("out of bounds=%v,%v", path, ok)
	}
	b.SetWalkable(14, 21, false)
	if path, ok := b.FindPath(10.25, 20.5, 17, 21); ok || path != nil {
		t.Fatalf("unreachable=%v,%v", path, ok)
	}
}

func BenchmarkMapConstruction(b *testing.B) {
	data := make([]int, 32*24)
	for i := range data {
		data[i] = i % 5
	}
	blocked := func(v int) bool { return v == 3 }
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := NewFromTiledLayer(32, 24, 4, 4, data, blocked); err != nil {
			b.Fatal(err)
		}
	}
}
