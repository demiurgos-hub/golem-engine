package interest

import (
	"math/rand"
	"testing"
)

func TestGridMembershipLifecycle(t *testing.T) {
	g := NewGrid(2)
	expected := map[int64]pos{}
	rng := rand.New(rand.NewSource(42))
	for step := 0; step < 1000; step++ {
		id := int64(rng.Intn(20))
		p := pos{float64(rng.Intn(20)-10) / 2, float64(rng.Intn(20)-10) / 2}
		switch rng.Intn(3) {
		case 0:
			g.Insert(id, p.x, p.y)
			expected[id] = p
		case 1:
			g.Move(id, p.x, p.y)
			expected[id] = p
		case 2:
			g.Remove(id)
			delete(expected, id)
		}
		if len(g.positions) != len(expected) || len(g.cellOf) != len(expected) {
			t.Fatalf("step %d: indexes disagree", step)
		}
		count := 0
		for key, cell := range g.cells {
			if len(cell) == 0 {
				t.Fatal("empty cell retained")
			}
			for member := range cell {
				count++
				want, ok := expected[member]
				if !ok || g.positions[member] != want || g.cellOf[member] != key || key != g.toCell(want.x, want.y) {
					t.Fatalf("stale member %d", member)
				}
			}
		}
		if count != len(expected) {
			t.Fatal("membership count differs")
		}
	}
	for id := range expected {
		g.Remove(id)
	}
	if len(g.cells) != 0 || len(g.positions) != 0 || len(g.cellOf) != 0 {
		t.Fatal("removal did not prune indexes")
	}
}

func TestGridSameCellPositionAndNegativeBoundary(t *testing.T) {
	g := NewGrid(2)
	g.Insert(1, -.1, -.1)
	if g.cellOf[1] != (cellKey{-1, -1}) {
		t.Fatal("negative coordinates must floor")
	}
	g.Move(1, -1.5, -1.5)
	if got := g.Query(-1.5, -1.5, 0); len(got) != 1 || got[0] != 1 {
		t.Fatalf("same-cell position not updated: %v", got)
	}
	if got := g.Query(-.1, -.1, 0); len(got) != 0 {
		t.Fatalf("stale position: %v", got)
	}
	g.Insert(1, 0, 0)
	if _, ok := g.cells[cellKey{-1, -1}]; ok {
		t.Fatal("old cell retained")
	}
	if allocs := testing.AllocsPerRun(100, func() { g.Move(1, .5, .5) }); allocs != 0 {
		t.Fatalf("same-cell allocations=%g", allocs)
	}
}

func BenchmarkGridMove(b *testing.B) {
	for _, cross := range []bool{false, true} {
		name := "same-cell"
		if cross {
			name = "cross-cell"
		}
		b.Run(name, func(b *testing.B) {
			g := NewGrid(2)
			g.Insert(1, 0, 0)
			g.Insert(2, 2, 0)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				x := float64(i%2) * .5
				if cross {
					x *= 4
				}
				g.Move(1, x, 0)
			}
		})
	}
}
