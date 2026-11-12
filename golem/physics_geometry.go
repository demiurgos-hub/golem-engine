package golem

import (
	"context"
	"errors"
	"fmt"

	"github.com/demiurgos-hub/golem-engine/golem/nav/surface"
	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
	"github.com/demiurgos-hub/golem-engine/golem/physics3d/content"
)

// PhysicsGeometryIdentity binds one desired source generation across both worlds.
type PhysicsGeometryIdentity struct {
	Key, PhysicsFingerprint, NavigationFingerprint string
	Revision                                       uint64
}

// PreparedPhysicsGeometry holds private source preparation and an atomic
// navigation replacement. Nil Source explicitly unloads this key. Identity must
// exactly match the queued request; Source.Fingerprint must match when present.
type PreparedPhysicsGeometry struct {
	Identity     PhysicsGeometryIdentity
	Source       *content.Prepared
	Navigation   NavigationBuildResult
	ResolveOwner content.OwnerResolver
}

// PhysicsGeometryRequest prepares one source lease while unaffected agents keep
// moving. Quarantine must first register the complete old/new Bounds for every
// profile (including bake clearance influence) in the navigation backend. Prepare
// owns immutable inputs and runs on a worker; it must not read live entities/worlds.
type PhysicsGeometryRequest struct {
	Identity   PhysicsGeometryIdentity
	Profiles   []surface.ProfileID
	Bounds     NavigationTraversalBounds
	Priority   int
	Quarantine func() error
	Prepare    func(context.Context) (PreparedPhysicsGeometry, error)
}

// QueuePhysicsGeometry establishes quarantine before scheduling private source
// preparation through the normal coalescing navigation build queue. Publication
// loads the candidate, atomically publishes navigation, then removes the previous
// source lease before the next physics frame. Before-publication failures remove
// the candidate and retain the previous lease; unexpected cleanup failure closes
// readiness. Quarantine remains on failure until a later explicit world change.
func (s *Server) QueuePhysicsGeometry(request PhysicsGeometryRequest) (uint64, error) {
	if err := s.physics.mutable(); err != nil {
		return 0, err
	}
	if s.navigation == nil || request.Identity.Key == "" || len(request.Identity.Key) > 4096 || request.Identity.Revision == 0 || request.Identity.PhysicsFingerprint == "" || len(request.Identity.PhysicsFingerprint) > 4096 || request.Identity.NavigationFingerprint == "" || len(request.Identity.NavigationFingerprint) > 4096 || request.Prepare == nil || request.Quarantine == nil || len(request.Profiles) == 0 || len(request.Profiles) > 256 || !validTraversalBounds(request.Bounds) {
		return 0, p.ErrInvalid
	}
	request.Profiles = append([]surface.ProfileID(nil), request.Profiles...)
	for _, profile := range request.Profiles {
		if profile == "" || len(profile) > 256 {
			return 0, p.ErrInvalid
		}
	}
	if err := request.Quarantine(); err != nil {
		return 0, err
	}
	if !s.physicsGeometryQuarantined(request) {
		return 0, errors.New("golem: physics geometry requires an active navigation quarantine")
	}
	return s.QueueNavigationBuild("physics/"+request.Identity.Key, request.Priority, func(ctx context.Context) (NavigationBuildResult, error) {
		prepared, err := request.Prepare(ctx)
		if err != nil {
			if prepared.Navigation != nil {
				prepared.Navigation.Discard()
			}
			return nil, err
		}
		if prepared.Navigation == nil || prepared.Identity != request.Identity || prepared.Source != nil && prepared.Source.Fingerprint() != request.Identity.PhysicsFingerprint {
			if prepared.Navigation != nil {
				prepared.Navigation.Discard()
			}
			return nil, errors.New("golem: prepared physics/navigation identity mismatch")
		}
		return &physicsGeometryBuild{server: s, request: request, prepared: prepared}, nil
	})
}

// PhysicsGeometry returns the accepted source lease for a stable cell/object key.
// It is tick-owned. Ownership remains with Server; do not remove or mutate its bodies
// directly. Runtime entity attachments must detach before that source is replaced.
func (s *Server) PhysicsGeometry(key string) (*content.Instance, bool) {
	if s.physics == nil {
		return nil, false
	}
	instance, ok := s.physics.geometry[key]
	return instance, ok
}

// PhysicsReady reports tick-owned physics readiness, including coherent restoration.
func (s *Server) PhysicsReady() (ready, restoring bool, err error) {
	if s.physics == nil {
		return false, false, errors.New("golem: physics is not configured")
	}
	ps := s.physics
	return !ps.closed && !ps.restoring && ps.fault == nil, ps.restoring, ps.fault
}

func (s *Server) physicsGeometryQuarantined(request PhysicsGeometryRequest) bool {
	for _, profile := range request.Profiles {
		if safety := s.navigation.regionalSafety(profile); safety != nil {
			if safety.SafeBounds(request.Bounds.Min, request.Bounds.Max) {
				return false
			}
		} else if !s.navigation.profileUnavailable(profile) {
			return false
		}
	}
	return true
}

type physicsGeometryBuild struct {
	server                    *Server
	request                   PhysicsGeometryRequest
	prepared                  PreparedPhysicsGeometry
	done, navigationCommitted bool
}

func (b *physicsGeometryBuild) Commit() error {
	if b.done {
		return p.ErrStale
	}
	s, ps := b.server, b.server.physics
	if err := ps.mutable(); err != nil {
		return err
	}
	if s.navigation.frame != nil || !s.physicsGeometryQuarantined(b.request) {
		return p.ErrBusy
	}
	old := ps.geometry[b.request.Identity.Key]
	if old != nil {
		min, max, present, err := old.Bounds()
		if err != nil {
			return err
		}
		if present && !geometryBoundsContain(b.request.Bounds, min, max) {
			return errors.New("golem: current geometry lies outside declared quarantine bounds")
		}
		for _, id := range old.Prepared().BodyIDs() {
			h, ok := old.Body(id)
			if ok && ps.bodies[h] != 0 {
				return errors.New("golem: detach entity physics before replacing its source lease")
			}
		}
	}
	if source := b.prepared.Source; source != nil {
		min, max, present, err := source.Bounds()
		if err != nil {
			return err
		}
		if present && !geometryBoundsContain(b.request.Bounds, min, max) {
			return errors.New("golem: replacement geometry lies outside declared quarantine bounds")
		}
		owned := map[string]bool{}
		for key, instance := range ps.geometry {
			if key != b.request.Identity.Key {
				for _, id := range instance.Prepared().BodyIDs() {
					owned[id] = true
				}
			}
		}
		for _, id := range source.BodyIDs() {
			if owned[id] {
				return errors.New("golem: geometry source body identity belongs to another lease")
			}
		}
	}
	var candidate *content.Instance
	if b.prepared.Source != nil {
		var err error
		candidate, err = b.prepared.Source.Load(ps.world, b.prepared.ResolveOwner)
		if err != nil {
			if candidate != nil {
				b.fault(err)
			}
			return err
		}
	}
	if err := b.prepared.Navigation.Commit(); err != nil {
		if candidate != nil {
			if rollback := candidate.Remove(); rollback != nil {
				err = errors.Join(err, rollback)
				b.fault(err)
			}
		}
		return err
	}
	b.navigationCommitted = true
	if old != nil {
		if err := old.Remove(); err != nil {
			b.fault(err)
			return err
		}
	}
	if err := ps.world.Synchronize(); err != nil {
		b.fault(err)
		return err
	}
	if ps.geometry == nil {
		ps.geometry = map[string]*content.Instance{}
	}
	if candidate == nil {
		delete(ps.geometry, b.request.Identity.Key)
	} else {
		ps.geometry[b.request.Identity.Key] = candidate
	}
	b.done = true
	return nil
}

func (b *physicsGeometryBuild) fault(err error) {
	b.server.physics.fault = fmt.Errorf("golem: geometry publication requires coherent recovery: %w", err)
	b.server.physics.restoring = true
	b.server.navigation.restoring = true
	b.server.navigation.restoreErr = err
}

func (b *physicsGeometryBuild) Discard() {
	if b.done {
		return
	}
	b.done = true
	if !b.navigationCommitted {
		b.prepared.Navigation.Discard()
	}
}

func geometryBoundsContain(bounds NavigationTraversalBounds, min, max p.Vec3) bool {
	return traversalBoundsContain(bounds, physicsNavPoint(min)) && traversalBoundsContain(bounds, physicsNavPoint(max))
}
