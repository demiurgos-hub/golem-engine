package navmesh

import "github.com/demiurgos-hub/golem-engine/golem/nav/surface"

// SameSupport compares retained polygon identity without selecting a new floor.
func (l location) SameSupport(other surface.Location) bool {
	r, ok := other.(location)
	return ok && l.view != nil && l.view == r.view && !l.view.released.Load() && l.value.SamePolygon(r.value)
}

var _ surface.SupportIdentity = location{}
