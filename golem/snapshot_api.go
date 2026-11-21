package golem

import (
	"fmt"

	"github.com/demiurgos-hub/golem-engine/golem/snapshot"
)

// writeSnapshotAsync is snapshot.WriteAsync; tests replace it to hold the
// background write.
var writeSnapshotAsync = snapshot.WriteAsync

// SaveSnapshot collects the full state of all persistent entities and writes an
// atomic snapshot file to path. Serialization (each entity's space and
// FullUpdate state) runs synchronously on the caller; only the file write runs
// in a background goroutine. Call it on the tick goroutine (a tick callback or
// Post) or while the server is not running so that every record's space and
// state come from the same tick; from other goroutines use SaveSnapshotOnTick.
// The returned channel receives exactly one value: nil on success or a
// non-nil error on failure.
//
// fingerprint should be the generated SchemaFingerprint constant from the
// consumer's generated code (e.g. synced.SchemaFingerprint). It is embedded in
// the file header and validated on Load to detect schema changes between saves.
// Each entity's space is recorded; the file keeps snapshot format version 1
// while every entity is in space 0. Only entity state is saved, even when a
// space has physics: bodies, bindings and navigation are not (paired physics
// checkpoints save those; SaveContentSnapshot and LoadSnapshotStrict reject
// servers with physics).
//
// Before Run, a concurrent Run waits for this capture to finish before its
// first tick.
func (s *Server) SaveSnapshot(fingerprint, path string) <-chan error {
	if s.enterPreRunCapture() {
		defer s.preRunCaptureMu.RUnlock()
	}
	return s.saveSnapshot(fingerprint, path)
}

// enterPreRunCapture takes the pre-Run capture read lock and reports true when
// the server has not started Run, so Run's first tick waits for the caller's
// synchronous capture; the caller releases it with preRunCaptureMu.RUnlock.
// It reports false, holding nothing, once Run has started.
func (s *Server) enterPreRunCapture() bool {
	s.lifeMu.Lock()
	defer s.lifeMu.Unlock()
	if s.life != lifecycleCreated {
		return false
	}
	s.preRunCaptureMu.RLock()
	return true
}

// saveSnapshot is SaveSnapshot without the pre-Run capture gate.
func (s *Server) saveSnapshot(fingerprint, path string) <-chan error {
	data, err := snapshot.CaptureSpaced(s.reg.AppendSpaced(nil), fingerprint)
	if err != nil {
		errc := make(chan error, 1)
		errc <- err
		return errc
	}
	return writeSnapshotAsync(path, data)
}

// snapshotRequest is one pending SaveSnapshotOnTick call.
type snapshotRequest struct {
	fingerprint, path string
	done              chan error
}

// SaveSnapshotOnTick is SaveSnapshot taken on the tick goroutine, so every
// record's space and state come from one tick even when called from another
// goroutine. While the server is running, the request is queued and captured
// at the end of the next tick, after that tick's replication flush and before
// OnTickEnd callbacks; requests queued together are captured in arrival order
// at that same point. Before Run it captures immediately on the caller, like
// SaveSnapshot, and a concurrent Run waits for that capture before its first
// tick. Requests made once shutdown has begun, and requests still
// pending when Run returns, receive ErrServerNotRunning, so the channel never
// stays empty. The returned channel receives exactly one value: nil on
// success or a non-nil error; only the file write runs in the background.
func (s *Server) SaveSnapshotOnTick(fingerprint, path string) <-chan error {
	s.lifeMu.Lock()
	life := s.life
	var done chan error
	switch life {
	case lifecycleRunning:
		done = make(chan error, 1)
		s.snapshotRequests = append(s.snapshotRequests, snapshotRequest{fingerprint, path, done})
	case lifecycleCreated:
		// Hold Run's first tick until the capture below finishes.
		s.preRunCaptureMu.RLock()
	default:
		done = make(chan error, 1)
		done <- ErrServerNotRunning
	}
	s.lifeMu.Unlock()
	if life == lifecycleCreated {
		// Never capture under lifeMu: FullUpdate is entity code.
		defer s.preRunCaptureMu.RUnlock()
		return s.saveSnapshot(fingerprint, path)
	}
	return done
}

// drainSnapshotRequests captures every pending SaveSnapshotOnTick request on
// the tick goroutine, after the tick's replication flush.
func (s *Server) drainSnapshotRequests() {
	s.lifeMu.Lock()
	pending := s.snapshotRequests
	s.snapshotRequests = nil
	s.lifeMu.Unlock()
	for _, req := range pending {
		forwardSnapshotResult(s.saveSnapshot(req.fingerprint, req.path), req.done)
	}
}

// failSnapshotRequestsLocked answers every pending request with
// ErrServerNotRunning. Caller holds lifeMu after the loop has stopped.
func (s *Server) failSnapshotRequestsLocked() {
	for _, req := range s.snapshotRequests {
		req.done <- ErrServerNotRunning
	}
	s.snapshotRequests = nil
}

// forwardSnapshotResult relays the single result of a save to done without
// blocking the tick.
func forwardSnapshotResult(result <-chan error, done chan<- error) {
	go func() { done <- <-result }()
}

// LoadSnapshot reads a snapshot file, restores all entities via the provided
// restore function, registers them with their original IDs in their recorded
// space, and advances the entity ID counter past the highest restored ID.
//
// restore should be the generated synced.RestoreEntity function. Records whose
// type is unknown to restore (it returns a non-nil error) are silently skipped—
// this is the expected behavior when an entity type has been removed from the schema.
//
// Returns snapshot.ErrFingerprintMismatch when the file was saved with a
// different schema binary layout — the caller should delete the snapshot and
// rebuild state from scratch. It does not check for physics: entities are
// recreated with CreateEntityIn and attach no bodies or navigation.
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
		if err := s.CreateEntityIn(rec.Space, entity); err != nil {
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
