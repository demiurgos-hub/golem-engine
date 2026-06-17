package collision

import "fmt"

// Layers maps named layers to bit indices, maintains a symmetric collision
// matrix, and — when bound to a backend via Bind — acts as a registration
// facade so callers only pass a layer name rather than raw bitmasks.
//
// Typical setup at program start:
//
//	layers := collision.NewLayers().
//	    Bind(backend).
//	    Define("Player", "Enemy", "Wall", "Projectile").
//	    SetCollides("Player", "Wall").
//	    SetCollides("Enemy", "Wall").
//	    SetCollides("Projectile", "Enemy").
//	    SetCollides("Projectile", "Wall")
//
// Then when registering a shape:
//
//	layers.Add(id, collision.Circle{R: 0.5}, "Player", false)
//
// Layer, Mask, and MaskFor remain available for spatial queries:
//
//	ids := srv.OverlapCircle(x, y, r, layers.MaskFor("Enemy"))
type Layers struct {
	indices map[string]int // name → bit index (0–31)
	matrix  [32]uint32     // matrix[i] = bitmask of layer bits that layer i collides with
	backend Backend        // nil until Bind is called
}

// NewLayers creates an empty Layers registry ready for Bind, Define, and SetCollides calls.
func NewLayers() *Layers {
	return &Layers{indices: make(map[string]int)}
}

// Bind attaches a collision backend. Required before calling Add, Set, or Remove.
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
	for _, name := range names {
		if _, exists := l.indices[name]; exists {
			panic(fmt.Sprintf("collision.Layers: layer %q is already defined", name))
		}
		idx := len(l.indices)
		if idx >= 32 {
			panic("collision.Layers: cannot define more than 32 layers")
		}
		l.indices[name] = idx
	}
	return l
}

// SetCollides records that shapes on layer a should test against layer b and
// vice versa. This populates the mask derivation table used by Add and Set:
// after this call, Mask("a") includes b's bit and Mask("b") includes a's bit.
// The rule is symmetric: SetCollides("A", "B") is equivalent to SetCollides("B", "A").
// Returns the receiver for method chaining. Panics if either name has not been
// registered with Define.
func (l *Layers) SetCollides(a, b string) *Layers {
	ai := l.mustIndex(a)
	bi := l.mustIndex(b)
	l.matrix[ai] |= 1 << bi
	l.matrix[bi] |= 1 << ai
	return l
}

// Add registers a collision shape for the given entity on the named layer.
// The layer bit and collision mask are derived automatically from the matrix
// configured via SetCollides. Panics if layerName is undefined or no backend
// has been bound via Bind.
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
// Use this for spatial query layerMask arguments alongside MaskFor.
// Panics if the name has not been registered with Define.
func (l *Layers) Layer(name string) uint32 {
	return 1 << l.mustIndex(name)
}

// Mask returns the collision mask for the named layer: the OR of the bits of
// all layers it has been configured to collide with via SetCollides. This is
// what Add and Set pass as the mask argument to the backend automatically.
// Panics if the name has not been registered with Define.
func (l *Layers) Mask(name string) uint32 {
	return l.matrix[l.mustIndex(name)]
}

// MaskFor returns the OR of the Layer bits for each of the given names. Use
// this to build layerMask arguments for spatial queries such as OverlapBox or
// Raycast. Panics if any name has not been registered with Define.
func (l *Layers) MaskFor(names ...string) uint32 {
	var out uint32
	for _, name := range names {
		out |= 1 << l.mustIndex(name)
	}
	return out
}

// mustIndex returns the bit index for name, panicking if it is not defined.
func (l *Layers) mustIndex(name string) int {
	idx, ok := l.indices[name]
	if !ok {
		panic(fmt.Sprintf("collision.Layers: layer %q is not defined", name))
	}
	return idx
}

// mustBackend panics if no backend has been bound.
func (l *Layers) mustBackend() {
	if l.backend == nil {
		panic("collision.Layers: no backend bound; call Bind before Add, Set, or Remove")
	}
}
