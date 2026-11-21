package golem

import (
	"errors"
	"fmt"
	"slices"

	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
	"github.com/demiurgos-hub/golem-engine/golem/registry"
)

// spaceTransferEntity is one moving entity with physics or navigation
// attachments in its source space.
type spaceTransferEntity struct {
	id        int64
	from      *spaceRuntime
	physics   *physicsAttachment // source attachment; nil without physics
	source    p.BodyState        // accepted source body state
	nav       bool
	navConfig NavigationAgentConfig
	body      p.BodyHandle // admitted destination body
	state     p.BodyState  // admitted destination body state
	// controller is the source body's controller policy with Velocity cleared
	// (zero without a controller); controllerKnown is false when the source
	// world does not implement physics3d.CharacterControllerReader.
	controller      p.CharacterControllerConfig
	controllerKnown bool
}

// applySpaceMove validates every entity of t, including its legacy collider
// transfer and the services it needs in t.To, rebuilds physics bodies in the
// destination world, checks that every source body is still alive in a usable
// world (preflightTransferSources), calls t.MoveShapes, and then commits in
// this order: detach every source attachment (physics and navigation), move
// the registry space (under interestMu, so the batch is atomic for
// session-space readers), move legacy colliders, attach destination physics,
// run the deferred traversal-cancel callbacks, and attach destination
// navigation. Anything rejected before the detach loop changes nothing (the
// rebuilt bodies are destroyed; failing to destroy them faults the
// destination). A backend failure from the detach loop on faults every
// affected space (faultServices) and may leave the move partly applied.
// Sessions whose avatar or FOI anchor moved hold their server events until
// their transition is queued (space_events.go).
func (s *Server) applySpaceMove(op string, t SpaceTransfer) error {
	if !p.Finite(t.Placement.MaxPenetration) || t.Placement.MaxPenetration < 0 {
		return fmt.Errorf("golem: %s: SpaceTransfer.Placement.MaxPenetration must be finite and not negative: %w", op, p.ErrInvalid)
	}
	dst := s.runtime(t.To)
	var moves []spaceTransferEntity
	physicsMoves := 0
	froms := make([]SpaceID, len(t.Entities))
	for i, id := range t.Entities {
		from, published := s.reg.PublishedSpace(id)
		if !published {
			return fmt.Errorf("golem: %s: entity %d is not published", op, id)
		}
		froms[i] = from
		src := s.runtime(from)
		if _, pending := s.navReattachPending[id]; pending {
			// A traversal-cancel callback of an outer transfer runs before this
			// entity's navigation is reattached in its new space.
			return fmt.Errorf("golem: %s: entity %d is awaiting navigation reattachment after a space transfer: %w", op, id, p.ErrBusy)
		}
		if n := src.nav(); n != nil && n.deleting[id] {
			return fmt.Errorf("golem: %s: entity %d is being deleted", op, id)
		}
		if src.phys().deletePending(id) {
			return fmt.Errorf("golem: %s: entity %d has a pending deletion", op, id)
		}
		if from == t.To {
			continue
		}
		m := spaceTransferEntity{id: id, from: src}
		if ps := src.phys(); ps != nil {
			m.physics = ps.agents[id]
		}
		if n := src.nav(); n != nil && n.agents[id] != nil {
			m.nav, m.navConfig = true, n.agents[id].config
		}
		if m.physics != nil {
			if src.physics.geometryOwns(m.physics.body) {
				return fmt.Errorf("golem: %s: entity %d is attached to a body of loaded physics geometry, which cannot leave its space", op, id)
			}
			if dst.phys() == nil {
				return fmt.Errorf("golem: %s: entity %d is attached to physics but space %d has no physics world", op, id, t.To)
			}
			if t.RebuildPhysics == nil {
				return fmt.Errorf("golem: %s: entity %d is attached to physics; SpaceTransfer.RebuildPhysics is required", op, id)
			}
			physicsMoves++
		}
		if m.nav && dst.nav() == nil {
			return fmt.Errorf("golem: %s: entity %d is attached to navigation but space %d has no surface navigation", op, id, t.To)
		}
		if m.nav && m.navConfig.StagedPhysics && m.physics == nil && dst.phys() != nil {
			// The destination's physics step requires a body for every staged agent.
			return fmt.Errorf("golem: %s: entity %d has staged navigation without a physics attachment", op, id)
		}
		if m.physics != nil || m.nav {
			moves = append(moves, m)
		}
	}
	colliders, err := s.planColliderMoves(op, t)
	if err != nil {
		return err
	}
	if len(moves) == 0 {
		if t.MoveShapes != nil {
			if shapes := s.transferShapeMoves(t, froms, nil); len(shapes) != 0 {
				s.transferring = true
				defer s.finishTransfer()
				if err := s.moveTransferShapes(op, t, froms, nil, colliders, shapes); err != nil {
					return err
				}
			}
		}
		bad, moved := s.setSpacesHoldingEvents(t)
		if !moved {
			return fmt.Errorf("golem: %s: entity %d is not published", op, bad)
		}
		applyColliderMoves(colliders)
		return nil
	}

	s.transferring = true
	defer s.finishTransfer()
	var created map[int64]p.BodyHandle
	if physicsMoves != 0 {
		created, err = s.rebuildTransferPhysics(op, t, dst, moves)
		if err != nil {
			return err
		}
		// RebuildPhysics must not call Server APIs; make sure it changed nothing
		// the commit relies on before anything changes.
		if err := s.transferUnchanged(op, "RebuildPhysics", t, froms, moves, colliders); err != nil {
			return errors.Join(err, rollbackTransferBodies(op, dst, created))
		}
		// The detach loop must not fail on a dead body or an unusable world:
		// check every source before the destructive phase.
		if err := preflightTransferSources(op, moves); err != nil {
			return errors.Join(err, rollbackTransferBodies(op, dst, created))
		}
	}
	if t.MoveShapes != nil {
		if shapes := s.transferShapeMoves(t, froms, moves); len(shapes) != 0 {
			if err := s.moveTransferShapes(op, t, froms, moves, colliders, shapes); err != nil {
				return errors.Join(err, rollbackTransferBodies(op, dst, created))
			}
		}
	}

	// Traversal cancellation callbacks of the detached source agents run only
	// after the registry and physics have committed.
	var deferred []*navigationService
	for _, m := range moves {
		if n := m.from.nav(); n != nil && n.frameCallbacks == nil {
			n.frameCallbacks = make([]func(), 0)
			deferred = append(deferred, n)
		}
	}
	runDeferred := func() {
		var callbacks []func()
		for _, n := range deferred {
			callbacks = append(callbacks, n.frameCallbacks...)
			n.frameCallbacks = nil
		}
		deferred = nil
		s.transferring = false
		for _, callback := range callbacks {
			callback()
		}
	}

	// Detach every source first; this starts the destructive phase. The
	// preflight makes a failure here a backend failure: it leaves the registry
	// and colliders unchanged, but entities detached before it have lost their
	// source bodies, which cannot be restored, and MoveShapes has already moved
	// the game's shapes. The source and the destination fault (Run ends).
	var retired []*physicsService
	for _, m := range moves {
		if m.physics != nil {
			ps := m.from.physics
			// Destroys the source body with its colliders and joints, cancels its
			// timed trajectory and detaches its source navigation.
			body := m.physics.body
			if err := ps.detach(m.id, p.Teleported); err != nil {
				failure := fmt.Errorf("golem: %s: detach entity %d from space %d physics: %w", op, m.id, m.from.id, err)
				m.from.faultServices(failure)
				dst.faultServices(failure)
				syncErr := synchronizeRetired(op, retired)
				runDeferred()
				return errors.Join(failure, syncErr, rollbackTransferBodies(op, dst, created))
			}
			ps.retire(body)
			if !slices.Contains(retired, ps) {
				retired = append(retired, ps)
			}
		}
		if m.nav {
			m.from.navigation.detach(m.id)
		}
	}
	// Destroyed bodies leave the source worlds unsynchronized; publish them so
	// source queries and checkpoint captures work again. A failure faults that
	// space, but the batch still commits: its source bodies are gone.
	syncErr := synchronizeRetired(op, retired)

	bad, moved := s.setSpacesHoldingEvents(t)
	if !moved {
		// Unreachable after validation; keep the registry and colliders. The
		// detached entities are attached nowhere now: fault the destination and
		// every space that lost bodies.
		failure := fmt.Errorf("golem: %s: entity %d is not published", op, bad)
		dst.faultServices(failure)
		for _, ps := range retired {
			ps.space.faultServices(failure)
		}
		runDeferred()
		return errors.Join(failure, syncErr, rollbackTransferBodies(op, dst, created))
	}
	applyColliderMoves(colliders)

	for _, m := range moves {
		if m.physics != nil {
			old := m.physics
			a := &physicsAttachment{id: m.id, body: m.body, entity: old.entity, incarnation: old.incarnation, position: old.position, writer: old.writer,
				rotation: old.rotation, velocity: old.velocity, activation: old.activation, config: old.config,
				localAnchor: m.state.Pose.Inverse().Compose(m.state.Anchor)}
			dst.physics.agents[m.id], dst.physics.bodies[m.body] = a, m.id
			dst.physics.write(a, m.state, true)
		}
	}
	// Until navigation is reattached below, a cancel callback must not move
	// these entities again: the nested move would find no agent to carry and
	// this transfer would then skip the reattachment.
	var reattach []int64
	for _, m := range moves {
		if m.nav {
			reattach = append(reattach, m.id)
		}
	}
	if len(reattach) != 0 {
		if s.navReattachPending == nil {
			s.navReattachPending = make(map[int64]struct{})
		}
		for _, id := range reattach {
			s.navReattachPending[id] = struct{}{}
		}
		defer func() {
			for _, id := range reattach {
				delete(s.navReattachPending, id)
			}
		}()
	}
	runDeferred()
	// A cancel callback may have deleted or re-attached the entity.
	for _, m := range moves {
		if !m.nav {
			continue
		}
		n := dst.nav()
		if space, ok := s.reg.PublishedSpace(m.id); !ok || space != t.To || n == nil || n.agents[m.id] != nil || n.deleting[m.id] {
			continue
		}
		if m.navConfig.StagedPhysics && dst.phys() != nil && !dst.physicsAttached(m.id) {
			continue
		}
		n.addAgent(m.id, m.navConfig)
	}
	return syncErr
}

// finishTransfer ends a transfer and applies the DeleteEntity calls deferred
// while it ran.
func (s *Server) finishTransfer() {
	s.transferring = false
	deletes := s.transferDeletes
	s.transferDeletes = nil
	for _, id := range deletes {
		s.DeleteEntity(id)
	}
}

// transferUnchanged checks, after RebuildPhysics or MoveShapes (named by
// phase), that every entity is still published in the space validation found
// it in, is the same entity (registry incarnation: Entity values may be
// non-comparable), is not being deleted and keeps the source attachments the
// commit will detach.
func (s *Server) transferUnchanged(op, phase string, t SpaceTransfer, froms []SpaceID, moves []spaceTransferEntity, colliders []colliderMove) error {
	for i, id := range t.Entities {
		if space, ok := s.reg.PublishedSpace(id); !ok || space != froms[i] {
			return fmt.Errorf("golem: %s: entity %d changed during %s", op, id, phase)
		}
		src := s.runtime(froms[i])
		if n := src.nav(); n != nil && n.deleting[id] || src.phys().deletePending(id) {
			return fmt.Errorf("golem: %s: entity %d changed during %s", op, id, phase)
		}
	}
	for _, m := range moves {
		var physics *physicsAttachment
		if ps := m.from.phys(); ps != nil {
			physics = ps.agents[m.id]
		}
		n := m.from.nav()
		if physics != m.physics || (n != nil && n.agents[m.id] != nil) != m.nav {
			return fmt.Errorf("golem: %s: entity %d attachments changed during %s", op, m.id, phase)
		}
		if m.physics != nil {
			if inc, ok := s.reg.Incarnation(m.id); !ok || inc != m.physics.incarnation {
				return fmt.Errorf("golem: %s: entity %d changed during %s", op, m.id, phase)
			}
		}
	}
	for _, c := range colliders {
		if inc, ok := s.reg.Incarnation(c.id); !ok || inc != c.incarnation {
			return fmt.Errorf("golem: %s: entity %d changed during %s", op, c.id, phase)
		}
	}
	return nil
}

// preflightTransferSources checks, before the destructive phase, that every
// moving physics entity's source service accepts mutations, its world has not
// faulted (Box3D still answers Body in a faulted world) and its source body is
// still alive, so the detach loop cannot fail on a body RebuildPhysics
// destroyed through another reference or on an unusable world.
func preflightTransferSources(op string, moves []spaceTransferEntity) error {
	checked := make(map[*physicsService]bool)
	for _, m := range moves {
		if m.physics == nil {
			continue
		}
		ps := m.from.physics
		if !checked[ps] {
			if err := ps.mutable(); err != nil {
				return fmt.Errorf("golem: %s: space %d physics: %w", op, m.from.id, err)
			}
			if fault := ps.world.Metrics().Fault; fault != nil {
				return fmt.Errorf("golem: %s: space %d physics world: %w: %v", op, m.from.id, p.ErrFaulted, fault)
			}
			checked[ps] = true
		}
		state, err := ps.world.Body(m.physics.body)
		if err == nil && state.Handle != m.physics.body {
			err = p.ErrStale
		}
		if err != nil {
			return fmt.Errorf("golem: %s: source body of entity %d in space %d: %w", op, m.id, m.from.id, err)
		}
	}
	return nil
}

// transferShapeMoves lists the entities of t that change space, for
// SpaceTransfer.MoveShapes, with their destination position: the rebuilt
// body's gameplay anchor for a physics entity, else the entity's Position3D,
// else its 2D Position with Z = 0.
func (s *Server) transferShapeMoves(t SpaceTransfer, froms []SpaceID, moves []spaceTransferEntity) []ShapeMove {
	anchors := make(map[int64]p.Vec3)
	for _, m := range moves {
		if m.physics != nil {
			anchors[m.id] = m.state.Anchor.Position
		}
	}
	var out []ShapeMove
	for i, id := range t.Entities {
		if froms[i] == t.To {
			continue
		}
		position, ok := anchors[id]
		if !ok {
			if e, found := s.reg.Get(id); found {
				if sp, is3D := e.(registry.Spatial3DEntity); is3D {
					x, y, z := sp.Position3D()
					position = p.Vec3{X: x, Y: y, Z: z}
				} else {
					x, y := e.Position()
					position = p.Vec3{X: x, Y: y}
				}
			}
		}
		out = append(out, ShapeMove{Entity: id, From: froms[i], Position: position})
	}
	return out
}

// moveTransferShapes calls t.MoveShapes (s.transferring is set) and then
// checks that the hook changed no moving entity. A hook error changes nothing
// on the engine's side. A hook that changed an entity has already moved its
// shapes, which the engine cannot undo; the move is still rejected.
func (s *Server) moveTransferShapes(op string, t SpaceTransfer, froms []SpaceID, moves []spaceTransferEntity, colliders []colliderMove, shapes []ShapeMove) error {
	if err := t.MoveShapes(t.To, shapes); err != nil {
		return fmt.Errorf("golem: %s: move shapes: %w", op, err)
	}
	return s.transferUnchanged(op, "MoveShapes", t, froms, moves, colliders)
}

// synchronizeRetired advances the retire epoch of every source physics service
// that destroyed bodies in a transfer (retire recorded each destroyed body at
// the new epoch, so commands queued earlier that fail on those bodies' stale
// handles are dropped instead of faulting) and synchronizes its world.
// A failing synchronization faults that space (faultServices).
func synchronizeRetired(op string, retired []*physicsService) error {
	var errs []error
	for _, ps := range retired {
		ps.retireEpoch++
		if err := ps.world.Synchronize(); err != nil {
			err = fmt.Errorf("golem: %s: synchronize space %d physics: %w", op, ps.space.id, err)
			ps.space.faultServices(err)
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// rebuildTransferPhysics captures the source states (and character
// controllers, when the source world reports them) of the moving physics
// entities, calls RebuildPhysics once on the destination world and admits each
// returned body. On any failure every returned body is destroyed and nothing
// else has changed (failing to destroy them faults the destination). Admitted
// bodies are recorded in moves. A failing source Synchronize is a plain
// rejection: it only fails for a closed, faulted or busy world and changes no
// state.
func (s *Server) rebuildTransferPhysics(op string, t SpaceTransfer, dst *spaceRuntime, moves []spaceTransferEntity) (map[int64]p.BodyHandle, error) {
	dps := dst.physics
	var sources []PhysicsTransferSource
	synchronized := map[*physicsService]bool{}
	for i := range moves {
		m := &moves[i]
		if m.physics == nil {
			continue
		}
		ps := m.from.physics
		if !synchronized[ps] {
			if err := ps.world.Synchronize(); err != nil {
				return nil, fmt.Errorf("golem: %s: synchronize space %d physics: %w", op, m.from.id, err)
			}
			synchronized[ps] = true
		}
		state, err := ps.world.Body(m.physics.body)
		if err != nil {
			return nil, fmt.Errorf("golem: %s: read entity %d body: %w", op, m.id, err)
		}
		m.source = state
		if reader, ok := ps.world.(p.CharacterControllerReader); ok {
			ctrl, character, err := reader.CharacterController(m.physics.body)
			if err != nil {
				return nil, fmt.Errorf("golem: %s: read entity %d controller: %w", op, m.id, err)
			}
			if character {
				m.controller = transferControllerConfig(ctrl)
			}
			m.controllerKnown = true
		}
		sources = append(sources, PhysicsTransferSource{Entity: m.id, From: m.from.id, Body: state, Config: m.physics.config, Controller: m.controller})
	}
	created, err := t.RebuildPhysics(dps.world, sources)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("golem: %s: rebuild physics: %w", op, err), rollbackTransferBodies(op, dst, created))
	}
	if err := s.admitTransferBodies(op, dps, t.Placement, moves, created); err != nil {
		return nil, errors.Join(err, rollbackTransferBodies(op, dst, created))
	}
	return created, nil
}

// transferPlacement returns the policy that admits one rebuilt body: the
// transfer's tolerance (DefaultTransferMaxPenetration when zero) or the
// entity's own, whichever permits more.
func transferPlacement(transfer, entity PhysicsPlacementPolicy) PhysicsPlacementPolicy {
	if transfer.MaxPenetration == 0 {
		transfer.MaxPenetration = DefaultTransferMaxPenetration
	}
	if entity.MaxPenetration > transfer.MaxPenetration {
		return entity
	}
	return transfer
}

func (s *Server) admitTransferBodies(op string, dps *physicsService, placement PhysicsPlacementPolicy, moves []spaceTransferEntity, created map[int64]p.BodyHandle) error {
	wanted := make(map[int64]bool)
	for _, m := range moves {
		if m.physics != nil {
			wanted[m.id] = true
		}
	}
	for id := range created {
		if !wanted[id] {
			return fmt.Errorf("golem: %s: RebuildPhysics returned a body for entity %d, which moves without physics", op, id)
		}
	}
	if err := dps.world.Synchronize(); err != nil {
		return fmt.Errorf("golem: %s: synchronize destination physics: %w", op, err)
	}
	seen := make(map[p.BodyHandle]int64)
	for i := range moves {
		m := &moves[i]
		if m.physics == nil {
			continue
		}
		h, ok := created[m.id]
		if !ok || h == (p.BodyHandle{}) {
			return fmt.Errorf("golem: %s: RebuildPhysics returned no body for entity %d", op, m.id)
		}
		if other, duplicate := seen[h]; duplicate {
			return fmt.Errorf("golem: %s: entities %d and %d received the same body", op, other, m.id)
		}
		seen[h] = m.id
		if _, attached := dps.bodies[h]; attached {
			return fmt.Errorf("golem: %s: rebuilt body of entity %d is already attached", op, m.id)
		}
		if err := s.foreignPhysicsBody(dps, h); err != nil {
			return fmt.Errorf("golem: %s: rebuilt body of entity %d: %w", op, m.id, err)
		}
		state, err := dps.world.Body(h)
		if err != nil {
			return fmt.Errorf("golem: %s: rebuilt body of entity %d: %w", op, m.id, err)
		}
		if !validPhysicsState(state) || state.Handle != h || state.Owner != 0 && state.Owner != m.id {
			return fmt.Errorf("golem: %s: rebuilt body owner or state is invalid for entity %d", op, m.id)
		}
		if state.Mode != m.source.Mode {
			return fmt.Errorf("golem: %s: rebuilt body of entity %d changes its motion mode", op, m.id)
		}
		if err := checkTransferController(dps, m, h); err != nil {
			return fmt.Errorf("golem: %s: rebuilt body of entity %d: %w", op, m.id, err)
		}
		if m.nav && !physicsNavigationMode(state.Mode) {
			return fmt.Errorf("golem: %s: rebuilt body mode of entity %d cannot carry its navigation attachment", op, m.id)
		}
		policy := transferPlacement(placement, m.physics.config.Placement)
		if err := dps.validatePlacement(h, state.Pose, policy); err != nil {
			return fmt.Errorf("golem: %s: rebuilt body of entity %d (penetration tolerance %g m, see SpaceTransfer.Placement): %w", op, m.id, policy.MaxPenetration, err)
		}
		if state.Owner == 0 {
			binder, ok := dps.world.(p.OwnerBinder)
			if !ok {
				return fmt.Errorf("golem: %s: an unowned rebuilt body requires OwnerBinder", op)
			}
			if err := binder.SetOwner(h, m.id); err != nil {
				return fmt.Errorf("golem: %s: own rebuilt body of entity %d: %w", op, m.id, err)
			}
			state.Owner = m.id
		}
		m.body, m.state = h, state
	}
	return nil
}

// transferControllerConfig returns the configuration part of a controller
// policy: everything but Velocity, the retained intent that SetCharacterVelocity
// rewrites at runtime and that transfers do not carry.
func transferControllerConfig(c p.CharacterControllerConfig) p.CharacterControllerConfig {
	c.Velocity = p.Vec3{}
	return c
}

// checkTransferController rejects a rebuilt body whose controller configuration
// differs from the source body's, including one that gains or loses a
// controller. It is skipped when either world cannot report controllers.
func checkTransferController(dps *physicsService, m *spaceTransferEntity, h p.BodyHandle) error {
	reader, ok := dps.world.(p.CharacterControllerReader)
	if !ok || !m.controllerKnown {
		return nil
	}
	ctrl, character, err := reader.CharacterController(h)
	if err != nil {
		return err
	}
	switch {
	case character && !m.controller.Enabled:
		return errors.New("source body has no character controller but the rebuilt body has one")
	case !character && m.controller.Enabled:
		return errors.New("rebuilt body lost the source body's character controller")
	case character && transferControllerConfig(ctrl) != m.controller:
		return errors.New("rebuilt body changes its character controller settings")
	}
	return nil
}

// rollbackTransferBodies destroys the bodies RebuildPhysics returned and
// faults the destination when that fails: bodies the engine could not destroy
// stay in its world, owned by nothing.
func rollbackTransferBodies(op string, dst *spaceRuntime, created map[int64]p.BodyHandle) error {
	err := destroyTransferBodies(dst.phys(), created)
	if err != nil {
		err = fmt.Errorf("golem: %s: destroy rebuilt bodies in space %d: %w", op, dst.id, err)
		dst.faultServices(err)
	}
	return err
}

// destroyTransferBodies removes the bodies RebuildPhysics returned (with their
// colliders and joints), skipping handles some attachment already owns.
func destroyTransferBodies(dps *physicsService, created map[int64]p.BodyHandle) error {
	if dps == nil || len(created) == 0 {
		return nil
	}
	var errs []error
	done := make(map[p.BodyHandle]bool, len(created))
	for _, h := range created {
		if h == (p.BodyHandle{}) || done[h] {
			continue
		}
		done[h] = true
		if _, attached := dps.bodies[h]; attached {
			continue
		}
		if err := dps.world.DestroyBody(h, p.Deleted); err != nil && !errors.Is(err, p.ErrStale) {
			errs = append(errs, err)
		}
	}
	if err := dps.world.Synchronize(); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// setSpacesHoldingEvents moves the registry spaces of t's entities under
// interestMu, so the batch is atomic for session-space readers, and then makes
// every session whose space now differs from its announced one hold its
// server events. It returns SetSpaces' result.
func (s *Server) setSpacesHoldingEvents(t SpaceTransfer) (int64, bool) {
	s.interestMu.Lock()
	bad, moved := s.reg.SetSpaces(t.Entities, t.To)
	var switching []int64
	if moved {
		switching = s.appendMovedSessionSwitchesLocked(nil, t.Entities)
	}
	s.interestMu.Unlock()
	s.holdSessionEvents(switching...)
	return bad, moved
}
