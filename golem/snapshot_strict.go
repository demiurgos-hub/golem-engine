package golem

import (
	"errors"
	"fmt"
	"sync/atomic"

	"github.com/demiurgos-hub/golem-engine/golem/snapshot"
)

// LoadSnapshotStrict restores an entity-only snapshot before Run. It reads the
// file once, validates every record and restored identity, then publishes the
// entire batch without ordinary spawn/remove hooks. Existing entities must have
// distinct IDs and are preserved. Restore errors, including missing content
// definitions and unknown entity types, are never skipped.
//
// Restore must decode mutable state and return a fresh entity without publishing
// entities or changing live server state. Defaults belong to creation, not this
// path. Server binding and schema colliders are installed after all records pass
// preflight. Hosts rebuild other transient dependencies after success and before
// accepting clients or starting Run. Physics-configured servers must instead use
// paired physics checkpoints. The legacy LoadSnapshot contract is unchanged.
func (s *Server) LoadSnapshotStrict(path, fingerprint string, restore func(snapshot.Record) (Entity, error)) error {
	s.lifeMu.Lock()
	created := s.life == lifecycleCreated
	s.lifeMu.Unlock()
	if !created {
		return errors.New("golem: LoadSnapshotStrict requires startup before Run")
	}
	if s.physics != nil {
		return errors.New("golem: LoadSnapshotStrict is entity-only; use paired physics checkpoint restoration")
	}
	if restore == nil {
		return errors.New("golem: LoadSnapshotStrict requires a restore callback")
	}
	if err := s.entitySetupAllowed(); err != nil {
		return err
	}
	records, err := snapshot.Load(path, fingerprint)
	if err != nil {
		return err
	}
	return s.restoreSnapshotRecordsStrict(records, restore)
}

func (s *Server) restoreSnapshotRecordsStrict(records []snapshot.Record, restore func(snapshot.Record) (Entity, error)) (err error) {
	ids := make([]int64, len(records))
	seen := make(map[int64]bool, len(records))
	var maxID int64
	for i, rec := range records {
		if rec.EntityID <= 0 || rec.TypeName == "" || seen[rec.EntityID] {
			return fmt.Errorf("snapshot: invalid or duplicate record identity %d (%s)", rec.EntityID, rec.TypeName)
		}
		if _, exists := s.Get(rec.EntityID); exists {
			return fmt.Errorf("snapshot: entity %d (%s) already registered", rec.EntityID, rec.TypeName)
		}
		seen[rec.EntityID], ids[i] = true, rec.EntityID
		if rec.EntityID > maxID {
			maxID = rec.EntityID
		}
	}
	entities := make([]Entity, len(records))
	for i, rec := range records {
		e, restoreErr := restore(rec)
		if restoreErr != nil {
			return fmt.Errorf("snapshot: restoring entity %d (%s): %w", rec.EntityID, rec.TypeName, restoreErr)
		}
		if nilSetupEntity(e) {
			return fmt.Errorf("snapshot: restore returned nil for entity %d (%s)", rec.EntityID, rec.TypeName)
		}
		if e.EntityID() != rec.EntityID || e.TypeName() != rec.TypeName {
			return fmt.Errorf("snapshot: restore identity mismatch for entity %d (%s): got %d (%s)", rec.EntityID, rec.TypeName, e.EntityID(), e.TypeName())
		}
		entities[i] = e
	}
	staged := 0
	committed := false
	defer func() {
		if !committed {
			for i := staged - 1; i >= 0; i-- {
				err = errors.Join(err, s.rollbackEntitySetup(entities[i], ids[i]))
			}
		}
	}()
	for i, e := range entities {
		if binder, ok := e.(ServerBinder); ok {
			binder.BindServer(s)
		}
		if e.EntityID() != ids[i] || e.TypeName() != records[i].TypeName {
			return fmt.Errorf("snapshot: restored identity changed during binding for entity %d (%s)", ids[i], records[i].TypeName)
		}
		if err = s.reg.AddProvisional(e); err != nil {
			return err
		}
		staged++
		s.registerCollider(e)
	}
	if err = s.reg.CommitProvisional(ids...); err != nil {
		return err
	}
	committed = true
	for {
		current := atomic.LoadInt64(&s.idCounter)
		if current >= maxID || atomic.CompareAndSwapInt64(&s.idCounter, current, maxID) {
			break
		}
	}
	return nil
}
