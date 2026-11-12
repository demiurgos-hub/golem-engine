package golem

import (
	"context"
	"errors"
	"sort"

	"github.com/demiurgos-hub/golem-engine/golem/nav/surface"
	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
	"github.com/demiurgos-hub/golem-engine/golem/physics3d/content"
)

// PhysicsGeometryPending captures a stable desired change without worker closures.
// Activated distinguishes new physical geometry awaiting navigation from a fully
// private candidate. Failed navigation keeps this record and its quarantine.
type PhysicsGeometryPending struct {
	Identity  PhysicsGeometryIdentity
	Profiles  []surface.ProfileID
	Bounds    NavigationTraversalBounds
	Activated bool
}

// PhysicsGeometryActivation prepares navigation only after physical activation.
// Source must already be privately prepared; nil explicitly unloads the key.
// Quarantine covers complete old/new geometry and navigation clearance influence.
type PhysicsGeometryActivation struct {
	Identity          PhysicsGeometryIdentity
	Profiles          []surface.ProfileID
	Bounds            NavigationTraversalBounds
	Priority          int
	Source            *content.Prepared
	ResolveOwner      content.OwnerResolver
	Quarantine        func() error
	PrepareNavigation func(context.Context) (NavigationBuildResult, error)
}

// ActivatePhysicsGeometry changes physical geometry at the current tick boundary,
// then queues navigation preparation. Quarantine remains until successful navigation
// publication. A failed/unqueued build retains the new physical state and can retry
// with the same identity without recreating bodies. New identities supersede workers.
func (s *Server) ActivatePhysicsGeometry(change PhysicsGeometryActivation) (uint64, error) {
	if err := s.physics.mutable(); err != nil {
		return 0, err
	}
	r := PhysicsGeometryRequest{Identity: change.Identity, Profiles: append([]surface.ProfileID(nil), change.Profiles...), Bounds: change.Bounds, Priority: change.Priority, Quarantine: change.Quarantine}
	if s.navigation == nil || r.Identity.Key == "" || len(r.Identity.Key) > 4096 || r.Identity.Revision == 0 || r.Identity.PhysicsFingerprint == "" || len(r.Identity.PhysicsFingerprint) > 4096 || r.Identity.NavigationFingerprint == "" || len(r.Identity.NavigationFingerprint) > 4096 || len(r.Profiles) == 0 || len(r.Profiles) > 256 || !validTraversalBounds(r.Bounds) || r.Quarantine == nil || change.PrepareNavigation == nil || change.Source != nil && change.Source.Fingerprint() != r.Identity.PhysicsFingerprint {
		return 0, p.ErrInvalid
	}
	for _, profile := range r.Profiles {
		if profile == "" || len(profile) > 256 {
			return 0, p.ErrInvalid
		}
	}
	if err := s.physicsGeometryCapacity(r.Identity.Key); err != nil {
		return 0, err
	}
	if err := r.Quarantine(); err != nil {
		return 0, err
	}
	if !s.physicsGeometryQuarantined(r) {
		return 0, errors.New("golem: physics activation requires navigation quarantine")
	}
	key := "physics/" + r.Identity.Key
	s.CancelNavigationBuild(key)
	s.rememberPhysicsGeometry(r, false)
	if s.physics.geometryIdentities[r.Identity.Key] != r.Identity {
		build := &physicsGeometryBuild{server: s, request: r, activation: true, prepared: PreparedPhysicsGeometry{Identity: r.Identity, Source: change.Source, ResolveOwner: change.ResolveOwner, Navigation: physicsActivationBoundary{}}}
		if err := build.Commit(); err != nil {
			return 0, err
		}
	}
	s.rememberPhysicsGeometry(r, true)
	return s.QueueNavigationBuild(key, r.Priority, func(ctx context.Context) (NavigationBuildResult, error) {
		prepared, err := change.PrepareNavigation(ctx)
		if err != nil {
			if prepared != nil {
				prepared.Discard()
			}
			return nil, err
		}
		if prepared == nil {
			return nil, p.ErrInvalid
		}
		return &physicsActivatedNavigation{server: s, request: r, navigation: prepared}, nil
	})
}

func (s *Server) rememberPhysicsGeometry(r PhysicsGeometryRequest, activated bool) {
	if s.physics.geometryPending == nil {
		s.physics.geometryPending = map[string]PhysicsGeometryPending{}
	}
	s.physics.geometryPending[r.Identity.Key] = PhysicsGeometryPending{Identity: r.Identity, Profiles: append([]surface.ProfileID(nil), r.Profiles...), Bounds: r.Bounds, Activated: activated}
}

func (s *Server) physicsGeometryCapacity(key string) error {
	if _, exists := s.physics.geometryPending[key]; !exists && len(s.physics.geometryPending) >= s.navigation.config.BuildHistoryCapacity {
		return p.ErrBusy
	}
	if _, exists := s.navigation.buildStates["physics/"+key]; !exists && len(s.navigation.buildStates) >= s.navigation.config.BuildHistoryCapacity {
		return p.ErrBusy
	}
	return nil
}

// PendingPhysicsGeometry returns copied stable changes still under quarantine.
// It is tick-owned and includes failed changes awaiting an explicit retry.
func (s *Server) PendingPhysicsGeometry() []PhysicsGeometryPending {
	if s.physics == nil {
		return nil
	}
	var result []PhysicsGeometryPending
	for _, pending := range s.physics.geometryPending {
		pending.Profiles = append([]surface.ProfileID(nil), pending.Profiles...)
		result = append(result, pending)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Identity.Key < result[j].Identity.Key })
	return result
}

type physicsActivationBoundary struct{}

func (physicsActivationBoundary) Commit() error { return nil }
func (physicsActivationBoundary) Discard()      {}

type physicsActivatedNavigation struct {
	server     *Server
	request    PhysicsGeometryRequest
	navigation NavigationBuildResult
	done       bool
}

func (b *physicsActivatedNavigation) Commit() error {
	if b.done {
		return p.ErrStale
	}
	if err := b.server.physics.mutable(); err != nil {
		return err
	}
	if b.server.physics.geometryIdentities[b.request.Identity.Key] != b.request.Identity || b.server.navigation.frame != nil || !b.server.physicsGeometryQuarantined(b.request) {
		return p.ErrStale
	}
	if pending, ok := b.server.physics.geometryPending[b.request.Identity.Key]; !ok || pending.Identity != b.request.Identity || !pending.Activated {
		return p.ErrStale
	}
	if err := b.navigation.Commit(); err != nil {
		return err
	}
	delete(b.server.physics.geometryPending, b.request.Identity.Key)
	b.done = true
	return nil
}
func (b *physicsActivatedNavigation) Discard() {
	if !b.done {
		b.done = true
		b.navigation.Discard()
	}
}
