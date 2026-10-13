// Package layerpolicy implements dimension-independent named collision layers.
package layerpolicy

import "fmt"

// Table assigns names to at most 32 layer bits and stores a symmetric collision
// matrix. Use New to initialize it. A Table is not safe for concurrent mutation.
type Table struct {
	indices map[string]int
	matrix  [32]uint32
}

// New creates an empty named-layer table.
func New() Table { return Table{indices: make(map[string]int)} }

// Define assigns consecutive bits to names. On error, earlier names from the
// same call remain defined. Names are used exactly as supplied.
func (t *Table) Define(names ...string) error {
	for _, name := range names {
		if _, exists := t.indices[name]; exists {
			return fmt.Errorf("layer %q is already defined", name)
		}
		idx := len(t.indices)
		if idx >= 32 {
			return fmt.Errorf("cannot define more than 32 layers")
		}
		t.indices[name] = idx
	}
	return nil
}

// SetCollides adds a symmetric collision rule, including self-collision when
// a equals b. Both names must be defined; an error leaves the matrix unchanged.
func (t *Table) SetCollides(a, b string) error {
	ai, err := t.index(a)
	if err != nil {
		return err
	}
	bi, err := t.index(b)
	if err != nil {
		return err
	}
	t.matrix[ai] |= 1 << bi
	t.matrix[bi] |= 1 << ai
	return nil
}

// Layer returns the named layer's single bit, or an error for an unknown name.
func (t *Table) Layer(name string) (uint32, error) {
	idx, err := t.index(name)
	if err != nil {
		return 0, err
	}
	return 1 << idx, nil
}

// Mask returns the named layer's collision mask, or an error for an unknown name.
func (t *Table) Mask(name string) (uint32, error) {
	idx, err := t.index(name)
	if err != nil {
		return 0, err
	}
	return t.matrix[idx], nil
}

// MaskFor combines the layer bits of names, or returns an error for the first
// unknown name. An empty list produces zero.
func (t *Table) MaskFor(names ...string) (uint32, error) {
	var out uint32
	for _, name := range names {
		bit, err := t.Layer(name)
		if err != nil {
			return 0, err
		}
		out |= bit
	}
	return out, nil
}

// Lookup returns the named layer bit and collision mask. Nil tables and unknown
// names return zero values and false.
func (t *Table) Lookup(name string) (layer, mask uint32, ok bool) {
	if t == nil {
		return 0, 0, false
	}
	idx, ok := t.indices[name]
	if !ok {
		return 0, 0, false
	}
	return 1 << idx, t.matrix[idx], true
}

func (t *Table) index(name string) (int, error) {
	idx, ok := t.indices[name]
	if !ok {
		return 0, fmt.Errorf("layer %q is not defined", name)
	}
	return idx, nil
}
