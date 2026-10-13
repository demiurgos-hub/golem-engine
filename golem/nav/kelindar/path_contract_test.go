package navkelindar

import (
	"testing"

	"github.com/kelindar/tile"
)

func TestWeightedPathAndPadding(t *testing.T) {
	b, err := New(GridConfig{Cols: 5, Rows: 2, CellW: 2.5, CellH: 2.5, CostOf: func(col, row int) uint16 {
		if row == 0 && col > 0 && col < 4 {
			return 100
		}
		return 1
	}})
	if err != nil {
		t.Fatal(err)
	}
	points, ok := b.FindPath(.25, .75, 10.5, .5)
	if !ok {
		t.Fatal("no weighted path")
	}
	detour := false
	for _, p := range points[1:] {
		if p.Y == 3.75 {
			detour = true
		}
		if p.Y == 1.25 && p.X > 2.5 && p.X < 10 {
			t.Fatalf("used expensive tile: %v", points)
		}
	}
	if !detour {
		t.Fatalf("no detour: %v", points)
	}
	for _, p := range []tile.Point{tile.At(5, 0), tile.At(0, 2)} {
		v, ok := b.grid.At(p.X, p.Y)
		if !ok || v.Value() != 0 {
			t.Fatalf("padding=%v %v", v, ok)
		}
	}
	b.WriteValue(1, 1, 65536)
	if v, ok := b.ReadValue(1, 1); !ok || v != 65536 || b.costFn(v) != 0 {
		t.Fatalf("raw cost narrowing=%d %v", v, ok)
	}
	b.SetWalkable(1, 1, true)
	if v, _ := b.ReadValue(1, 1); v != 1 {
		t.Fatalf("walkable reset=%d", v)
	}
	if _, err := New(GridConfig{Cols: 32768, Rows: 1, CellW: 1, CellH: 1}); err == nil {
		t.Fatal("expected size error")
	}
	// Characterize the existing overflow without allocating an invalid grid.
	if n := int16(roundUp3(32767)); n >= 0 {
		t.Fatalf("expected int16 rounding overflow, got %d", n)
	}
}
