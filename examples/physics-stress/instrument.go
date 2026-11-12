package main

import (
	"os"
	"path/filepath"

	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
	box "github.com/demiurgos-hub/golem-engine/golem/physics3d/box3d"
)

// This wrapper records failed proposals unchanged, at the public physics seam.
// It does not alter motion policy or make an unsupported frame look accepted.
type observedWorld struct {
	*box.World
	out            string
	debugCapture   bool
	sourceSnapshot string
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
			Paths   []p.BodyPath
			Error   string
		}{frame.ID, frame.Delta, recordMotions(frame.Motions), result.Paths, err.Error()})
		if w.debugCapture {
			o := p.DefaultDebugOptions()
			o.IncludeUncommitted = true
			diagnostic := result
			diagnostic.ID = frame.ID
			o.Frame = &diagnostic
			o.SourceSnapshot = w.sourceSnapshot
			for _, m := range frame.Motions {
				for _, s := range m.Segments {
					if len(o.Targets) >= 256 {
						break
					}
					o.Targets = append(o.Targets, p.DebugTarget{Body: m.Body, From: s.From, Target: s.Target, Label: "navigation intent; failed frame, not accepted"})
				}
			}
			capture, captureErr := w.CaptureDebug(o)
			if captureErr == nil {
				f, e := os.OpenFile(filepath.Join(w.out, "failed-debug.json"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
				captureErr = e
				if e == nil {
					captureErr = p.WriteDebug(f, capture)
					_ = f.Close()
				}
			}
			if captureErr != nil {
				_ = writeJSON(filepath.Join(w.out, "failed-debug-error.json"), map[string]string{"Error": captureErr.Error()})
			}
		}
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
