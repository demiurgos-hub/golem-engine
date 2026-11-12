package main

import (
	"errors"
	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
	box "github.com/demiurgos-hub/golem-engine/golem/physics3d/box3d"
	"os"
	"path/filepath"
	"testing"
)

func TestDebugFailureCaptureIsExplicitAndSeparatesNativeCandidate(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		w, err := box.New(p.DefaultConfig())
		if err != nil {
			t.Fatal(err)
		}
		d := p.DefaultBody(p.FreeDynamic)
		d.ID = "falling"
		d.Pose.Position.Y = 10
		body, err := w.CreateBody(d)
		if err != nil {
			t.Fatal(err)
		}
		s, err := w.CreateShape(p.ShapeDesc{Kind: p.Sphere, Radius: .5})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = w.CreateCollider(p.DefaultCollider(body, s)); err != nil {
			t.Fatal(err)
		}
		if err = w.Synchronize(); err != nil {
			t.Fatal(err)
		}
		if _, err = w.Simulate(p.Frame{ID: 1, Delta: .05}); err != nil {
			t.Fatal(err)
		}
		if err = w.Reject(1, errors.New("deliberate diagnostic fixture fault")); err != nil {
			t.Fatal(err)
		}
		out := t.TempDir()
		observer := observedWorld{World: w, out: out, debugCapture: enabled}
		if _, err = observer.Simulate(p.Frame{ID: 2, Delta: .05}); err == nil {
			t.Fatal("fault accepted")
		}
		data, err := os.Open(filepath.Join(out, "failed-debug.json"))
		if !enabled {
			if !os.IsNotExist(err) {
				t.Fatal("capture not opt-in", err)
			}
		} else {
			if err != nil {
				t.Fatal(err)
			}
			capture, e := p.ReadDebug(data)
			_ = data.Close()
			if e != nil {
				t.Fatal(e)
			}
			if capture.Status != "faulted" || capture.FrameAccepted || capture.Bodies[0].State.Pose.Position.Y != 10 || capture.Bodies[0].Uncommitted == nil || capture.Bodies[0].Uncommitted.Pose.Position.Y >= 10 {
				t.Fatal("fault candidate misrepresented", capture)
			}
		}
		_ = w.Close()
	}
}
