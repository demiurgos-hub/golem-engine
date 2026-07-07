package golem

import (
	"fmt"

	"github.com/demiurgos-hub/golem-engine/golem/snapshot"
)

// SaveSnapshot collects the full state of all persistent entities and writes an
// atomic snapshot file to path. The write runs in a background goroutine so the
// game loop is never blocked. The returned channel receives exactly one value:
// nil on success or a non-nil error on failure.
//
// fingerprint should be the generated SchemaFingerprint constant from the
// consumer's generated code (e.g. synced.SchemaFingerprint). It is embedded in
// the file header and validated on Load to detect schema changes between saves.
func (s *Server) SaveSnapshot(fingerprint, path string) <-chan error {
	return snapshot.Save(s.reg.All(), fingerprint, path)
}

// LoadSnapshot reads a snapshot file, restores all entities via the provided
// restore function, registers them with their original IDs, and advances the
// entity ID counter past the highest restored ID.
//
// restore should be the generated synced.RestoreEntity function. Records whose
// type is unknown to restore (it returns a non-nil error) are silently skipped—
// this is the expected behavior when an entity type has been removed from the schema.
//
// Returns snapshot.ErrFingerprintMismatch when the file was saved with a
// different schema binary layout — the caller should delete the snapshot and
// rebuild state from scratch.
func (s *Server) LoadSnapshot(path, fingerprint string, restore func(snapshot.Record) (Entity, error)) error {
	records, err := snapshot.Load(path, fingerprint)
	if err != nil {
		return err
	}

	var maxID int64
	for _, rec := range records {
		entity, err := restore(rec)
		if err != nil {
			// Unknown type — entity was removed from the schema; skip it.
			continue
		}
		if err := s.CreateEntity(entity); err != nil {
			return fmt.Errorf("snapshot: restoring entity %d (%s): %w", rec.EntityID, rec.TypeName, err)
		}
		if rec.EntityID > maxID {
			maxID = rec.EntityID
		}
	}

	if maxID > 0 {
		s.SetEntityIDCounter(maxID)
	}
	return nil
}
