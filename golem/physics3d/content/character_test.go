package content

import (
	"errors"
	"math"
	"testing"

	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
)

func authoredCharacter(t *testing.T) *Source {
	source := fixtureSource(t)
	source.Bodies[0].Kind = "kinematic"
	source.Bodies[0].UseGravity = false
	c := &source.Colliders[0]
	c.Shape, c.Axis, c.Radius, c.Height = "capsule", 1, .4, 2
	c.Character = &Character{StepOffset: .25, SlopeLimit: 38, SkinWidth: .03, DetectCollisions: true, EnableOverlapRecovery: true}
	return source
}

func TestAuthoredCharacterRequiresExplicitPolicy(t *testing.T) {
	w := newLoadWorld()
	source := authoredCharacter(t)
	prepared, err := Prepare(source, DefaultSettings())
	if err != nil {
		t.Fatal(err)
	}
	if len(prepared.Diagnostics()) == 0 || prepared.Source().Colliders[0].Character == nil {
		t.Fatal("authored character was silently dropped")
	}
	if _, err = prepared.Load(w, func(string) (int64, error) { return 1, nil }); !errors.Is(err, p.ErrUnsupported) || w.createCalls != 0 {
		t.Fatalf("policy preflight %v, calls %d", err, w.createCalls)
	}
}

func TestCharacterPolicyHashRejectsNonfiniteUnusedSettings(t *testing.T) {
	settings := DefaultSettings()
	settings.CharacterController.Skin = float32(math.NaN())
	if _, err := Prepare(fixtureSource(t), settings); !errors.Is(err, p.ErrInvalid) {
		t.Fatalf("invalid settings hash: %v", err)
	}
}

func TestAuthoredCharacterMapsAndHashesExplicitPolicy(t *testing.T) {
	settings := DefaultSettings()
	settings.CharacterPolicy = GolemKinematicCharacters
	settings.CharacterController.GroundDistance = .08
	source := authoredCharacter(t)
	prepared, err := Prepare(source, settings)
	if err != nil {
		t.Fatal(err)
	}
	if len(prepared.Diagnostics()) != 0 {
		t.Fatalf("prepare diagnostics %+v", prepared.Diagnostics())
	}
	w := newLoadWorld()
	instance, err := prepared.Load(w, func(string) (int64, error) { return 1, nil })
	if err != nil {
		t.Fatal(err)
	}
	body, _ := instance.Body(source.Bodies[0].ID)
	got := w.bodies[body]
	if !got.Controller.Enabled || got.Controller.Gravity || got.Controller.StepHeight != .25 || got.Controller.MaxSlopeDegrees != 38 || got.Controller.Skin != .03 || got.Controller.GroundDistance != .08 {
		t.Fatalf("controller mapping %+v", got.Controller)
	}
	for _, shape := range w.shapes {
		if shape.Kind != p.Capsule || shape.Radius != .4 || shape.PointB.Sub(shape.PointA).Length() != 1.2 {
			t.Fatalf("geometry changed %+v", shape)
		}
	}
	settings.CharacterController.Gravity = true
	other, err := Prepare(source, settings)
	if err != nil || other.Fingerprint() == prepared.Fingerprint() || other.SourceHash() != prepared.SourceHash() {
		t.Fatal("controller policy was not independently hashed", err)
	}
}

func TestDisabledAuthoredCharacterRetainsDormantController(t *testing.T) {
	source := authoredCharacter(t)
	source.Colliders[0].Enabled = false
	settings := DefaultSettings()
	settings.CharacterPolicy = GolemKinematicCharacters
	prepared, err := Prepare(source, settings)
	if err != nil {
		t.Fatal(err)
	}
	w := newLoadWorld()
	instance, err := prepared.Load(w, func(string) (int64, error) { return 1, nil })
	if err != nil {
		t.Fatal(err)
	}
	body, _ := instance.Body(source.Bodies[0].ID)
	if w.bodies[body].Enabled || !w.bodies[body].Controller.Enabled || !prepared.Source().Bodies[0].Enabled {
		t.Fatal("disabled source character became active or lost authored metadata")
	}
}

func TestAuthoredCharacterUnsupportedControlsPreflight(t *testing.T) {
	for name, change := range map[string]func(*Source){
		"deadband":                       func(s *Source) { s.Colliders[0].Character.MinimumMoveDistance = .001 },
		"collision disabled":             func(s *Source) { s.Colliders[0].Character.DetectCollisions = false },
		"recovery disabled":              func(s *Source) { s.Colliders[0].Character.EnableOverlapRecovery = false },
		"sideways capsule":               func(s *Source) { s.Colliders[0].Axis = 0 },
		"slope outside controller range": func(s *Source) { s.Colliders[0].Character.SlopeLimit = 90 },
	} {
		t.Run(name, func(t *testing.T) {
			source := authoredCharacter(t)
			change(source)
			settings := DefaultSettings()
			settings.CharacterPolicy = GolemKinematicCharacters
			prepared, err := Prepare(source, settings)
			if err != nil {
				t.Fatal(err)
			}
			w := newLoadWorld()
			if len(prepared.Diagnostics()) == 0 {
				t.Fatal("unsupported authored setting disappeared")
			}
			if _, err = prepared.Load(w, func(string) (int64, error) { return 1, nil }); !errors.Is(err, p.ErrUnsupported) || w.createCalls != 0 {
				t.Fatalf("preflight %v calls %d", err, w.createCalls)
			}
		})
	}
}
