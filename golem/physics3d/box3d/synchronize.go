package box3d

import p "github.com/demiurgos-hub/golem-engine/golem/physics3d"

func (w *World) changedBody(h p.BodyHandle) {
	w.changed()
	// Owned statics can lose their owner before synchronization. Retain the exact
	// dirty handle even while activeBodies would also refresh it.
	if v := w.bodyForHandle(h); v != nil && v.desc.Mode == p.Static {
		if w.dirtyStatics == nil {
			w.dirtyStatics = make(map[p.BodyHandle]struct{})
		}
		w.dirtyStatics[h] = struct{}{}
	}
}

func (w *World) synchronizeBodies() {
	// A joint or body mutation may wake other members of an island. Refresh all
	// movable/owned bodies, including sleeping bodies, at the existing boundary.
	for h := range w.activeBodies {
		v := w.bodyForHandle(h)
		v.committed = w.bodyState(h, v)
	}
	for h := range w.dirtyStatics {
		if _, active := w.activeBodies[h]; active {
			continue
		}
		if v := w.bodyForHandle(h); v != nil {
			v.committed = w.bodyState(h, v)
		}
	}
	// Initial construction may mark the entire static world. Do not retain that
	// sparse table and scan its old capacity for every later one-object edit.
	if len(w.dirtyStatics) > 128 {
		w.dirtyStatics = nil
	} else {
		clear(w.dirtyStatics)
	}
}
