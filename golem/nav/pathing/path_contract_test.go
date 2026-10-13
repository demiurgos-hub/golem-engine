package navpathing

import (
	"testing"

	"github.com/demiurgos-hub/golem-engine/golem/nav"
)

func TestPathChainingAndFractionalCellSize(t *testing.T) {
	b, err := New(GridConfig{Cols: 130, Rows: 1, CellW: 3.9, CellH: 3.9, Walkable: func(int, int) bool { return true }})
	if err != nil {
		t.Fatal(err)
	}
	points, ok := b.FindPath(.25, .75, 388, 1)
	if !ok || len(points) != 130 {
		t.Fatalf("chained path len=%d ok=%v", len(points), ok)
	}
	if points[0] != (nav.Point{X: .25, Y: .75}) {
		t.Fatalf("start=%v", points[0])
	}
	for i := 1; i < len(points); i++ {
		if points[i] != (nav.Point{X: float64(i*3 + 1), Y: 1}) {
			t.Fatalf("point %d=%v", i, points[i])
		}
	}
}
