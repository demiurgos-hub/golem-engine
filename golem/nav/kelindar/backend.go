package navkelindar

import (
	"fmt"

	"github.com/kelindar/tile"
	"github.com/demiurgos-hub/golem-engine/golem/nav"
)

// Backend wraps kelindar/tile's Grid to implement nav.Backend and nav.DynamicBackend.
// It also exposes kelindar-specific functionality on the concrete type: Around for
// BFS radius scans, WriteValue/ReadValue for variable terrain costs, and Grid() for
// direct access to the full kelindar API (observers, save/load, bounded iteration).
//
// All methods are safe for concurrent use: kelindar/tile uses CAS writes and
// per-page spinlocks internally.
//
// Create one with New, NewFromTiledLayer, or NewFromLDtkIntGrid, then pass it
// to Server.SetNavBackend.
type Backend struct {
	grid             *tile.Grid[struct{}]
	cols             int16
	rows             int16
	cellW, cellH     float64
	originX, originY float64
	costFn           func(tile.Value) uint16
}

// GridConfig is the parameter for New.
type GridConfig struct {
	// Cols and Rows are the grid dimensions in cells.
	Cols, Rows int
	// CellW and CellH are the cell size in world units.
	CellW, CellH float64
	// OriginX and OriginY are the world-space coordinates of the top-left
	// corner of the grid. For Tiled maps this is typically (0, 0). For LDtk,
	// use the level's WorldX and WorldY.
	OriginX, OriginY float64
	// CostOf returns the traversal cost for cell (col, row). Return 0 for
	// impassable. The returned value is stored as the tile's raw uint32 value,
	// so WriteValue can update individual cells later without changing CostOf.
	CostOf func(col, row int) uint16
}

// New creates a Backend from a generic grid description.
func New(cfg GridConfig) (*Backend, error) {
	return newBackend(cfg.Cols, cfg.Rows, cfg.CellW, cfg.CellH, cfg.OriginX, cfg.OriginY, func(col, row int) tile.Value {
		if cfg.CostOf == nil {
			return 1
		}
		return tile.Value(cfg.CostOf(col, row))
	})
}

// NewFromTiledLayer builds a Backend directly from the raw fields of a parsed
// Tiled layer. Pass tiled.Map.Width, tiled.Map.Height, tiled.Map.TileWidth,
// tiled.Map.TileHeight, and tiled.Layer.Data (GID slice, row-major).
//
// blocked(gid) returns true for GIDs that are impassable. GID 0 (empty cell)
// is always treated as impassable regardless of blocked.
func NewFromTiledLayer(cols, rows, tileW, tileH int, data []int, blocked func(gid int) bool) (*Backend, error) {
	return newBackend(cols, rows, float64(tileW), float64(tileH), 0, 0, func(col, row int) tile.Value {
		gid := data[row*cols+col]
		if gid == 0 || blocked(gid) {
			return 0
		}
		return 1
	})
}

// NewFromLDtkIntGrid builds a Backend from an LDtk IntGrid layer. Pass
// ldtk.LayerInstance.CellsWide, CellsHigh, GridSize, the level's WorldX and
// WorldY as originX/Y, and ldtk.LayerInstance.IntGridCSV.
//
// blocked(val) returns true for IntGrid values that are impassable. Value 0
// (empty cell) is always treated as impassable.
func NewFromLDtkIntGrid(cols, rows, gridSize int, originX, originY float64, csv []int, blocked func(val int) bool) (*Backend, error) {
	cw := float64(gridSize)
	return newBackend(cols, rows, cw, cw, originX, originY, func(col, row int) tile.Value {
		val := csv[row*cols+col]
		if val == 0 || blocked(val) {
			return 0
		}
		return 1
	})
}

func newBackend(cols, rows int, cellW, cellH, originX, originY float64, tileOf func(col, row int) tile.Value) (*Backend, error) {
	if cols <= 0 || rows <= 0 {
		return nil, fmt.Errorf("nav/kelindar: grid dimensions must be positive, got %dx%d", cols, rows)
	}
	if cellW <= 0 || cellH <= 0 {
		return nil, fmt.Errorf("nav/kelindar: cell dimensions must be positive, got %gx%g", cellW, cellH)
	}
	if cols > 32767 || rows > 32767 {
		return nil, fmt.Errorf("nav/kelindar: grid dimensions must not exceed 32767, got %dx%d", cols, rows)
	}

	// kelindar/tile requires dimensions to be multiples of 3 (3x3 page layout).
	// Round up; cells outside the actual map bounds keep value 0 (impassable).
	w := int16(roundUp3(cols))
	h := int16(roundUp3(rows))

	g := tile.NewGridOf[struct{}](w, h)

	for row := 0; row < rows; row++ {
		for col := 0; col < cols; col++ {
			v := tileOf(col, row)
			if v != 0 {
				g.WriteAt(int16(col), int16(row), v)
			}
		}
	}

	// The cost function is the identity: the tile value IS the traversal cost.
	// Value 0 → impassable; any other value → that cost.
	costFn := func(v tile.Value) uint16 { return uint16(v) }

	return &Backend{
		grid:    g,
		cols:    int16(cols),
		rows:    int16(rows),
		cellW:   cellW,
		cellH:   cellH,
		originX: originX,
		originY: originY,
		costFn:  costFn,
	}, nil
}

// roundUp3 returns the smallest multiple of 3 that is >= n.
func roundUp3(n int) int {
	return ((n + 2) / 3) * 3
}

// FindPath returns world-space waypoints from (x0,y0) to (x1,y1), including
// both the start position and the goal cell centre. Returns nil, false if no
// path exists or either coordinate is outside the grid bounds.
//
// Safe for concurrent use with SetWalkable, WriteValue, and Around.
func (b *Backend) FindPath(x0, y0, x1, y1 float64) ([]nav.Point, bool) {
	sc, sr := b.worldToCell(x0, y0)
	gc, gr := b.worldToCell(x1, y1)

	if !b.inBounds(sc, sr) || !b.inBounds(gc, gr) {
		return nil, false
	}
	if sc == gc && sr == gr {
		return []nav.Point{{X: x0, Y: y0}}, true
	}

	raw, _, found := b.grid.Path(tile.At(sc, sr), tile.At(gc, gr), b.costFn)
	if !found || len(raw) == 0 {
		return nil, false
	}

	points := make([]nav.Point, len(raw))
	// Replace the first point with the caller's exact position so the NPC
	// doesn't snap to the cell centre on the first step.
	points[0] = nav.Point{X: x0, Y: y0}
	for i := 1; i < len(raw); i++ {
		wx, wy := b.cellToWorld(raw[i].X, raw[i].Y)
		points[i] = nav.Point{X: wx, Y: wy}
	}
	return points, true
}

// SetWalkable marks the cell containing world-space coordinate (x, y) as
// passable (true) or impassable (false). Thread-safe; also notifies any
// registered kelindar observers.
func (b *Backend) SetWalkable(x, y float64, walkable bool) {
	col, row := b.worldToCell(x, y)
	if !b.inBounds(col, row) {
		return
	}
	v := tile.Value(0)
	if walkable {
		v = 1
	}
	b.grid.WriteAt(col, row, v)
}

// WriteValue writes a raw tile value at world-space (x, y). Thread-safe.
// The value is used directly as the traversal cost by FindPath and Around:
// 0 means impassable, any other value is the cost. Use this to set variable
// terrain kinds (e.g. road=1, grass=2, swamp=5) at runtime.
// Out-of-bounds coordinates are ignored.
func (b *Backend) WriteValue(x, y float64, v tile.Value) {
	col, row := b.worldToCell(x, y)
	if b.inBounds(col, row) {
		b.grid.WriteAt(col, row, v)
	}
}

// ReadValue returns the current raw tile value at world-space (x, y).
// Returns 0, false if the coordinate is outside the grid.
func (b *Backend) ReadValue(x, y float64) (tile.Value, bool) {
	col, row := b.worldToCell(x, y)
	if !b.inBounds(col, row) {
		return 0, false
	}
	t, ok := b.grid.At(col, row)
	if !ok {
		return 0, false
	}
	return t.Value(), true
}

// Around performs a BFS from world-space (x, y), calling fn for every
// reachable cell within radius cells. costOf maps a raw tile value to a
// traversal cost; return 0 to treat a cell as impassable. Pass nil to use
// the same cost function as FindPath (tile value == cost).
// Out-of-bounds start coordinates are silently ignored.
//
// This has no equivalent in golem.nav/pathing. Typical uses: finding all
// cells reachable within a movement budget, nearest-spawn search, enemy
// awareness radius.
func (b *Backend) Around(x, y float64, radius uint32, costOf func(tile.Value) uint16, fn func(nav.Point)) {
	col, row := b.worldToCell(x, y)
	if !b.inBounds(col, row) {
		return
	}
	if costOf == nil {
		costOf = b.costFn
	}
	b.grid.Around(tile.At(col, row), radius, costOf, func(p tile.Point, _ tile.Tile[struct{}]) {
		wx, wy := b.cellToWorld(p.X, p.Y)
		fn(nav.Point{X: wx, Y: wy})
	})
}

// Grid returns the underlying kelindar/tile Grid for direct access to the
// full kelindar API: Within (bounded tile iteration), NewView (reactive
// tile-change observers), WriteTo/ReadFrom (grid serialisation), MaskAt and
// MergeAt (atomic bit manipulation). Mirrors CpBackend.Space().
func (b *Backend) Grid() *tile.Grid[struct{}] {
	return b.grid
}

// worldToCell converts a world-space position to grid cell coordinates.
func (b *Backend) worldToCell(x, y float64) (int16, int16) {
	col := int16((x - b.originX) / b.cellW)
	row := int16((y - b.originY) / b.cellH)
	return col, row
}

// cellToWorld returns the world-space centre of a grid cell.
func (b *Backend) cellToWorld(col, row int16) (float64, float64) {
	x := b.originX + float64(col)*b.cellW + b.cellW*0.5
	y := b.originY + float64(row)*b.cellH + b.cellH*0.5
	return x, y
}

// inBounds reports whether a grid coordinate falls within the original map bounds.
func (b *Backend) inBounds(col, row int16) bool {
	return col >= 0 && col < b.cols && row >= 0 && row < b.rows
}
