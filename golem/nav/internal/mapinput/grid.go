// Package mapinput interprets row-major Tiled and LDtk values for nav adapters.
package mapinput

// RowMajor borrows map values during synchronous backend construction.
// Grid dimensions, origins, and validation remain the backend's responsibility.
type RowMajor struct {
	Cols    int
	Values  []int
	Blocked func(int) bool
}

// Walkable reports whether a cell has a nonzero value not rejected by Blocked.
// Zero cells do not call Blocked. Invalid indexes and a nil Blocked for nonzero
// cells retain the constructors' existing panic behavior.
func (s *RowMajor) Walkable(col, row int) bool {
	value := s.Values[row*s.Cols+col]
	return value != 0 && !s.Blocked(value)
}
