package box3d

import (
	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
	"sort"
)

func (w *World) orderedActiveBodies() []p.BodyHandle {
	keys := make([]p.BodyHandle, 0, len(w.activeBodies))
	for key := range w.activeBodies {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].Index < keys[j].Index })
	return keys
}

func geometryInBounds(s p.ShapeDesc, pose p.Pose) bool { return s.InBounds(pose) }
func (w *World) bodyInBounds(v *body, pose p.Pose) bool {
	if !pose.Valid() || !pose.Compose(v.desc.Anchor).Position.InBounds() {
		return false
	}
	for _, h := range v.colliders {
		c := w.colliderForHandle(h)
		if c.desc.Enabled && !geometryInBounds(w.shapeForHandle(c.desc.Shape).desc, pose.Compose(c.desc.LocalPose)) {
			return false
		}
	}
	return true
}
