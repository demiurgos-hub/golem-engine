package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/demiurgos-hub/golem-engine/golem"
	"github.com/demiurgos-hub/golem-engine/golem/nav/surface"
	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
	"github.com/demiurgos-hub/golem-engine/golem/physics3d/content"
	"github.com/demiurgos-hub/navmesh-go/dynamic"
	"github.com/demiurgos-hub/navmesh-go/geom"
)

const churnKey = "qualification-door"

type churnEvidence struct {
	Sequence, Submitted, Committed, Unloads uint64
	PendingGeneration                       uint64
	Failure                                 string
}

// Only this tick-owned harness mutates these worlds, and it does not submit
// another edit while a candidate is outstanding. Preflight all revisions before
// publishing either clearance profile; no worker can mutate them at commitment.
type profileBuild struct {
	worlds     []*dynamic.World
	candidates []*dynamic.Candidate
}

type retryProfileBuild struct{ cause error }

func (e retryProfileBuild) Error() string              { return e.cause.Error() }
func (e retryProfileBuild) Unwrap() error              { return e.cause }
func (e retryProfileBuild) RetryNavigationBuild() bool { return true }

func (b *profileBuild) Commit() error {
	for i, world := range b.worlds {
		status := world.Status()
		if status.State != dynamic.Prepared || status.Revision != b.candidates[i].Revision() {
			return dynamic.ErrSuperseded
		}
	}
	for i, world := range b.worlds {
		if err := world.Commit(b.candidates[i]); err != nil {
			return err
		}
	}
	return nil
}

func (b *profileBuild) Discard() {
	for i, candidate := range b.candidates {
		b.worlds[i].Discard(candidate)
	}
}

func prepareProfiles(ctx context.Context, worlds []*dynamic.World) (golem.NavigationBuildResult, error) {
	b := &profileBuild{worlds: worlds}
	for _, world := range worlds {
		candidate, err := world.Prepare(ctx)
		if err != nil {
			b.Discard()
			if errors.Is(err, dynamic.ErrBusy) {
				return nil, retryProfileBuild{err}
			}
			return nil, err
		}
		b.candidates = append(b.candidates, candidate)
	}
	return b, nil
}

func churnOrigin(extent int) (float32, float32) {
	// Capacity routes must avoid the full padded dirty tile, not only the door.
	return -float32(extent) * .3, -float32(extent) * .45
}

func churnSource(sequence uint64, extent int) (*content.Prepared, geom.Bounds, error) {
	x, z := churnOrigin(extent)
	if sequence%3 == 2 {
		x += 2
	}
	bounds := geom.Bounds{Min: geom.Vec3{X: float64(x - 1), Y: 0, Z: float64(z - 1)}, Max: geom.Vec3{X: float64(x + 1), Y: 4, Z: float64(z + 1)}}
	if sequence%3 == 0 {
		return nil, bounds, nil
	}
	masks := make([]uint32, 32)
	for i := range masks {
		masks[i] = ^uint32(0)
	}
	source := &content.Source{Generator: content.Generator, Version: 1, WorldID: churnKey, Revision: sequence, CoordinateLimit: p.CoordinateLimit, LayerCollisionMasks: masks,
		Bodies:    []content.Body{{ID: "churn/body", Kind: "static", CollisionDetection: "discrete", Position: p.Vec3{X: x, Y: 2, Z: z}, Rotation: p.Quat{W: 1}, Enabled: true, Active: true, Mass: 1, AutomaticCenterOfMass: true, AutomaticInertiaTensor: true, InertiaTensor: p.Vec3{X: 1, Y: 1, Z: 1}, InertiaRotation: p.Quat{W: 1}}},
		Colliders: []content.Collider{{ID: "churn/collider", BodyID: "churn/body", SourceID: "qualification/door", Shape: "box", Size: p.Vec3{X: 2, Y: 4, Z: 2}, Rotation: p.Quat{W: 1}, Enabled: true, Active: true, Solid: true, Query: true, ContactOffset: .01, MaterialID: "default"}},
		Materials: []content.Material{{ID: "default", StaticFriction: .5, DynamicFriction: .5, FrictionCombine: "average", BounceCombine: "average"}},
	}
	prepared, err := content.Prepare(source, content.DefaultSettings())
	if err == nil && len(prepared.Diagnostics()) != 0 {
		err = fmt.Errorf("churn content diagnostics: %v", prepared.Diagnostics())
	}
	return prepared, bounds, err
}

func (s *scene) pollChurn(out string) error {
	if s.churn.PendingGeneration == 0 {
		return nil
	}
	state, ok := s.server.NavigationBuildStatus("physics/" + churnKey)
	if !ok || state.Generation != s.churn.PendingGeneration {
		return fmt.Errorf("churn build generation lost")
	}
	if state.Pending {
		return nil
	}
	if !state.Committed || state.Err != nil {
		s.churn.Failure = fmt.Sprintf("churn navigation publication: %+v", state)
		return fmt.Errorf("%s", s.churn.Failure)
	}
	s.churn.Committed++
	s.churn.PendingGeneration = 0
	return writeJSON(filepath.Join(out, fmt.Sprintf("churn-%06d.json", s.churn.Sequence)), map[string]any{"sequence": s.churn.Sequence, "build": state, "physics": s.world.Metrics(), "tick": s.server.Tick(), "unloaded": s.churn.Sequence%3 == 0})
}

func (s *scene) beginChurn(o options) error {
	if s.churn.PendingGeneration != 0 {
		return p.ErrBusy
	}
	sequence := s.churn.Sequence + 1
	source, bounds, err := churnSource(sequence, o.Extent)
	if err != nil {
		return err
	}
	change := dynamic.Changes{Volumes: []geom.Volume{{ID: churnKey, Shape: "box", Exclude: true, Bounds: bounds}}}
	fingerprint := "unloaded"
	if source == nil {
		change = dynamic.Changes{RemoveVolumes: []string{churnKey}}
	} else {
		fingerprint = source.Fingerprint()
	}
	data, err := json.Marshal(change)
	if err != nil {
		return err
	}
	// Union both positions and leave clearance padding within the known world.
	px, pz := churnOrigin(o.Extent)
	x, z := float64(px), float64(pz)
	identity := golem.PhysicsGeometryIdentity{Key: churnKey, Revision: sequence, PhysicsFingerprint: fingerprint, NavigationFingerprint: hash(data)}
	generation, err := s.server.ActivatePhysicsGeometry(golem.PhysicsGeometryActivation{Identity: identity, Profiles: []surface.ProfileID{"human", "large"}, Bounds: golem.NavigationTraversalBounds{Min: surface.Vec3{X: x - 3, Y: -1, Z: z - 3}, Max: surface.Vec3{X: x + 5, Y: 5, Z: z + 3}}, Source: source,
		Quarantine: func() error {
			for _, world := range s.navWorlds {
				if _, err := world.Apply(change); err != nil {
					return err
				}
			}
			return nil
		},
		PrepareNavigation: func(ctx context.Context) (golem.NavigationBuildResult, error) {
			return prepareProfiles(ctx, s.navWorlds)
		},
	})
	if err != nil {
		return err
	}
	s.churn.Sequence, s.churn.PendingGeneration = sequence, generation
	s.churn.Submitted++
	if source == nil {
		s.churn.Unloads++
	}
	return nil
}
