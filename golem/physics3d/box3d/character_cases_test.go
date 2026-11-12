package box3d_test

import (
	"math"
	"testing"

	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
	box "github.com/demiurgos-hub/golem-engine/golem/physics3d/box3d"
	"github.com/demiurgos-hub/golem-engine/golem/physics3d/character"
)

func floorForCharacter(t *testing.T, w *box.World) p.ColliderHandle {
	_, collider := add(t, w, p.Static, p.Vec3{Y: -.5}, p.ShapeDesc{Kind: p.Box, HalfExtents: p.Vec3{X: 10, Y: .5, Z: 10}})
	return collider
}
func moveCharacter(t *testing.T, w *box.World, cfg character.Config, at, delta p.Vec3) character.Result {
	t.Helper()
	pose := p.Identity()
	pose.Position = at
	r, err := cfg.Move(w, pose, delta, p.QueryFilter{Mask: ^uint64(0)})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestCharacterStairsAndLowCeiling(t *testing.T) {
	for _, ceiling := range []bool{false, true} {
		t.Run(map[bool]string{false: "clear", true: "low-ceiling"}[ceiling], func(t *testing.T) {
			w := world(t)
			floorForCharacter(t, w)
			_, stair := add(t, w, p.Static, p.Vec3{X: 2, Y: .1}, p.ShapeDesc{Kind: p.Box, HalfExtents: p.Vec3{X: 1, Y: .1, Z: 2}})
			if ceiling {
				add(t, w, p.Static, p.Vec3{X: 1.5, Y: 2.15}, p.ShapeDesc{Kind: p.Box, HalfExtents: p.Vec3{X: 2, Y: .25, Z: 2}})
			}
			r := moveCharacter(t, w, character.DefaultConfig(), p.Vec3{Y: .901}, p.Vec3{X: 2})
			if ceiling {
				if r.Stepped || r.Pose.Position.X > 1 || r.Pose.Position.Y > .95 {
					t.Fatalf("passed under insufficient headroom: %+v", r)
				}
			} else if !r.Stepped || !r.Grounded || r.Support.Collider != stair || math.Abs(float64(r.Pose.Position.Y-1.101)) > .002 {
				t.Fatalf("failed stair: %+v", r)
			}
		})
	}
}

func TestCharacterCornerAndNarrowPassage(t *testing.T) {
	for _, narrow := range []bool{false, true} {
		t.Run(map[bool]string{false: "corner", true: "narrow"}[narrow], func(t *testing.T) {
			w := world(t)
			floorForCharacter(t, w)
			cfg := character.DefaultConfig()
			cfg.StepHeight = 0
			if narrow {
				add(t, w, p.Static, p.Vec3{X: 2, Y: 2, Z: .85}, p.ShapeDesc{Kind: p.Box, HalfExtents: p.Vec3{X: 1, Y: 2, Z: .5}})
				add(t, w, p.Static, p.Vec3{X: 2, Y: 2, Z: -.85}, p.ShapeDesc{Kind: p.Box, HalfExtents: p.Vec3{X: 1, Y: 2, Z: .5}})
				r := moveCharacter(t, w, cfg, p.Vec3{Y: .901}, p.Vec3{X: 4})
				if !r.Blocked || r.Pose.Position.X > 1 {
					t.Fatal(r)
				}
			} else {
				add(t, w, p.Static, p.Vec3{X: 2, Y: 2}, p.ShapeDesc{Kind: p.Box, HalfExtents: p.Vec3{X: .5, Y: 2, Z: 10}})
				add(t, w, p.Static, p.Vec3{Y: 2, Z: 2}, p.ShapeDesc{Kind: p.Box, HalfExtents: p.Vec3{X: 10, Y: 2, Z: .5}})
				r := moveCharacter(t, w, cfg, p.Vec3{Y: .901}, p.Vec3{X: 4, Z: 4})
				if !r.Blocked || r.Pose.Position.X > 1.101 || r.Pose.Position.Z > 1.101 || r.Pose.Position.Y > .902 {
					t.Fatal(r)
				}
			}
		})
	}
}

func TestCharacterSupportIdentityAndRemoval(t *testing.T) {
	w := world(t)
	floorForCharacter(t, w)
	bridge, collider := add(t, w, p.Static, p.Vec3{Y: 3}, p.ShapeDesc{Kind: p.Box, HalfExtents: p.Vec3{X: 2, Y: .25, Z: 2}})
	cfg := character.DefaultConfig()
	r := moveCharacter(t, w, cfg, p.Vec3{Y: 4.151}, p.Vec3{X: .1})
	if !r.Grounded || r.Support.Collider != collider {
		t.Fatalf("wrong floor support: %+v", r)
	}
	if err := w.DestroyBody(bridge, p.Deleted); err != nil {
		t.Fatal(err)
	}
	if err := w.Synchronize(); err != nil {
		t.Fatal(err)
	}
	r = moveCharacter(t, w, cfg, r.Pose.Position, p.Vec3{})
	if r.Grounded || r.Pose.Position.Y < 4.15 {
		t.Fatalf("support removal snapped to lower floor: %+v", r)
	}
}

func TestCharacterLedgeAndUpwardCeiling(t *testing.T) {
	w := world(t)
	_, platform := add(t, w, p.Static, p.Vec3{Y: -.25}, p.ShapeDesc{Kind: p.Box, HalfExtents: p.Vec3{X: 1, Y: .25, Z: 2}})
	cfg := character.DefaultConfig()
	cfg.StepHeight = 0
	r := moveCharacter(t, w, cfg, p.Vec3{Y: .901}, p.Vec3{X: 3})
	if r.Grounded || r.Pose.Position.Y != .901 || r.Support.Collider == platform {
		t.Fatalf("ledge snapped or retained stale support: %+v", r)
	}
	add(t, w, p.Static, p.Vec3{Y: 2.5}, p.ShapeDesc{Kind: p.Box, HalfExtents: p.Vec3{X: 5, Y: .25, Z: 5}})
	r = moveCharacter(t, w, cfg, p.Vec3{Y: .901}, p.Vec3{Y: 3})
	if !r.Blocked || r.Pose.Position.Y > 1.351 || r.Pose.Position.Y < 1.3 {
		t.Fatalf("ceiling clipping: %+v", r)
	}
}

func TestCharacterWalkableSlopeAllowsAscent(t *testing.T) {
	w := world(t)
	floorForCharacter(t, w)
	shape, err := w.CreateShape(p.ShapeDesc{Kind: p.Box, HalfExtents: p.Vec3{X: 4, Y: .2, Z: 2}})
	if err != nil {
		t.Fatal(err)
	}
	d := p.DefaultBody(p.Static)
	d.Pose.Position = p.Vec3{X: 2, Y: .5}
	d.Pose.Rotation = p.Quat{Z: float32(math.Sin(math.Pi / 12)), W: float32(math.Cos(math.Pi / 12))}
	body, err := w.CreateBody(d)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = w.CreateCollider(p.DefaultCollider(body, shape)); err != nil {
		t.Fatal(err)
	}
	if err = w.Synchronize(); err != nil {
		t.Fatal(err)
	}
	cfg := character.DefaultConfig()
	cfg.StepHeight = 0
	r := moveCharacter(t, w, cfg, p.Vec3{Y: .901}, p.Vec3{X: 3})
	if !r.Grounded || r.Pose.Position.Y < 1.3 || r.Support.Normal.Y < .85 {
		t.Fatalf("walkable ascent blocked: %+v", r)
	}
}

func TestCharacterSteepSlopeDoesNotClimbFromHorizontalIntent(t *testing.T) {
	w := world(t)
	floorForCharacter(t, w)
	shape, err := w.CreateShape(p.ShapeDesc{Kind: p.Box, HalfExtents: p.Vec3{X: 2, Y: .2, Z: 2}})
	if err != nil {
		t.Fatal(err)
	}
	d := p.DefaultBody(p.Static)
	d.Pose.Position = p.Vec3{X: 2, Y: 1}
	angle := math.Pi / 3
	d.Pose.Rotation = p.Quat{Z: float32(math.Sin(angle / 2)), W: float32(math.Cos(angle / 2))}
	body, err := w.CreateBody(d)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = w.CreateCollider(p.DefaultCollider(body, shape)); err != nil {
		t.Fatal(err)
	}
	if err = w.Synchronize(); err != nil {
		t.Fatal(err)
	}
	cfg := character.DefaultConfig()
	cfg.StepHeight = 0
	r := moveCharacter(t, w, cfg, p.Vec3{Y: .901}, p.Vec3{X: 3})
	if !r.Blocked || r.Pose.Position.Y > .903 {
		t.Fatalf("horizontal intent climbed unwalkable slope: %+v", r)
	}
}
