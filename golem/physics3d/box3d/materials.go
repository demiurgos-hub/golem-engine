package box3d

import (
	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
	"math"
)

func combine(a, b float32, mode p.CombineMode, restitution bool) float32 {
	switch mode {
	case p.Average:
		return (a + b) * .5
	case p.Minimum:
		return min(a, b)
	case p.Multiply:
		return a * b
	case p.Maximum:
		return max(a, b)
	default:
		if restitution {
			return max(a, b)
		}
		return float32(math.Sqrt(float64(a * b)))
	}
}
func (w *World) initMaterials() {
	w.materials = map[uint64]p.Material{}
	w.materialKeys = map[p.Material]uint64{}
	w.backend.SetFrictionCallback(func(a float32, ai uint64, b float32, bi uint64) float32 {
		return combine(a, b, max(w.materials[ai].FrictionCombine, w.materials[bi].FrictionCombine), false)
	})
	w.backend.SetRestitutionCallback(func(a float32, ai uint64, b float32, bi uint64) float32 {
		return combine(a, b, max(w.materials[ai].RestitutionCombine, w.materials[bi].RestitutionCombine), true)
	})
}
func (w *World) registerMaterial(m p.Material) (p.Material, error) {
	if m.FrictionCombine > p.Maximum || m.RestitutionCombine > p.Maximum {
		return m, p.ErrInvalid
	}
	if m.ID != 0 {
		if old, ok := w.materials[m.ID]; ok && old != m {
			return m, p.ErrInvalid
		}
		w.materials[m.ID] = m
		return m, nil
	}
	if id, ok := w.materialKeys[m]; ok {
		m.ID = id
		return m, nil
	}
	key := m
	for {
		w.nextMaterial++
		id := (uint64(1) << 63) | w.nextMaterial
		if _, ok := w.materials[id]; !ok {
			m.ID = id
			break
		}
	}
	w.materialKeys[key] = m.ID
	w.materials[m.ID] = m
	return m, nil
}
