package collision3d

import "github.com/demiurgos-hub/golem-engine/golem/collision/layerpolicy"

// Layers maps named layers to bit indices, maintains a symmetric collision
// matrix, and — when bound to a backend via Bind — acts as a registration
// facade so callers only pass a layer name rather than raw bitmasks.
//
// Semantics match collision.Layers for 2D backends.
type Layers struct {
	table   layerpolicy.Table
	backend Backend // nil until Bind is called
}

// NewLayers creates an empty Layers registry ready for Bind, Define, and SetCollides calls.
func NewLayers() *Layers {
	return &Layers{table: layerpolicy.New()}
}

// Bind attaches a 3D collision backend. Required before calling Add, Set, or Remove.
// Returns the receiver for method chaining.
func (l *Layers) Bind(b Backend) *Layers {
	l.backend = b
	return l
}

// Define registers one or more layer names, assigning them consecutive bit
// indices starting after the last registered layer. Returns the receiver for
// method chaining. Panics if any name is already registered or if the total
// number of defined layers would exceed 32.
func (l *Layers) Define(names ...string) *Layers {
	if len(names) == 0 {
		return l
	}
	if err := l.table.Define(names...); err != nil {
		panic("collision3d.Layers: " + err.Error())
	}
	return l
}

// SetCollides records that shapes on layer a should test against layer b and
// vice versa. The rule is symmetric: SetCollides("A", "B") is equivalent to
// SetCollides("B", "A"). Returns the receiver for method chaining. Panics if
// either name has not been registered with Define.
func (l *Layers) SetCollides(a, b string) *Layers {
	if err := l.table.SetCollides(a, b); err != nil {
		panic("collision3d.Layers: " + err.Error())
	}
	return l
}

// Add registers a collision shape for the given entity on the named layer.
// Panics if layerName is undefined or no backend has been bound via Bind.
func (l *Layers) Add(entityID int64, shape Shape, layerName string, trigger bool) {
	l.mustBackend()
	l.backend.Add(entityID, shape, l.Layer(layerName), l.Mask(layerName), trigger)
}

// Set replaces the collision shape and trigger flag for a registered entity,
// re-deriving the layer bit and mask from layerName. Panics if layerName is
// undefined or no backend has been bound via Bind.
func (l *Layers) Set(entityID int64, shape Shape, layerName string, trigger bool) {
	l.mustBackend()
	l.backend.Set(entityID, shape, l.Layer(layerName), l.Mask(layerName), trigger)
}

// Remove unregisters the entity's collision shape from the backend.
// Panics if no backend has been bound via Bind.
func (l *Layers) Remove(entityID int64) {
	l.mustBackend()
	l.backend.Remove(entityID)
}

// Layer returns the single-bit uint32 mask identifying the named layer.
// Panics if the name has not been registered with Define.
func (l *Layers) Layer(name string) uint32 {
	value, err := l.table.Layer(name)
	if err != nil {
		panic("collision3d.Layers: " + err.Error())
	}
	return value
}

// Mask returns the collision mask for the named layer: the OR of the bits of
// all layers it has been configured to collide with via SetCollides.
// Panics if the name has not been registered with Define.
func (l *Layers) Mask(name string) uint32 {
	value, err := l.table.Mask(name)
	if err != nil {
		panic("collision3d.Layers: " + err.Error())
	}
	return value
}

// Lookup returns the layer bit and collision mask for name.
// ok is false when name has not been registered with Define.
func (l *Layers) Lookup(name string) (layer, mask uint32, ok bool) {
	if l == nil {
		return 0, 0, false
	}
	return l.table.Lookup(name)
}

// MaskFor returns the OR of the Layer bits for each of the given names.
// Panics if any name has not been registered with Define.
func (l *Layers) MaskFor(names ...string) uint32 {
	if len(names) == 0 {
		return 0
	}
	value, err := l.table.MaskFor(names...)
	if err != nil {
		panic("collision3d.Layers: " + err.Error())
	}
	return value
}

func (l *Layers) mustBackend() {
	if l.backend == nil {
		panic("collision3d.Layers: no backend bound; call Bind before Add, Set, or Remove")
	}
}
