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
// frames, restoration, the per-space simulation phase and the legacy collision
// phase; use Post from a contact or navigation callback. Entities being deleted are rejected. A
// navigation-attached entity is reattached idle in the destination (see
// MoveToSpace); a physics-attached entity needs MoveToSpace with
// SpaceTransfer.RebuildPhysics and is rejected here.
func (s *Server) SetEntitySpace(id int64, space SpaceID) error {
	return s.MoveToSpace(SpaceTransfer{To: space, Entities: []int64{id}})
}

// DefaultTransferMaxPenetration is the penetration, in metres, that
// MoveToSpace tolerates when it admits a rebuilt body and
// SpaceTransfer.Placement leaves MaxPenetration zero. It covers the contact
// slop of bodies at rest.
const DefaultTransferMaxPenetration float32 = 0.01

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
	// A rebuilt character must be created with src.Controller as its
	// BodyDesc.Controller (adding only new velocity intent): when both worlds
	// implement physics3d.CharacterControllerReader, admission rejects a body
	// whose controller configuration differs from the source's, or that gains
	// or loses a controller.
	// It must only create bodies, colliders and joints in dst and must not call
	// Server APIs: AttachPhysics, AttachNavigation, CreateEntityWithSetup and
	// MoveToSpace return an error wrapping physics3d.ErrBusy, DeleteEntity is
	// deferred until the transfer has finished, and a change to a moving entity
	// rejects the transfer. Required when any moved entity is attached to
	// physics.
	//
	// Ownership: when the transfer is rolled back (the callback fails, a body is
	// rejected, MoveShapes fails, or a source cannot be detached), the engine
	// destroys only the bodies returned in the map, with their colliders and
	// joints. The callback owns everything else it created, such as shared
	// shapes or bodies it did not return, both on error and on success; on
	// success the returned bodies belong to their new attachments.
	RebuildPhysics func(dst p.World, src []PhysicsTransferSource) (map[int64]p.BodyHandle, error)
	// Placement is the penetration tolerance used to admit the rebuilt bodies
	// in the destination world. A zero MaxPenetration selects
	// DefaultTransferMaxPenetration: a body that rests on the ground or on
	// another moved body sits a few millimetres inside its support, so a
	// transfer that tolerated no penetration would reject every settled body.
	// Each body is admitted with the larger of this tolerance and its own
	// PhysicsAttachmentConfig.Placement.MaxPenetration. Set a tiny positive
	// value (such as 1e-6) to reject any real penetration. A negative or
	// non-finite value rejects the transfer.
	Placement PhysicsPlacementPolicy
	// MoveShapes moves the shapes the game registered itself on legacy
	// collision backends (outside the layer helpers, which move schema
	// colliders automatically) for every entity of the batch that changes
	// space. Optional. It is called once, on the tick goroutine, after
	// RebuildPhysics and after every check that can reject the transfer, just
	// before the commit, in Entities order; it is also called for batches
	// without physics or navigation. Find the backends with
	// Space(from).CollisionBackend / CollisionBackend3D and
	// Space(to).CollisionBackend / CollisionBackend3D: remove each shape from
	// the source backend, add it on the destination backend and Update it to
	// ShapeMove.Position.
	//
	// On error the hook must leave every shape as it found it; MoveToSpace then
	// destroys the rebuilt bodies and returns the error with nothing changed.
	// After it succeeds the transfer commits; if a backend fails during the
	// commit the affected spaces fault (Run ends) and the shapes stay moved.
	// Like RebuildPhysics, it must not call Server APIs: attach and detach
	// calls, CreateEntityWithSetup and MoveToSpace return an error wrapping
	// physics3d.ErrBusy, DeleteEntity is deferred until the transfer has
	// finished, and a change to a moving entity rejects the transfer (the
	// shapes have then already moved).
	MoveShapes func(to SpaceID, moves []ShapeMove) error
}

// ShapeMove is one entity whose game-registered collision shapes
// SpaceTransfer.MoveShapes moves.
type ShapeMove struct {
	// Entity is the moving entity.
	Entity int64
	// From is the entity's source space.
	From SpaceID
	// Position is where the entity will be in the destination: the rebuilt
	// body's gameplay anchor for a physics-attached entity, else the entity's
	// current Position3D (registry.Spatial3DEntity), else its 2D Position as
	// (X, Y, 0). Use it for 3D backends; for a 2D backend, map it to 2D the way
	// the entity's Position does (the collision step syncs 2D backends from
	// Position each tick, except for physics-attached entities).
	Position p.Vec3
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
	// Controller is the source body's current character controller policy, to
	// pass unchanged as the rebuilt body's BodyDesc.Controller. Its Velocity is
	// zero: retained velocity intent is runtime state and is not carried. It is
	// the zero (disabled) config when the source body has no enabled controller
	// or the source world does not implement physics3d.CharacterControllerReader
	// (Box3D does).
	Controller p.CharacterControllerConfig
}

// MoveToSpace moves every entity of t to t.To in one step. All entities are
// validated first; when any is rejected nothing moves. Moves are reported in
// the next ReplicationFrame.Moved.
//
// Each entity must exist, be published and not be in deletion (including a
// DeleteEntity deferred from a physics callback), and the destination must
// have the services the entity uses: a physics world for a physics attachment
// (plus t.RebuildPhysics), surface navigation for a navigation attachment.
// Entities attached to a body of loaded physics geometry are rejected; the
// geometry lease owns that body. Schema colliders need bound layer helpers in
// both spaces and a destination helper that defines their layer. MoveToSpace
// returns an error wrapping physics3d.ErrBusy while any space is stepping,
// dispatching accepted-frame callbacks, restoring or holding a prepared
// navigation frame, during the per-space simulation phase, and during the
// legacy collision phase (OnContact, OnContact3D and per-entity contact
// events); callbacks must use Server.Post. It returns ErrServerNotRunning
// while the server stops.
//
// Physics-attached entities are rebuilt before anything changes: RebuildPhysics
// runs once on the destination world and must return a body for every moved
// physics entity; each body is admitted like AttachPhysics (owner binding,
// complete-volume placement, navigation mode), except that placement uses the
// transfer's tolerance (SpaceTransfer.Placement, 1 cm by default) or the
// entity's own PhysicsPlacementPolicy when that is larger, so bodies at rest
// are accepted. Each body must keep the source body's motion mode and, when both
// worlds implement physics3d.CharacterControllerReader, its character
// controller configuration (every field except velocity intent; a body may
// neither gain nor lose a controller). Without the reader in either world the
// controller check is skipped and the callback is trusted. Every source body
// must then still be alive in a source world that has not faulted (a callback
// that destroyed one is caught here). Finally SpaceTransfer.MoveShapes, when
// set, moves the game's own collision shapes.
//
// Guarantees: a transfer rejected before the commit (validation, the
// callback fails or changes a moving entity, a body is rejected, a source
// body or world is unusable, MoveShapes fails) changes nothing: every
// returned body is destroyed with its colliders and joints. Only a backend
// failure breaks this: if a returned body cannot be destroyed, the
// destination faults. A backend failure during or after the commit faults
// the affected spaces (Run ends; navigation readiness closes) and the move
// may be partly applied.
//
// The commit then runs in this order:
//  1. Every source attachment is detached: physics (destroying the source
//     body and its joints and cancelling its timed trajectory) and navigation.
//     If a source body still cannot be destroyed, the source and destination
//     spaces fault (Run ends), the rebuilt bodies are destroyed and
//     MoveToSpace returns the error with the registry and colliders
//     unchanged; entities detached before the failure have already lost
//     their source bodies and MoveShapes has already moved the shapes.
//  2. Each source world that lost bodies is synchronized; a failure faults
//     that space but the commit continues.
//  3. The registry space of every entity changes.
//  4. Schema colliders leave the old space's layers and register on the new
//     space's layers, at the entity's current position.
//  5. Physics is attached to each new body with the same
//     PhysicsAttachmentConfig. The new body's gameplay anchor defines the
//     entity position, written through its Position3DWriter.
//  6. Navigation traversal-cancel callbacks of the detached source agents run,
//     with the entities already in the destination.
//  7. Navigation is reattached in the destination with the same configuration
//     but no destination intent (idle), unless a cancel callback deleted the
//     entity, attached navigation itself, or detached the physics a staged
//     agent needs.
//
// Not carried to the destination: timed trajectories, physics contact history,
// navigation routes and destinations, pending forces and retained controller
// velocity intent, and joints to bodies that were not moved. Physics commands
// queued in the source space still run there; one queued before the transfer
// that fails with a *physics3d.StaleHandleError naming a source body the
// transfer destroyed is dropped instead of faulting the space. Any other
// error still faults it, including a stale handle of an unrelated body and a
// stale collider or joint handle that was destroyed with a moved body (no API
// lists those). Shapes the game
// registered on a legacy collision backend itself move only through
// SpaceTransfer.MoveShapes.
//
// Until step 7 has run, the moved navigation-attached entities cannot move
// again: a traversal-cancel callback calling MoveToSpace for one of them gets
// an error wrapping physics3d.ErrBusy (other entities may move).
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
// per-space simulation phase, during the legacy collision phase (contact
// callbacks) and while another transfer runs.
func (s *Server) checkSpaceMutable(op string) error {
	s.lifeMu.Lock()
	stopping := s.life == lifecycleStopping || s.life == lifecycleStopped
	s.lifeMu.Unlock()
	if stopping {
		return ErrServerNotRunning
	}
	if s.transferring || s.simulatingSpaces || s.collisionStepping {
		return fmt.Errorf("golem: %s is unavailable while spaces simulate or transfer: %w", op, p.ErrBusy)
	}
	return s.servicesQuiescent(op)
}

// transferBusy rejects an attachment change made while a transfer runs, such
// as from SpaceTransfer.RebuildPhysics or MoveShapes, which must not call
// Server APIs.
func (s *Server) transferBusy(op string) error {
	if s.transferring {
		return fmt.Errorf("golem: %s is unavailable during a space transfer: %w", op, p.ErrBusy)
	}
	return nil
}

// SetSessionSpace sets the space of a session that has neither an FOI anchor
// nor an avatar, such as a spectator or a client in a menu. The session's
// effective space comes from, in order: its FOI anchor, its avatar, this
// explicit space, the last space of its deleted avatar, and finally space 0.
// Space 0 clears the explicit space, and every call forgets the deleted
// avatar's space. A session with an FOI whose anchor is missing keeps its
// last space; the avatar or explicit space only seeds it when the FOI is
// assigned.
//
// A session whose avatar is deleted therefore stays where the avatar was
// until it gets a new avatar or this method is called: to send a dead player
// elsewhere, call SetSessionSpace (before or after deleting the avatar).
//
// The change is announced to the client on the next replication pass as a
// space transition; server events sent to the session until then follow the
// transition. Concurrency-safe; the setting is dropped on disconnect, and a
// call for a session that is not connected (unknown, or already disconnected)
// is ignored.
func (s *Server) SetSessionSpace(sessionID int64, space SpaceID) {
	s.interestMu.Lock()
	if _, closed := s.closedSessions[sessionID]; closed {
		s.interestMu.Unlock()
		return
	}
	delete(s.sessionSpaceKept, sessionID)
	if space == 0 {
		delete(s.sessionSpaceExplicit, sessionID)
	} else {
		s.sessionSpaceExplicit[sessionID] = space
	}
	pending := s.switchPendingLocked(sessionID)
	s.interestMu.Unlock()
	// The listener is never called under interestMu, so check afterwards: a
	// session it does not list is gone for good (or never existed) and its
	// disconnect cleanup may already have run, so nothing else would remove
	// the entry.
	if _, listed := s.listener.SessionTransport(sessionID); !listed {
		s.interestMu.Lock()
		delete(s.sessionSpaceExplicit, sessionID)
		s.interestMu.Unlock()
		return
	}
	if pending {
		s.holdSessionEvents(sessionID)
	}
}

// SessionSpace returns the session's effective space: the space of its FOI
// anchor, else of its avatar, else the space set with SetSessionSpace, else
// the last space of its deleted avatar, else 0.
// The client learns about a change on the next replication pass; until that
// pass has queued the session's transition, SessionsInSpace still lists the
// session in its announced (old) space.
func (s *Server) SessionSpace(sessionID int64) SpaceID {
	s.interestMu.Lock()
	defer s.interestMu.Unlock()
	return s.effectiveSessionSpaceLocked(sessionID)
}

// SessionsInSpace returns the connected sessions whose announced space is
// space. Every session starts in space 0 and changes space only through a
// replication space transition; a session counts as announced in its new
// space once the transition frames (SpaceChanged onwards) are queued, so this
// matches what each client has been told, even from OnUpdates on the
// replication pass that plans the transition. World pushes use this list;
// Space.BroadcastServerEvent follows each session to the space it is moving
// to instead, because its events are held until the transition.
func (s *Server) SessionsInSpace(space SpaceID) []int64 {
	all := s.listener.SessionIDs()
	s.interestMu.Lock()
	defer s.interestMu.Unlock()
	var out []int64
	for _, sid := range all {
		if s.announcedSpaceLocked(sid) == space {
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
// Server.SessionsInSpace. BroadcastServerEvent chooses its recipients by the
// space each session is in or moving to instead.
func (sp *Space) Sessions() []int64 { return sp.srv.SessionsInSpace(sp.id) }

// BroadcastServerEvent sends one marshalled ServerEvent to every session in
// this space, on the chosen delivery lane with the same stream fallback as
// Server.BroadcastServerEvent. Sessions in other spaces receive nothing.
// Sessions that disconnect meanwhile are skipped.
//
// Recipients are chosen by the space each session is in or moving to
// (Server.SessionSpace), not by its announced space: a session that changed
// space earlier on this tick receives the events of its new space, held and
// delivered right after its transition (behind SpaceSettled), and no longer
// receives the events of the space it is leaving. Space.Sessions still lists
// such a session in its old space until the transition is queued. Recipient
// selection and the enqueue are atomic with space transitions, so the event
// is ordered with them from any goroutine.
func (sp *Space) BroadcastServerEvent(delivery EventDelivery, event []byte) error {
	wrapped := WrapServerEvent(event)
	s := sp.srv
	s.announceMu.RLock()
	defer s.announceMu.RUnlock()
	sessions, moving := s.spaceEventRecipients(sp.id)
	if len(sessions) == 0 {
		return nil
	}
	// A recipient still on its way holds the event until its transition is
	// queued; most hold already (every space change marks its sessions), this
	// covers the changes that do not.
	s.holdSessionEvents(moving...)
	return s.listener.SendEventTo(sessions, delivery, event, wrapped)
}

// spaceEventRecipients returns the connected sessions whose effective space
// is space, and those among them whose client has not been told yet (a switch
// is pending, or planned and not queued). Call with announceMu held.
func (s *Server) spaceEventRecipients(space SpaceID) (sessions, moving []int64) {
	connected := s.listener.SessionIDs()
	s.interestMu.Lock()
	defer s.interestMu.Unlock()
	if !s.spacesActiveLocked() {
		// Every session is in space 0 and knows it.
		if space != 0 {
			return nil, nil
		}
		return connected, nil
	}
	for _, sid := range connected {
		effective := s.effectiveSessionSpaceLocked(sid)
		if effective != space {
			continue
		}
		sessions = append(sessions, sid)
		if s.announcedSpaceLocked(sid) != effective {
			moving = append(moving, sid)
		}
	}
	return sessions, moving
}
