package box3d

import p "github.com/demiurgos-hub/golem-engine/golem/physics3d"

func (w *World) claimHandle() (p.Handle, error) {
	if count := len(w.freeHandles); count > 0 {
		index := w.freeHandles[count-1]
		w.freeHandles = w.freeHandles[:count-1]
		return p.Handle{World: w.id, Index: index, Generation: w.generations[index]}, nil
	}
	if len(w.generations) == 0 {
		w.generations = append(w.generations, 0)
		w.handleBindings = append(w.handleBindings, portableHandleBinding{})
	}
	if uint64(len(w.generations)) > uint64(^uint32(0)) {
		return p.Handle{}, p.ErrInvalid
	}
	index := uint32(len(w.generations))
	w.generations = append(w.generations, 1)
	w.handleBindings = append(w.handleBindings, portableHandleBinding{})
	return p.Handle{World: w.id, Index: index, Generation: 1}, nil
}

func (w *World) releaseHandle(h p.Handle) {
	w.handleBindings[h.Index] = portableHandleBinding{}
	// A wrapped generation must never revive an old handle. Retire that slot.
	if w.generations[h.Index] == ^uint32(0) {
		return
	}
	w.generations[h.Index]++
	w.freeHandles = append(w.freeHandles, h.Index)
}
