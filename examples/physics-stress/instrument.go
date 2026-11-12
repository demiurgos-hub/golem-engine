package main

import (
	"path/filepath"

	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
	box "github.com/demiurgos-hub/golem-engine/golem/physics3d/box3d"
)

// This wrapper records failed proposals unchanged, at the public physics seam.
// It does not alter motion policy or make an unsupported frame look accepted.
type observedWorld struct {
	*box.World
	out string
}

var (
	_ p.PlacementValidator = observedWorld{}
	_ p.OwnerBinder        = observedWorld{}
	_ p.CheckpointWorld    = observedWorld{}
)

// Simulate delegates unchanged and records failed frame proposals for diagnosis.
func (w observedWorld) Simulate(frame p.Frame) (p.FrameResult, error) {
	result, err := w.World.Simulate(frame)
	if err != nil {
		_ = writeJSON(filepath.Join(w.out, "failed-frame.json"), struct {
			Frame   uint64
			Delta   float32
			Motions []observedMotion
			Error   string
		}{frame.ID, frame.Delta, recordMotions(frame.Motions), err.Error()})
	}
	return result, err
}

type observedMotion struct {
	Body          p.BodyHandle
	Velocity      p.Vec3
	MaxDriveForce float32
	Frozen        bool
	Segments      []p.MotionSegment
}

func recordMotions(motions []p.Motion) []observedMotion {
	out := make([]observedMotion, len(motions))
	for i, m := range motions {
		out[i] = observedMotion{m.Body, m.Velocity, m.MaxDriveForce, m.Frozen, m.Segments}
	}
	return out
}
