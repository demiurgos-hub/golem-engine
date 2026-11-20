package golem

import (
	"fmt"

	"github.com/demiurgos-hub/golem-engine/golem/nav/surface"
	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
)

// PreparedPhysicsRecovery owns one space's privately reconstructed desired world
// pair. The host applies the space's captured desired source to its captured
// accepted physical state, migrating body/joint state by stable IDs, and
// prepares matching navigation. Nothing may mutate live state before return.
// Geometry identifies the resulting source leases; callback implementations and
// backend handles are not persisted. A multi-space restore prepares one pair per
// pending space (PhysicsRestoreOptions.PreparePendingWorld for space 0,
// PhysicsSpaceRestoreOptions.PreparePendingWorld for the others); its identity
// must match that space's section.
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

func (r PreparedPhysicsRecovery) validate(checkpointID string, c PhysicsSpaceCheckpoint) error {
	if r.Physics == nil || r.Navigation == nil || len(r.Geometry) > 100000 || r.CheckpointID != checkpointID || r.PhysicsFingerprint != c.DesiredPhysicsFingerprint || r.NavigationFingerprint != c.Navigation.SourceFingerprint || r.PhysicsRevision != c.DesiredPhysicsRevision || r.NavigationRevision != c.Navigation.DesiredRevision {
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

// physicsWorldPending reports whether space 0's physical or navigation world
// has pending geometry; checkpoint capture asks each space's runtime.
func (s *Server) physicsWorldPending() bool { return s.space0.physicsWorldPending() }

func (rt *spaceRuntime) physicsWorldPending() bool {
	if len(rt.physics.geometryPending) != 0 {
		return true
	}
	n := rt.navigation
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
