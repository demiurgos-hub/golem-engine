package interest

import "math"

type cellKey struct {
	cx, cy int
}

type pos struct {
	x, y float64
}

// Grid is a spatial hash grid that allows efficient spatial queries.
// Not thread-safe; designed to be used exclusively from the game tick goroutine.
type Grid struct {
	cellSize    float64
	invCellSize float64
	cells       map[cellKey]map[int64]struct{}
	positions   map[int64]pos
	cellOf      map[int64]cellKey
}

// NewGrid creates a spatial hash grid with the given cell size.
func NewGrid(cellSize float64) *Grid {
	return &Grid{
		cellSize:    cellSize,
		invCellSize: 1.0 / cellSize,
		cells:       make(map[cellKey]map[int64]struct{}),
		positions:   make(map[int64]pos),
		cellOf:      make(map[int64]cellKey),
	}
}

func (g *Grid) toCell(x, y float64) cellKey {
	return cellKey{
		cx: int(math.Floor(x * g.invCellSize)),
		cy: int(math.Floor(y * g.invCellSize)),
	}
}

// Insert adds an entity at the given position. If the entity already exists
// it is moved instead.
func (g *Grid) Insert(id int64, x, y float64) {
	if _, exists := g.positions[id]; exists {
		g.Move(id, x, y)
		return
	}
	p := pos{x, y}
	ck := g.toCell(x, y)
	g.positions[id] = p
	g.cellOf[id] = ck
	cell := g.cells[ck]
	if cell == nil {
		cell = make(map[int64]struct{})
		g.cells[ck] = cell
	}
	cell[id] = struct{}{}
}

// Remove deletes an entity from the grid.
func (g *Grid) Remove(id int64) {
	ck, ok := g.cellOf[id]
	if !ok {
		return
	}
	if cell := g.cells[ck]; cell != nil {
		delete(cell, id)
		if len(cell) == 0 {
			delete(g.cells, ck)
		}
	}
	delete(g.positions, id)
	delete(g.cellOf, id)
}

// Move updates an entity's position. Only rehashes when the entity crosses
// a cell boundary.
func (g *Grid) Move(id int64, x, y float64) {
	oldCK, ok := g.cellOf[id]
	if !ok {
		g.Insert(id, x, y)
		return
	}
	g.positions[id] = pos{x, y}
	newCK := g.toCell(x, y)
	if oldCK == newCK {
		return
	}
	if cell := g.cells[oldCK]; cell != nil {
		delete(cell, id)
		if len(cell) == 0 {
			delete(g.cells, oldCK)
		}
	}
	cell := g.cells[newCK]
	if cell == nil {
		cell = make(map[int64]struct{})
		g.cells[newCK] = cell
	}
	cell[id] = struct{}{}
	g.cellOf[id] = newCK
}

// Has reports whether an entity is tracked by the grid.
func (g *Grid) Has(id int64) bool {
	_, ok := g.positions[id]
	return ok
}

// Query returns all entity IDs whose position is within radius of (cx, cy).
// Uses a bounding-box cell scan followed by a squared-distance check.
func (g *Grid) Query(cx, cy, radius float64) []int64 {
	return g.QueryInto(cx, cy, radius, nil)
}

// QueryInto appends all entity IDs within radius of (cx, cy) into dst and
// returns the resulting slice. Callers may pass a reused buffer to avoid
// per-query allocations.
func (g *Grid) QueryInto(cx, cy, radius float64, dst []int64) []int64 {
	r2 := radius * radius
	minCX := int(math.Floor((cx - radius) * g.invCellSize))
	maxCX := int(math.Floor((cx + radius) * g.invCellSize))
	minCY := int(math.Floor((cy - radius) * g.invCellSize))
	maxCY := int(math.Floor((cy + radius) * g.invCellSize))

	result := dst[:0]
	for gx := minCX; gx <= maxCX; gx++ {
		for gy := minCY; gy <= maxCY; gy++ {
			cell := g.cells[cellKey{gx, gy}]
			for id := range cell {
				p := g.positions[id]
				dx := p.x - cx
				dy := p.y - cy
				if dx*dx+dy*dy <= r2 {
					result = append(result, id)
				}
			}
		}
	}
	return result
}
