package golem

import (
	"errors"
	"fmt"
	"reflect"

	"github.com/demiurgos-hub/golem-engine/golem/registry"
)

// EntitySetup prepares a provisional entity before simulation or replication.
// It may return rollback even with an error. Rollback releases caller-owned
// resources only; the server releases successful physics/navigation attachments
// and schema colliders. Rollback is discarded on successful publication.
type EntitySetup func(Entity) (rollback func() error, err error)

// CreateEntityWithSetup assigns an ID, binds the server, registers schema
// colliders and calls setup while Get/Owner can resolve the provisional entity.
// All, Len, ticking, snapshots and replication exclude it until setup succeeds;
// OnSpawn runs only after publication. A nil setup simply publishes the entity.
// Failure rolls back attachments and registration without OnSpawn/OnRemove or
// lifecycle output. Cleanup errors are joined with the setup error; a setup
// panic rolls back server resources and then continues unwinding unchanged.
//
// Call before Run or on the tick goroutine, outside physics callbacks, active
// frames and restoration. Setup must not advance simulation, change the entity
// identity, publish it itself, or mutate unrelated entities. Unattached resources
// acquired before a panic remain the callback's responsibility. Assigned IDs are
// consumed even on failure. Existing CreateEntity behavior is unchanged.
func (s *Server) CreateEntityWithSetup(e Entity, setup EntitySetup, owner ...int64) (err error) {
	if len(owner) > 1 {
		return fmt.Errorf("golem: CreateEntityWithSetup expects 0 or 1 owner session ID, got %d", len(owner))
	}
	if nilSetupEntity(e) {
		return errors.New("golem: CreateEntityWithSetup requires a non-nil entity")
	}
	if err := s.entitySetupAllowed(); err != nil {
		return err
	}
	if e.EntityID() == 0 {
		setter, ok := e.(registry.EntityIDSetter)
		if !ok {
			return errors.New("golem: entity has no ID and does not implement EntityIDSetter")
		}
		setter.SetEntityID(s.nextID())
	}
	if binder, ok := e.(ServerBinder); ok {
		binder.BindServer(s)
	}
	if err := s.reg.AddProvisional(e, owner...); err != nil {
		return err
	}
	id := e.EntityID()
	committed := false
	var rollback func() error
	defer func() {
		if !committed {
			err = errors.Join(err, s.rollbackEntitySetup(e, id))
			if rollback != nil {
				err = errors.Join(err, rollback())
			}
		}
	}()
	s.registerCollider(e)
	if setup != nil {
		rollback, err = setup(e)
		if err != nil {
			return err
		}
	}
	if err = s.reg.CommitProvisional(id); err != nil {
		return err
	}
	committed = true
	registry.NotifySpawn(e)
	return nil
}

func (s *Server) entitySetupAllowed() error {
	s.lifeMu.Lock()
	stopping := s.life == lifecycleStopping || s.life == lifecycleStopped
	s.lifeMu.Unlock()
	if stopping {
		return ErrServerNotRunning
	}
	if s.physics != nil {
		if err := s.physics.mutable(); err != nil {
			return fmt.Errorf("golem: entity setup: %w", err)
		}
	}
	if n := s.navigation; n != nil && (n.restoring || n.restoreErr != nil || n.frame != nil) {
		return errors.New("golem: entity setup is unavailable during navigation frames or restoration")
	}
	return nil
}

func (s *Server) rollbackEntitySetup(e Entity, id int64) error {
	err := s.DetachPhysics(id)
	if err != nil && s.physics != nil {
		s.physics.fault = err
	}
	s.DetachNavigation(id)
	if _, ok := e.(ColliderProvider); ok && s.layers != nil {
		s.layers.Remove(id)
	}
	if _, ok := e.(ColliderProvider3D); ok && s.layers3D != nil {
		s.layers3D.Remove(id)
	}
	s.reg.DiscardProvisional(id)
	return err
}

func nilSetupEntity(e Entity) bool {
	if e == nil {
		return true
	}
	v := reflect.ValueOf(e)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	default:
		return false
	}
}
