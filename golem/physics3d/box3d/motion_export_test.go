package box3d

// MotionMaximumDepthForTest reads fresh geometry after a deliberately faulted step.
func MotionMaximumDepthForTest(w *World) float32 {
	depth := float32(0)
	for a := range w.shapeOwners {
		for z := range w.shapeOwners {
			if a.Index1 < z.Index1 {
				if r, ok := a.PairPenetration(z); ok && r.Depth > depth {
					depth = r.Depth
				}
			}
		}
	}
	return depth
}
