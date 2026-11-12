package golem

import (
	"fmt"

	"github.com/demiurgos-hub/golem-engine/golem/nav/surface"
	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
)

// PreparedPhysicsRecovery owns a privately reconstructed desired world pair.
// The host applies captured desired source to captured accepted physical state,
// migrating body/joint state by stable IDs, and prepares matching navigation.
// Nothing may mutate live state before return. Geometry identifies the resulting
// source leases; callback implementations and backend handles are not persisted.
type PreparedPhysicsRecovery struct {
	CheckpointID, PhysicsFingerprint, NavigationFingerprint string
	PhysicsRevision, NavigationRevision                     uint64
	Physics                                                 p.PreparedRestore
	Navigation                                              NavigationBuildResult
	Geometry                                                []PhysicsGeometryCheckpoint
}

// Discard releases unpublished candidates. After transfer, the root restoration
// coordinator owns these resources; callers must not discard them again.
func (r PreparedPhysicsRecovery) Discard() {
	if r.Physics != nil {
		r.Physics.Discard()
	}
	if r.Navigation != nil {
		r.Navigation.Discard()
	}
}

func (r PreparedPhysicsRecovery) validate(c PhysicsCheckpoint) error {
	if r.Physics == nil || r.Navigation == nil || len(r.Geometry) > 100000 || r.CheckpointID != c.CheckpointID || r.PhysicsFingerprint != c.DesiredPhysicsFingerprint || r.NavigationFingerprint != c.Navigation.SourceFingerprint || r.PhysicsRevision != c.DesiredPhysicsRevision || r.NavigationRevision != c.Navigation.DesiredRevision {
		return fmt.Errorf("%w: pending recovery pair identity mismatch", p.ErrInvalid)
	}
	keys := map[string]bool{}
	bytes := 0
	for _, g := range r.Geometry {
		bytes += len(g.Source.Source)
		if g.Key == "" || len(g.Key) > 4096 || keys[g.Key] || bytes > 512<<20 || g.Identity.Key != "" && (g.Identity.Key != g.Key || g.Identity.PhysicsFingerprint != g.Source.Fingerprint) {
			return p.ErrInvalid
		}
		keys[g.Key] = true
	}
	return nil
}

func validatePendingPhysicsGeometry(changes []PhysicsGeometryPending) error {
	keys := map[string]bool{}
	for _, change := range changes {
		id := change.Identity
		if id.Key == "" || len(id.Key) > 4096 || keys[id.Key] || id.Revision == 0 || id.PhysicsFingerprint == "" || len(id.PhysicsFingerprint) > 4096 || id.NavigationFingerprint == "" || len(id.NavigationFingerprint) > 4096 || len(change.Profiles) == 0 || len(change.Profiles) > 256 || !validTraversalBounds(change.Bounds) {
			return p.ErrInvalid
		}
		keys[id.Key] = true
		for _, profile := range change.Profiles {
			if profile == "" || len(profile) > 256 {
				return p.ErrInvalid
			}
		}
	}
	return nil
}

func (s *Server) physicsWorldPending() bool {
	if len(s.physics.geometryPending) != 0 {
		return true
	}
	n := s.navigation
	if n == nil {
		return false
	}
	for _, state := range n.buildStates {
		if state.Pending {
			return true
		}
	}
	profiles := map[surface.ProfileID]bool{}
	for _, profile := range n.config.RequiredProfiles {
		profiles[profile] = true
	}
	for _, a := range n.agents {
		profiles[a.config.Profile] = true
	}
	for profile := range profiles {
		if n.pending(profile) {
			return true
		}
	}
	return false
}
