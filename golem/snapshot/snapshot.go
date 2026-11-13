package snapshot

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/demiurgos-hub/golem-engine/golem/pb"
	"github.com/demiurgos-hub/golem-engine/golem/registry"
)

// Persister is an optional interface implemented by generated Synced* types.
// Entities that return false are excluded from snapshots. Entities that do not
// implement Persister are treated as persistent (safe default).
type Persister interface {
	IsPersistent() bool
}

// Record holds the serialized state of a single entity from a snapshot file.
// StateData contains the raw FullUpdate() bytes (a complete EntityUpdate proto
// with a *State payload), ready to be passed to the generated RestoreEntity helper.
type Record struct {
	EntityID  int64
	TypeName  string
	StateData []byte
}

// ErrFingerprintMismatch is returned by Load when the schema fingerprint stored
// in the file does not match the expected fingerprint. Loading would produce
// silently incorrect entity state; the caller should rebuild the snapshot.
var ErrFingerprintMismatch = errors.New("snapshot: schema fingerprint mismatch — entity schemas have changed since this snapshot was created")

// magic is the 4-byte file header identifying a Golem snapshot file.
var magic = [4]byte{'G', 'L', 'M', 0x01}

const fileFormatVersion uint32 = 1

// Save collects the persistent entities from the provided slice, serializes
// each via FullUpdate(), and writes an atomic snapshot file to path.
// The write is performed in a background goroutine; the returned channel
// receives exactly one value: nil on success or a non-nil error on failure.
// fingerprint should be the generated SchemaFingerprint constant from the
// consumer's generated code (e.g. synced.SchemaFingerprint).
func Save(entities []registry.Entity, fingerprint, path string) <-chan error {
	errc := make(chan error, 1)
	go func() {
		errc <- save(entities, fingerprint, path)
	}()
	return errc
}

func save(entities []registry.Entity, fingerprint, path string) error {
	encoded, err := Capture(entities, fingerprint)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, encoded, 0o644); err != nil {
		return fmt.Errorf("snapshot: writing temp file: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("snapshot: renaming snapshot file: %w", err)
	}
	return nil
}

// Capture synchronously captures persistent entity state into owned snapshot
// bytes. Call on the simulation owner for a coherent tick, then write off-thread.
// It preserves the existing version-1 snapshot format and persistence filtering.
func Capture(entities []registry.Entity, fingerprint string) ([]byte, error) {
	// Collect persistent entity records. FullUpdate is called outside the
	// registry lock; each entity's own mutex ensures a consistent read.
	var records []Record
	for _, e := range entities {
		if p, ok := e.(Persister); ok && !p.IsPersistent() {
			continue
		}
		data, err := e.FullUpdate()
		if err != nil {
			return nil, fmt.Errorf("snapshot: serializing entity %d (%s): %w", e.EntityID(), e.TypeName(), err)
		}
		if data == nil {
			continue
		}
		records = append(records, Record{
			EntityID:  e.EntityID(),
			TypeName:  e.TypeName(),
			StateData: data,
		})
	}

	// Encode the file in memory, then write atomically via a temp file.
	w := &pb.Writer{}
	w.Raw(magic[:])
	w.Uint32(fileFormatVersion)
	w.Int64(time.Now().UnixNano())
	w.String(fingerprint)
	w.Uint32(uint32(len(records)))
	for _, rec := range records {
		w.Int64(rec.EntityID)
		w.String(rec.TypeName)
		w.Bytes(rec.StateData)
	}
	return w.Finish(), nil
}

// Load reads a snapshot file, validates its magic bytes and schema fingerprint,
// and returns the decoded entity records. Returns ErrFingerprintMismatch when the
// stored fingerprint differs from expectedFingerprint.
func Load(path, expectedFingerprint string) ([]Record, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("snapshot: reading file: %w", err)
	}
	return Decode(data, expectedFingerprint)
}

// Decode validates already-owned snapshot bytes without opening a file. Returned
// records may borrow data; retain the bytes unchanged until restoration completes.
func Decode(data []byte, expectedFingerprint string) ([]Record, error) {
	if len(data) < 4 {
		return nil, fmt.Errorf("snapshot: file too short")
	}
	if data[0] != magic[0] || data[1] != magic[1] || data[2] != magic[2] || data[3] != magic[3] {
		return nil, fmt.Errorf("snapshot: invalid magic bytes — not a Golem snapshot file")
	}

	r := &snapshotReader{data: data[4:]}
	version := r.number()
	if r.err != nil {
		return nil, r.err
	}
	if version != uint64(fileFormatVersion) {
		return nil, fmt.Errorf("snapshot: unsupported format version %d (expected %d)", version, fileFormatVersion)
	}

	_ = r.number() // timestamp (informational; not validated)

	storedFingerprint := string(r.bytes())
	if r.err != nil {
		return nil, r.err
	}
	if storedFingerprint != expectedFingerprint {
		return nil, ErrFingerprintMismatch
	}

	count := r.number()
	if r.err != nil {
		return nil, r.err
	}
	// Every record needs at least three varints, even with empty payloads.
	if count > uint64(len(r.data)/3) || count > uint64(^uint32(0)) {
		return nil, fmt.Errorf("snapshot: record count exceeds remaining data")
	}
	records := make([]Record, 0, count)
	for i := uint64(0); i < count; i++ {
		rec := Record{
			EntityID:  int64(r.number()),
			TypeName:  string(r.bytes()),
			StateData: r.bytes(),
		}
		if r.err != nil {
			return nil, fmt.Errorf("snapshot: record %d: %w", i, r.err)
		}
		records = append(records, rec)
	}
	if len(r.data) != 0 {
		return nil, fmt.Errorf("snapshot: trailing data")
	}
	return records, nil
}

type snapshotReader struct {
	data []byte
	err  error
}

func (r *snapshotReader) number() uint64 {
	if r.err != nil {
		return 0
	}
	v, n := binary.Uvarint(r.data)
	if n <= 0 {
		r.err = fmt.Errorf("snapshot: truncated or overflowing integer")
		return 0
	}
	r.data = r.data[n:]
	return v
}
func (r *snapshotReader) bytes() []byte {
	n := r.number()
	if r.err != nil {
		return nil
	}
	if n > uint64(len(r.data)) {
		r.err = fmt.Errorf("snapshot: truncated length-delimited value")
		return nil
	}
	value := r.data[:int(n)]
	r.data = r.data[int(n):]
	return value
}
