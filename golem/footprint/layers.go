package footprint

import "github.com/demiurgos-hub/golem-engine/golem/collision"

// LayerResolver maps a Unity layer label from footprint YAML to collision
// filtering values. layer is the shape's own layer bitmask; mask is the set of
// layers it collides with. ok is false when the label is unknown.
type LayerResolver func(label string) (layer, mask uint32, ok bool)

// LayersResolver adapts collision.Layers to LayerResolver for 2D placement.
// The same LayerResolver function type is used for 3D placers; callers may
// supply an equivalent matrix for 3D without using collision.Layers.
func LayersResolver(layers *collision.Layers) LayerResolver {
	return func(label string) (layer, mask uint32, ok bool) {
		if layers == nil {
			return 0, 0, false
		}
		return layers.Lookup(label)
	}
}
