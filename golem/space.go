package golem

import (
	"fmt"

	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
	"github.com/demiurgos-hub/golem-engine/golem/registry"
)

// SpaceID identifies a separate coordinate space. Every entity belongs to one
// space; space 0 is the default and the only space a single-space game uses.
type SpaceID = registry.SpaceID

// EntitySpace returns the space of a published or provisional entity, or
// (0, false) when the entity is unknown.
func (s *Server) EntitySpace(id int64) (SpaceID, bool) {
	return s.reg.Space(id)
}

// SetEntitySpace moves a published entity to space. It is the low-level
// mover for plain entities: it changes only the entity's space and reports the
// change once in the next ReplicationFrame.Moved. Several moves of one entity
// between flushes coalesce, and moving to the current space is a no-op.
// Replication treats the entity as part of its new space from the next pass:
// sessions in the old space see it exit and sessions in the new space see it
// enter, except for sessions that move along with it (see MoveToSpace).
//
// Schema colliders (ColliderProvider / ColliderProvider3D) move with the
// entity: they leave the old space's collision layers and register, with the
// provider's current shape, on the new space's layers (none when the new space
// has no layer helper). A collider layer the destination's helper does not
// define rejects the move.
//
// Call before Run or on the tick goroutine, outside physics callbacks, active
// frames, restoration and the per-space simulation phase; use Post from a
// contact or navigation callback. Entities being deleted are rejected. A
// navigation-attached entity is reattached idle in the destination (see
// MoveToSpace); a physics-attached entity needs MoveToSpace with
// SpaceTransfer.RebuildPhysics and is rejected here.
func (s *Server) SetEntitySpace(id int64, space SpaceID) error {
	return s.MoveToSpace(SpaceTransfer{To: space, Entities: []int64{id}})
}

// SpaceTransfer describes entities that move to one space together, such as a
// player's avatar with the crate it carries.
type SpaceTransfer struct {
	// To is the destination space.
	To SpaceID
	// Entities lists the published entities to move. Duplicates are rejected.
	Entities []int64
	// RebuildPhysics creates the replacement bodies of the physics-attached
	// entities in the destination world dst, while the source bodies still
	// exist. It is called once, on the tick goroutine, with every moved physics
	// entity's accepted source state (in Entities order), and returns the new
	// body for each of them; joints between moved bodies are recreated here too.
	// It must only create bodies, colliders and joints in dst and must not call
	// Server APIs. Required when any moved entity is attached to physics.
	RebuildPhysics func(dst p.World, src []PhysicsTransferSource) (map[int64]p.BodyHandle, error)
}

// PhysicsTransferSource is one physics-attached entity moved by MoveToSpace,
// as seen by SpaceTransfer.RebuildPhysics.
type PhysicsTransferSource struct {
	// Entity is the moving entity.
	Entity int64
	// From is the entity's source space.
	From SpaceID
	// Body is the accepted state of the source body (pose, gameplay anchor,
	// mode, mass, velocities, activation and character support). Its handle
	// belongs to the source world.
	Body p.BodyState
	// Config is the attachment configuration the destination attachment keeps.
	Config PhysicsAttachmentConfig
}

// MoveToSpace moves every entity of t to t.To in one step. All entities are
// validated first; when any is rejected nothing moves. Moves are reported in
// the next ReplicationFrame.Moved.
//
// Each entity must exist, be published and not be in deletion, and the
// destination must have the services the entity uses: a physics world for a
// physics attachment (plus t.RebuildPhysics), surface navigation for a
// navigation attachment. MoveToSpace returns an error wrapping
// physics3d.ErrBusy while any space is stepping, dispatching accepted-frame
// callbacks, restoring or holding a prepared navigation frame, and during the
// per-space simulation phase; callbacks must use Server.Post. It returns
// ErrServerNotRunning while the server stops.
//
// Physics-attached entities are rebuilt before anything changes: RebuildPhysics
// runs once on the destination world and must return a body for every moved
// physics entity; each body is admitted like AttachPhysics (owner binding,
// complete-volume placement with the entity's PhysicsPlacementPolicy,
// navigation mode) and must keep the source body's motion mode. When the
// callback fails or any body is rejected, every returned body is destroyed
// (with its colliders and joints) and nothing changes. Then the source
// attachments are detached (destroying the source bodies and their joints and
// cancelling timed trajectories and navigation), schema colliders and the
// registry space move, navigation is reattached in the destination with the
// same configuration but no destination intent (idle), and physics is
// attached to the new body with the same PhysicsAttachmentConfig. The new
// body's gameplay anchor defines the entity position, written through its
// Position3DWriter.
//
// Not carried to the destination: timed trajectories, contact history,
// navigation routes and destinations, pending forces and retained controller
// velocity intent, physics commands queued in the source space, and joints to
// bodies that were not moved. A failing source body destruction faults the
// source space (Run ends).
//
// A session whose avatar or FOI anchor moves follows it to the new space on the
// next replication pass. The entities it already knew that moved with it are
// carried: the client keeps them and receives their full state inside the
// space transition instead of a removal and respawn.
func (s *Server) MoveToSpace(t SpaceTransfer) error {
	const op = "MoveToSpace"
	if len(t.Entities) == 1 {
		// Keep SetEntitySpace's historical diagnostics.
		if err := s.checkSpaceMutable("SetEntitySpace"); err != nil {
			return err
		}
		return s.applySpaceMove("SetEntitySpace", t)
	}
	if err := s.checkSpaceMutable(op); err != nil {
		return err
	}
	seen := make(map[int64]struct{}, len(t.Entities))
	for _, id := range t.Entities {
		if _, dup := seen[id]; dup {
			return fmt.Errorf("golem: %s: entity %d is listed twice", op, id)
		}
		seen[id] = struct{}{}
	}
	return s.applySpaceMove(op, t)
}

// checkSpaceMutable rejects space changes while stopping, during any space's
// physics steps and callbacks, navigation frames and restoration, during the
// per-space simulation phase and while another transfer runs.
func (s *Server) checkSpaceMutable(op string) error {
	s.lifeMu.Lock()
	stopping := s.life == lifecycleStopping || s.life == lifecycleStopped
	s.lifeMu.Unlock()
	if stopping {
		return ErrServerNotRunning
	}
	if s.transferring || s.simulatingSpaces {
		return fmt.Errorf("golem: %s is unavailable while spaces simulate or transfer: %w", op, p.ErrBusy)
	}
	return s.servicesQuiescent(op)
}

// SetSessionSpace sets the space of a session that has neither an FOI anchor
// nor an avatar, such as a spectator or a client in a menu. The session's
// effective space comes from, in order: its FOI anchor, its avatar, this
// explicit space, and finally space 0. Space 0 clears the explicit space. A
// session with an FOI whose anchor is missing keeps its last space; the
// avatar or explicit space only seeds it when the FOI is assigned.
//
// The change is announced to the client on the next replication pass as a
// space transition. Concurrency-safe; the setting is dropped on disconnect.
func (s *Server) SetSessionSpace(sessionID int64, space SpaceID) {
	s.interestMu.Lock()
	defer s.interestMu.Unlock()
	if space == 0 {
		delete(s.sessionSpaceExplicit, sessionID)
		return
	}
	s.sessionSpaceExplicit[sessionID] = space
}

// SessionSpace returns the session's effective space: the space of its FOI
// anchor, else of its avatar, else the space set with SetSessionSpace, else 0.
// The client learns about a change on the next replication pass; until then
// SessionsInSpace still lists the session in its announced space.
func (s *Server) SessionSpace(sessionID int64) SpaceID {
	s.interestMu.Lock()
	defer s.interestMu.Unlock()
	return s.effectiveSessionSpaceLocked(sessionID)
}

// SessionsInSpace returns the connected sessions whose announced space is
// space, that is, the space whose entities they currently replicate. Every
// session starts in space 0 and changes space only through a replication
// space transition, so this matches what each client has been told.
func (s *Server) SessionsInSpace(space SpaceID) []int64 {
	all := s.listener.SessionIDs()
	s.interestMu.Lock()
	defer s.interestMu.Unlock()
	var out []int64
	for _, sid := range all {
		if s.sentSpace[sid] == space {
			out = append(out, sid)
		}
	}
	return out
}

// Space is a lightweight handle to one space of a Server. Handles are cheap;
// two handles for the same ID behave identically. Each space has its own
// legacy collision backends and layer helpers, spatial queries, 2D nav
// backend, physics world and surface navigation (space_services.go); the
// matching Server methods act on space 0.
type Space struct {
	srv *Server
	id  SpaceID
}

// Space returns a handle to space id. The space does not need any entity.
func (s *Server) Space(id SpaceID) *Space {
	return &Space{srv: s, id: id}
}

// ID returns the space's identifier.
func (sp *Space) ID() SpaceID { return sp.id }

// Sessions returns the connected sessions announced in this space; see
// Server.SessionsInSpace.
func (sp *Space) Sessions() []int64 { return sp.srv.SessionsInSpace(sp.id) }

// BroadcastServerEvent sends one marshalled ServerEvent to every session
// announced in this space, on the chosen delivery lane with the same stream
// fallback as Server.BroadcastServerEvent. Sessions in other spaces receive
// nothing. Sessions that disconnect meanwhile are skipped.
func (sp *Space) BroadcastServerEvent(delivery EventDelivery, event []byte) error {
	sessions := sp.Sessions()
	if len(sessions) == 0 {
		return nil
	}
	return sp.srv.listener.SendEventTo(sessions, delivery, event, WrapServerEvent(event))
}
