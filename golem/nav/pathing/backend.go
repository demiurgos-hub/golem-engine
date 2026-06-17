package navpathing

import (
	"fmt"

	"github.com/quasilyte/pathing"
	"golem.nav"
)

// Backend wraps quasilyte/pathing's AStar to implement nav.Backend.
// It also implements nav.DynamicBackend: SetWalkable updates the grid at runtime.
//
// Create one with New, NewFromTiledLayer, or NewFromLDtkIntGrid, then pass it
// to Server.SetNavBackend.
type Backend struct {
	grid    *pathing.Grid
	astar   *pathing.AStar
	layer   pathing.GridLayer
	cols    int
	rows    int
	cellW   float64
	cellH   float64
	originX float64
	originY float64
}

// GridConfig is the parameter for New.
type GridConfig struct {
	// Cols and Rows are the grid dimensions in cells.
	Cols, Rows int
	// CellW and CellH are the cell size in world units. Must be positive
	// integer values (fractional parts are truncated by the underlying library).
	CellW, CellH float64
	// OriginX and OriginY are the world-space coordinates of the top-left
	// corner of the grid. For Tiled maps this is typically (0, 0). For LDtk,
	// use the level's WorldX and WorldY.
	OriginX, OriginY float64
	// Walkable reports whether the cell at (col, row) is passable.
	// col is in [0, Cols) and row is in [0, Rows).
	Walkable func(col, row int) bool
}

// New creates a Backend from a generic grid description.
func New(cfg GridConfig) (*Backend, error) {
	return newBackend(cfg.Cols, cfg.Rows, cfg.CellW, cfg.CellH, cfg.OriginX, cfg.OriginY, cfg.Walkable)
}

// NewFromTiledLayer builds a Backend directly from the raw fields of a parsed
// Tiled layer. Pass tiled.Map.Width, tiled.Map.Height, tiled.Map.TileWidth,
// tiled.Map.TileHeight, and tiled.Layer.Data (GID slice, row-major).
//
// blocked(gid) returns true for GIDs that are impassable. GID 0 (empty cell)
// is always treated as impassable regardless of blocked.
func NewFromTiledLayer(cols, rows, tileW, tileH int, data []int, blocked func(gid int) bool) (*Backend, error) {
	return newBackend(cols, rows, float64(tileW), float64(tileH), 0, 0, func(col, row int) bool {
		gid := data[row*cols+col]
		if gid == 0 {
			return false
		}
		return !blocked(gid)
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
	return newBackend(cols, rows, cw, cw, originX, originY, func(col, row int) bool {
		val := csv[row*cols+col]
		if val == 0 {
			return false
		}
		return !blocked(val)
	})
}

func newBackend(cols, rows int, cellW, cellH, originX, originY float64, walkable func(col, row int) bool) (*Backend, error) {
	if cols <= 0 || rows <= 0 {
		return nil, fmt.Errorf("nav/pathing: grid dimensions must be positive, got %dx%d", cols, rows)
	}
	if cellW <= 0 || cellH <= 0 {
		return nil, fmt.Errorf("nav/pathing: cell dimensions must be positive, got %gx%g", cellW, cellH)
	}

	g := pathing.NewGrid(pathing.GridConfig{
		WorldWidth:  uint(cols) * uint(cellW),
		WorldHeight: uint(rows) * uint(cellH),
		CellWidth:   uint(cellW),
		CellHeight:  uint(cellH),
	})

	// Mark impassable cells using the IsBlocked bit. All tile tags stay at 0.
	// The layer maps tag 0 → cost 1 (passable), and the blocked bit overrides
	// any cost to 0 (impassable) for flagged cells.
	for row := 0; row < rows; row++ {
		for col := 0; col < cols; col++ {
			if !walkable(col, row) {
				g.SetCellIsBlocked(pathing.GridCoord{X: col, Y: row}, true)
			}
		}
	}

	astar := pathing.NewAStar(pathing.AStarConfig{
		NumCols: uint(g.NumCols()),
		NumRows: uint(g.NumRows()),
	})

	// Tag 0 (default) → cost 1. The IsBlocked bit in the cell byte maps to
	// the second half of the GridLayer ([2]uint64), which is all zeros, giving
	// blocked cells a cost of 0 (impassable) regardless of their tile tag.
	layer := pathing.MakeGridLayer([8]uint8{0: 1})

	return &Backend{
		grid:    g,
		astar:   astar,
		layer:   layer,
		cols:    cols,
		rows:    rows,
		cellW:   cellW,
		cellH:   cellH,
		originX: originX,
		originY: originY,
	}, nil
}

// FindPath returns world-space waypoints from (x0,y0) to (x1,y1), including
// both the start position and the goal cell centre. Returns nil, false if no
// path exists or either coordinate is outside the grid bounds.
//
// Paths longer than 56 cells are handled transparently by chaining multiple
// BuildPath calls; the caller receives the complete path in one slice.
//
// FindPath is safe for concurrent use as long as SetWalkable is not called
// concurrently: the grid is read-only during pathfinding.
func (b *Backend) FindPath(x0, y0, x1, y1 float64) ([]nav.Point, bool) {
	start := b.worldToCell(x0, y0)
	goal := b.worldToCell(x1, y1)

	if !b.inBounds(start) || !b.inBounds(goal) {
		return nil, false
	}
	if start == goal {
		return []nav.Point{{X: x0, Y: y0}}, true
	}

	points := []nav.Point{{X: x0, Y: y0}}
	current := start

	for {
		result := b.astar.BuildPath(b.grid, current, goal, b.layer)

		if result.Steps.Len() == 0 {
			// Zero steps with Partial=true means goal is unreachable.
			return nil, false
		}

		// Walk direction deltas, tracking the current cell and collecting
		// the world-space centre of each cell visited.
		for result.Steps.HasNext() {
			switch result.Steps.Next() {
			case pathing.DirRight:
				current.X++
			case pathing.DirLeft:
				current.X--
			case pathing.DirDown:
				current.Y++
			case pathing.DirUp:
				current.Y--
			}
			wx, wy := b.cellToWorld(current)
			points = append(points, nav.Point{X: wx, Y: wy})
		}

		if !result.Partial {
			// Path is complete; goal was reached within the 56-step window.
			break
		}
		// result.Partial == true: the path hit the 56-step cap before reaching
		// the goal. Continue from the current cell.
	}

	return points, true
}

// SetWalkable marks the cell containing world-space coordinate (x, y) as
// passable (true) or impassable (false). Out-of-bounds coordinates are ignored.
//
// Not safe for concurrent use with FindPath. Callers must synchronise if
// SetWalkable is called from a different goroutine than FindPath.
func (b *Backend) SetWalkable(x, y float64, walkable bool) {
	coord := b.worldToCell(x, y)
	if b.inBounds(coord) {
		b.grid.SetCellIsBlocked(coord, !walkable)
	}
}

// worldToCell converts a world-space position to the grid cell that contains it.
// The origin offset is subtracted before delegating to the grid's PosToCoord,
// which performs integer division by cell size.
func (b *Backend) worldToCell(x, y float64) pathing.GridCoord {
	return b.grid.PosToCoord(x-b.originX, y-b.originY)
}

// cellToWorld returns the world-space centre of a grid cell, applying the
// origin offset to the grid's CoordToPos result.
func (b *Backend) cellToWorld(c pathing.GridCoord) (float64, float64) {
	cx, cy := b.grid.CoordToPos(c)
	return cx + b.originX, cy + b.originY
}

// inBounds reports whether a grid coordinate falls within the grid.
func (b *Backend) inBounds(c pathing.GridCoord) bool {
	return c.X >= 0 && c.X < b.cols && c.Y >= 0 && c.Y < b.rows
}
