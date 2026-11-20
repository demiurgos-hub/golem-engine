package golem

import (
	"errors"
	"fmt"

	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
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
}

// applySpaceMove validates every entity of t, including its legacy collider
// transfer and the services it needs in t.To, rebuilds physics bodies in the
// destination world, and then moves everything together: source attachments
// detach, the registry and legacy collider moves happen in the same
// tick-owned step (interestMu makes the batch atomic for session-space
// readers), and attachments are recreated in the destination.
func (s *Server) applySpaceMove(op string, t SpaceTransfer) error {
	dst := s.runtime(t.To)
	var moves []spaceTransferEntity
	physicsMoves := 0
	for _, id := range t.Entities {
		from, published := s.reg.PublishedSpace(id)
		if !published {
			return fmt.Errorf("golem: %s: entity %d is not published", op, id)
		}
		src := s.runtime(from)
		if n := src.nav(); n != nil && n.deleting[id] {
			return fmt.Errorf("golem: %s: entity %d is being deleted", op, id)
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
		s.interestMu.Lock()
		bad, moved := s.reg.SetSpaces(t.Entities, t.To)
		s.interestMu.Unlock()
		if !moved {
			return fmt.Errorf("golem: %s: entity %d is not published", op, bad)
		}
		applyColliderMoves(colliders)
		return nil
	}

	s.transferring = true
	defer func() { s.transferring = false }()
	var created map[int64]p.BodyHandle
	if physicsMoves != 0 {
		created, err = s.rebuildTransferPhysics(op, t, dst, moves)
		if err != nil {
			return err
		}
	}

	// Traversal cancellation callbacks of the detached source agents run only
	// after the whole batch has committed.
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
		s.transferring = false
		for _, callback := range callbacks {
			callback()
		}
	}

	s.interestMu.Lock()
	bad, moved := s.reg.SetSpaces(t.Entities, t.To)
	s.interestMu.Unlock()
	if !moved {
		runDeferred()
		return errors.Join(fmt.Errorf("golem: %s: entity %d is not published", op, bad), destroyTransferBodies(dst.phys(), created))
	}
	applyColliderMoves(colliders)

	var faults []error
	for _, m := range moves {
		if m.physics != nil {
			// Destroys the source body with its colliders and joints, cancels its
			// timed trajectory and detaches its source navigation.
			if err := m.from.physics.detach(m.id, p.Teleported); err != nil {
				m.from.physics.fault = err
				faults = append(faults, fmt.Errorf("golem: %s: detach entity %d from space %d physics: %w", op, m.id, m.from.id, err))
			}
		}
		if m.nav {
			m.from.navigation.detach(m.id)
		}
	}
	for _, m := range moves {
		if m.physics != nil {
			old := m.physics
			a := &physicsAttachment{id: m.id, body: m.body, entity: old.entity, position: old.position, writer: old.writer,
				rotation: old.rotation, velocity: old.velocity, activation: old.activation, config: old.config,
				localAnchor: m.state.Pose.Inverse().Compose(m.state.Anchor)}
			dst.physics.agents[m.id], dst.physics.bodies[m.body] = a, m.id
			dst.physics.write(a, m.state, true)
		}
		if m.nav {
			dst.navigation.addAgent(m.id, m.navConfig)
		}
	}
	runDeferred()
	return errors.Join(faults...)
}

// rebuildTransferPhysics captures the source states of the moving physics
// entities, calls RebuildPhysics once on the destination world and admits each
// returned body. On any failure every returned body is destroyed and nothing
// else has changed. Admitted bodies are recorded in moves.
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
		sources = append(sources, PhysicsTransferSource{Entity: m.id, From: m.from.id, Body: state, Config: m.physics.config})
	}
	created, err := t.RebuildPhysics(dps.world, sources)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("golem: %s: rebuild physics: %w", op, err), destroyTransferBodies(dps, created))
	}
	if err := s.admitTransferBodies(op, dps, moves, created); err != nil {
		return nil, errors.Join(err, destroyTransferBodies(dps, created))
	}
	return created, nil
}

func (s *Server) admitTransferBodies(op string, dps *physicsService, moves []spaceTransferEntity, created map[int64]p.BodyHandle) error {
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
		if m.nav && !physicsNavigationMode(state.Mode) {
			return fmt.Errorf("golem: %s: rebuilt body mode of entity %d cannot carry its navigation attachment", op, m.id)
		}
		if err := dps.validatePlacement(h, state.Pose, m.physics.config.Placement); err != nil {
			return fmt.Errorf("golem: %s: rebuilt body of entity %d: %w", op, m.id, err)
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
