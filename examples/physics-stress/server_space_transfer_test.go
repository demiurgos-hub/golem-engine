package main

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/demiurgos-hub/golem-engine/golem"
	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
	"github.com/demiurgos-hub/golem-engine/golem/physics3d/box3d"
)

const (
	transferAvatar int64 = iota + 1
	transferCrate
	transferCart
	transferCargo
)

// transferBoxes are the moved bodies: half extents and spawn position, a
// millimetre above where each comes to rest. The cargo rests on the cart;
// everything else rests on the ground.
var transferBoxes = map[int64]struct{ half, at p.Vec3 }{
	transferAvatar: {p.Vec3{X: .3, Y: .9, Z: .3}, p.Vec3{X: -3, Y: .901}},
	transferCrate:  {p.Vec3{X: .4, Y: .4, Z: .4}, p.Vec3{X: -5, Y: .401}},
	transferCart:   {p.Vec3{X: 1, Y: .25, Z: 1}, p.Vec3{Y: .251}},
	transferCargo:  {p.Vec3{X: .25, Y: .25, Z: .25}, p.Vec3{Y: .752}},
}

func transferBox(w p.World, mode p.MotionMode, pose p.Pose, half p.Vec3) (p.BodyHandle, error) {
	shape, err := w.CreateShape(p.ShapeDesc{Kind: p.Box, HalfExtents: half})
	if err != nil {
		return p.BodyHandle{}, err
	}
	desc := p.DefaultBody(mode)
	desc.Pose = pose
	body, err := w.CreateBody(desc)
	if err != nil {
		return p.BodyHandle{}, err
	}
	_, err = w.CreateCollider(p.DefaultCollider(body, shape))
	return body, err
}

// transferTow joins the avatar to the cart it pulls.
func transferTow(w p.World, avatar, cart p.BodyHandle) error {
	length := transferBoxes[transferCart].at.X - transferBoxes[transferAvatar].at.X
	_, err := w.CreateJoint(p.JointDesc{Kind: p.Distance, A: avatar, B: cart, FrameA: p.Identity(), FrameB: p.Identity(), Length: length})
	return err
}

type transferRig struct {
	server          *golem.Server
	overworld, cave *box3d.World
	actors          map[int64]*actor
	ticks           atomic.Int64
}

// newTransferRig builds an overworld with an avatar, a crate, a towed cart and
// cargo resting on the cart, and an empty cave with the same ground, then runs
// the server until the bodies have settled.
func newTransferRig(t *testing.T) *transferRig {
	t.Helper()
	rig := &transferRig{actors: map[int64]*actor{}}
	newWorld := func() *box3d.World {
		w, err := box3d.New(p.DefaultConfig())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = w.Close() })
		ground := p.Identity()
		ground.Position = p.Vec3{Y: -.5}
		if _, err := transferBox(w, p.Static, ground, p.Vec3{X: 50, Y: .5, Z: 50}); err != nil {
			t.Fatal(err)
		}
		return w
	}
	rig.overworld, rig.cave = newWorld(), newWorld()
	rig.server = golem.NewServer(golem.ServerConfig{TickRate: 60})
	if err := rig.server.SetPhysicsWorld(rig.overworld); err != nil {
		t.Fatal(err)
	}
	if err := rig.server.Space(3).SetPhysicsWorld(rig.cave); err != nil {
		t.Fatal(err)
	}
	bodies := map[int64]p.BodyHandle{}
	for id := transferAvatar; id <= transferCargo; id++ {
		box := transferBoxes[id]
		a := &actor{ID: id, position: box.at}
		rig.actors[id] = a
		if err := rig.server.CreateEntity(a); err != nil {
			t.Fatal(err)
		}
		pose := p.Identity()
		pose.Position = box.at
		body, err := transferBox(rig.overworld, p.FreeDynamic, pose, box.half)
		if err != nil {
			t.Fatal(err)
		}
		bodies[id] = body
	}
	if err := transferTow(rig.overworld, bodies[transferAvatar], bodies[transferCart]); err != nil {
		t.Fatal(err)
	}
	if err := rig.overworld.Synchronize(); err != nil {
		t.Fatal(err)
	}
	for id := transferAvatar; id <= transferCargo; id++ {
		// The default attachment: no placement tolerance of its own.
		if err := rig.server.AttachPhysics(id, bodies[id], golem.PhysicsAttachmentConfig{}); err != nil {
			t.Fatalf("attach entity %d: %v", id, err)
		}
	}
	rig.server.OnTick(func(float64, *golem.Server) { rig.ticks.Add(1) })
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- rig.server.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	rig.wait(t, 40)
	return rig
}

// wait blocks until n more ticks have run.
func (rig *transferRig) wait(t *testing.T, n int64) {
	t.Helper()
	target := rig.ticks.Load() + n
	for deadline := time.Now().Add(10 * time.Second); rig.ticks.Load() < target; {
		if time.Now().After(deadline) {
			t.Fatal("server did not tick")
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// onTick runs fn on the tick goroutine and waits for it.
func (rig *transferRig) onTick(t *testing.T, fn func() error) error {
	t.Helper()
	result := make(chan error, 1)
	for deadline := time.Now().Add(5 * time.Second); ; {
		err := rig.server.Post(func(*golem.Server) { result <- fn() })
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Millisecond)
	}
	select {
	case err := <-result:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("posted callback did not run")
		return nil
	}
}

// move carries all four entities to the cave, rebuilding each body at its
// source pose and the tow joint between avatar and cart.
func (rig *transferRig) move(t *testing.T, placement golem.PhysicsPlacementPolicy) error {
	t.Helper()
	return rig.onTick(t, func() error {
		return rig.server.MoveToSpace(golem.SpaceTransfer{To: 3, Placement: placement,
			Entities: []int64{transferAvatar, transferCrate, transferCart, transferCargo},
			RebuildPhysics: func(dst p.World, src []golem.PhysicsTransferSource) (map[int64]p.BodyHandle, error) {
				out := map[int64]p.BodyHandle{}
				for _, source := range src {
					body, err := transferBox(dst, source.Body.Mode, source.Body.Pose, transferBoxes[source.Entity].half)
					if err != nil {
						return out, err
					}
					out[source.Entity] = body
					if err := dst.SetVelocity(body, source.Body.LinearVelocity, source.Body.AngularVelocity); err != nil {
						return out, err
					}
				}
				return out, transferTow(dst, out[transferAvatar], out[transferCart])
			}})
	})
}

func (rig *transferRig) spaces() string {
	out := ""
	for id := transferAvatar; id <= transferCargo; id++ {
		space, _ := rig.server.EntitySpace(id)
		out += fmt.Sprint(space)
	}
	return out
}

// Bodies at rest sit a few millimetres inside their support. A transfer with
// default settings must still carry them: the avatar, a crate, the cart it
// tows and the cargo resting on that cart.
func TestSpaceTransferCarriesSettledBodies(t *testing.T) {
	rig := newTransferRig(t)
	cart, cargo := rig.actors[transferCart], rig.actors[transferCargo]
	var gap float32
	_ = rig.onTick(t, func() error {
		gap = (cargo.position.Y - .25) - (cart.position.Y + .25)
		return nil
	})
	if gap >= 0 {
		t.Logf("cargo does not penetrate the cart (gap %g); the strict test below still decides", gap)
	}
	if err := rig.move(t, golem.PhysicsPlacementPolicy{}); err != nil {
		t.Fatalf("settled bodies were rejected (cargo gap %g): %v", gap, err)
	}
	rig.wait(t, 30)
	var overworld, cave p.Metrics
	var cartY, cargoY float32
	err := rig.onTick(t, func() error {
		overworld, cave = rig.overworld.Metrics(), rig.cave.Metrics()
		cartY, cargoY = cart.position.Y, cargo.position.Y
		for id := transferAvatar; id <= transferCargo; id++ {
			if _, err := rig.server.PhysicsBody(id); err != nil {
				return fmt.Errorf("entity %d has no body after the move: %w", id, err)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := rig.spaces(); got != "3333" {
		t.Fatalf("entity spaces = %s", got)
	}
	// Ground only in the overworld; ground, four bodies and the tow joint in the cave.
	if overworld.Bodies != 1 || overworld.Joints != 0 || cave.Bodies != 5 || cave.Joints != 1 || cave.Fault != nil {
		t.Fatalf("overworld = %+v, cave = %+v", overworld, cave)
	}
	if cartY < .2 || cartY > .3 || cargoY < .7 || cargoY > .8 {
		t.Fatalf("the stack did not stay at rest in the cave: cart y=%g cargo y=%g", cartY, cargoY)
	}
}

// A tolerance below the resting penetration rejects the transfer and changes
// nothing.
func TestSpaceTransferStrictToleranceRejectsSettledBodies(t *testing.T) {
	rig := newTransferRig(t)
	err := rig.move(t, golem.PhysicsPlacementPolicy{MaxPenetration: 1e-6})
	if err == nil {
		t.Fatal("a strict transfer admitted bodies at rest")
	}
	var overworld, cave p.Metrics
	if err := rig.onTick(t, func() error {
		overworld, cave = rig.overworld.Metrics(), rig.cave.Metrics()
		for id := transferAvatar; id <= transferCargo; id++ {
			if _, err := rig.server.PhysicsBody(id); err != nil {
				return fmt.Errorf("entity %d lost its body: %w", id, err)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got := rig.spaces(); got != "0000" {
		t.Fatalf("entity spaces = %s after a rejected transfer", got)
	}
	if overworld.Bodies != 5 || overworld.Joints != 1 || cave.Bodies != 1 || cave.Joints != 0 || cave.Fault != nil || overworld.Fault != nil {
		t.Fatalf("overworld = %+v, cave = %+v", overworld, cave)
	}
}
