package snapshot

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
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
// Space is the entity's space; records from version-1 files are in space 0.
type Record struct {
	EntityID  int64
	TypeName  string
	StateData []byte
	Space     registry.SpaceID `json:",omitempty"`
}

// ErrFingerprintMismatch is returned by Load when the schema fingerprint stored
// in the file does not match the expected fingerprint. Loading would produce
// silently incorrect entity state; the caller should rebuild the snapshot.
var ErrFingerprintMismatch = errors.New("snapshot: schema fingerprint mismatch — entity schemas have changed since this snapshot was created")

// magic is the 4-byte file header identifying a Golem snapshot file.
var magic = [4]byte{'G', 'L', 'M', 0x01}

const (
	// fileFormatVersion is written when every record is in space 0.
	fileFormatVersion uint32 = 1
	// spacedFileFormatVersion adds a per-record space after the entity ID.
	spacedFileFormatVersion uint32 = 2
)

// Save collects the persistent entities from the provided slice, serializes
// each via FullUpdate(), and writes an atomic snapshot file to path.
// Serialization runs synchronously on the caller, so every record reflects the
// moment of the call and the entities may reuse their buffers afterwards; only
// the file write runs in a background goroutine (see WriteAsync). The returned
// channel receives exactly one value: nil on success or a non-nil error on
// failure (a serialization error is delivered without starting a write).
// fingerprint should be the generated SchemaFingerprint constant from the
// consumer's generated code (e.g. synced.SchemaFingerprint). Every entity is
// recorded in space 0; use SaveSpaced to keep spaces.
func Save(entities []registry.Entity, fingerprint, path string) <-chan error {
	return captureThenWrite(path, func() ([]byte, error) { return Capture(entities, fingerprint) })
}

// SaveSpaced is Save for entities paired with their space, such as the result
// of Registry.AppendSpaced. Each record's space and state are captured together
// on the caller. The file stays version 1 when every entity is in space 0.
func SaveSpaced(entities []registry.SpacedEntity, fingerprint, path string) <-chan error {
	return captureThenWrite(path, func() ([]byte, error) { return CaptureSpaced(entities, fingerprint) })
}

func captureThenWrite(path string, capture func() ([]byte, error)) <-chan error {
	encoded, err := capture()
	if err != nil {
		errc := make(chan error, 1)
		errc <- err
		return errc
	}
	return WriteAsync(path, encoded)
}

// WriteAsync atomically writes already-captured snapshot bytes (from Capture or
// CaptureSpaced) to path in a background goroutine, through a uniquely named
// temporary file in path's directory (".<name>-<digits>.tmp") that is synced
// and renamed over path. The file is created with mode 0644 less the process
// umask, like os.WriteFile. data is owned by the write and must not be
// modified until the result arrives. Writes to the same path (compared as
// cleaned absolute paths) run one at a time in the order of their WriteAsync
// calls, so the file ends with the bytes of the last call. The returned
// channel receives exactly one value: nil on success or a non-nil error on
// failure.
//
// A temporary file is removed when its write fails. One left behind by a
// process that crashed mid-write stays on disk: the writer never deletes a
// file it did not create, because a name alone does not show that a file is
// disposable.
func WriteAsync(path string, data []byte) <-chan error {
	errc := make(chan error, 1)
	key := writeChainKey(path)
	done := make(chan struct{})
	writeChains.mu.Lock()
	prev := writeChains.last[key]
	writeChains.last[key] = done
	writeChains.mu.Unlock()
	go func() {
		if prev != nil {
			<-prev
		}
		err := write(path, data)
		writeChains.mu.Lock()
		if writeChains.last[key] == done {
			delete(writeChains.last, key)
		}
		writeChains.mu.Unlock()
		close(done)
		errc <- err
	}()
	return errc
}

// writeChains orders writes per destination: last maps each cleaned absolute
// path to the done channel of its most recent write, which the next write to
// that path waits on. An entry is removed once its write finishes and no
// later write has replaced it.
var writeChains = struct {
	mu   sync.Mutex
	last map[string]chan struct{}
}{last: make(map[string]chan struct{})}

func writeChainKey(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		return abs
	}
	return filepath.Clean(path)
}

// createTemp creates a new, uniquely named temporary file for path in path's
// directory. Unlike os.CreateTemp (mode 0600, which a later chmod to 0644
// would widen past the umask), it creates the file with mode 0644 and lets
// the operating system apply the process umask, as os.WriteFile does, so a
// restrictive umask keeps protecting saved state.
func createTemp(path string) (*os.File, error) {
	prefix := filepath.Join(filepath.Dir(path), "."+filepath.Base(path)+"-")
	for attempt := 0; ; attempt++ {
		name := prefix + strconv.FormatUint(uint64(rand.Uint32()), 10) + ".tmp"
		file, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err == nil || !os.IsExist(err) || attempt == 10_000 {
			return file, err
		}
	}
}

func write(path string, encoded []byte) (err error) {
	file, err := createTemp(path)
	if err != nil {
		return fmt.Errorf("snapshot: creating temp file: %w", err)
	}
	tmp := file.Name()
	defer func() {
		if err != nil {
			file.Close()
			_ = os.Remove(tmp)
		}
	}()
	if _, err = file.Write(encoded); err != nil {
		return fmt.Errorf("snapshot: writing temp file: %w", err)
	}
	if err = file.Sync(); err != nil {
		return fmt.Errorf("snapshot: syncing temp file: %w", err)
	}
	if err = file.Close(); err != nil {
		return fmt.Errorf("snapshot: closing temp file: %w", err)
	}
	if err = os.Rename(tmp, path); err != nil {
		return fmt.Errorf("snapshot: renaming snapshot file: %w", err)
	}
	return nil
}

// Capture synchronously captures persistent entity state into owned snapshot
// bytes. Call on the simulation owner for a coherent tick, then write off-thread.
// It preserves the existing version-1 snapshot format and persistence filtering.
// Every entity is recorded in space 0; use CaptureSpaced to keep spaces.
//
// The returned bytes are complete when Capture returns: entities may reuse
// the buffers their FullUpdate returned. A capture of 4,096 entities or more
// calls FullUpdate (and IsPersistent) from several goroutines while the caller
// waits, each entity once, so those methods must be safe to call off the
// caller's goroutine, as the connect-time entity snapshot already requires,
// and must not share unsynchronized scratch state between entities. Records
// keep the order of entities, and the first error in that order is returned.
func Capture(entities []registry.Entity, fingerprint string) ([]byte, error) {
	return capture(len(entities), func(i int) (registry.Entity, registry.SpaceID) { return entities[i], 0 }, fingerprint)
}

// CaptureSpaced is Capture for entities paired with their space. It writes
// version 1 when every persistent entity is in space 0 and version 2, which
// adds a per-record space, otherwise.
func CaptureSpaced(entities []registry.SpacedEntity, fingerprint string) ([]byte, error) {
	return capture(len(entities), func(i int) (registry.Entity, registry.SpaceID) { return entities[i].Entity, entities[i].Space }, fingerprint)
}

// Captures of at least parallelCaptureMin entities serialize on several
// goroutines, at least parallelCaptureGrain entities each. Variables so tests
// can force either path.
var (
	parallelCaptureMin   = 4096
	parallelCaptureGrain = 1024
)

func capture(n int, at func(int) (registry.Entity, registry.SpaceID), fingerprint string) ([]byte, error) {
	records, err := captureRecords(n, at)
	if err != nil {
		return nil, err
	}
	return encode(records, fingerprint, time.Now().UnixNano()), nil
}

// captureRecords serializes the persistent entities into records, in entity
// order.
func captureRecords(n int, at func(int) (registry.Entity, registry.SpaceID)) ([]Record, error) {
	// One slot per entity, filled by index, so serializing in parallel keeps
	// the order. A slot without StateData is skipped: the entity is not
	// persistent or serialized to nothing. FullUpdate is called outside the
	// registry lock; each entity's own mutex ensures a consistent read.
	slots := make([]Record, n)
	fill := func(lo, hi int) (int, error) {
		for i := lo; i < hi; i++ {
			e, space := at(i)
			if p, ok := e.(Persister); ok && !p.IsPersistent() {
				continue
			}
			data, err := e.FullUpdate()
			if err != nil {
				return i, fmt.Errorf("snapshot: serializing entity %d (%s): %w", e.EntityID(), e.TypeName(), err)
			}
			if data == nil {
				continue
			}
			slots[i] = Record{EntityID: e.EntityID(), TypeName: e.TypeName(), StateData: data, Space: space}
		}
		return -1, nil
	}
	workers := 1
	if n >= parallelCaptureMin {
		workers = min(runtime.GOMAXPROCS(0), n/parallelCaptureGrain)
	}
	if workers <= 1 {
		if _, err := fill(0, n); err != nil {
			return nil, err
		}
	} else if err := fillParallel(n, workers, fill); err != nil {
		return nil, err
	}
	records := slots[:0]
	for i := range slots {
		if slots[i].StateData != nil {
			records = append(records, slots[i])
		}
	}
	return records, nil
}

// fillParallel runs fill over [0, n) split into one contiguous range per
// worker and waits for all of them. It returns the error of the lowest index,
// as a serial pass would, and re-raises a panic of entity code on the caller.
func fillParallel(n, workers int, fill func(lo, hi int) (int, error)) error {
	type result struct {
		index    int
		err      error
		panicked any
	}
	results := make([]result, workers)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		lo, hi := n*w/workers, n*(w+1)/workers
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					results[w].panicked = r
				}
			}()
			results[w].index, results[w].err = fill(lo, hi)
		}()
	}
	wg.Wait()
	var first *result
	for w := range results {
		r := &results[w]
		if r.panicked != nil {
			panic(r.panicked)
		}
		if r.err != nil && (first == nil || r.index < first.index) {
			first = r
		}
	}
	if first != nil {
		return first.err
	}
	return nil
}

// encode writes version 1 unless a record is outside space 0.
func encode(records []Record, fingerprint string, timestamp int64) []byte {
	version := fileFormatVersion
	for _, rec := range records {
		if rec.Space != 0 {
			version = spacedFileFormatVersion
			break
		}
	}
	// Usually one allocation: the payload plus an allowance for each record's
	// varints (ID, space and two lengths); append grows it if that falls short.
	size := len(magic) + 3*binary.MaxVarintLen64 + len(fingerprint)
	for i := range records {
		size += 16 + len(records[i].TypeName) + len(records[i].StateData)
	}
	w := (&pb.Writer{}).Grow(size)
	w.Raw(magic[:])
	w.Uint32(version)
	w.Int64(timestamp)
	w.String(fingerprint)
	w.Uint32(uint32(len(records)))
	for _, rec := range records {
		w.Int64(rec.EntityID)
		if version == spacedFileFormatVersion {
			w.Uint32(uint32(rec.Space))
		}
		w.String(rec.TypeName)
		w.Bytes(rec.StateData)
	}
	return w.Finish()
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

// Decode validates already-owned snapshot bytes without opening a file. It
// accepts version 1 (every record in space 0) and version 2 (per-record space).
// Returned records may borrow data; retain the bytes unchanged until
// restoration completes.
func Decode(data []byte, expectedFingerprint string) ([]Record, error) {
	r, spaced, storedFingerprint, err := decodeHeader(data)
	if err != nil {
		return nil, err
	}
	if storedFingerprint != expectedFingerprint {
		return nil, ErrFingerprintMismatch
	}

	count := r.number()
	if r.err != nil {
		return nil, r.err
	}
	// Every record needs at least three varints (four with a space), even with
	// empty payloads.
	minRecord := 3
	if spaced {
		minRecord = 4
	}
	if count > uint64(len(r.data)/minRecord) || count > uint64(^uint32(0)) {
		return nil, fmt.Errorf("snapshot: record count exceeds remaining data")
	}
	records := make([]Record, 0, count)
	for i := uint64(0); i < count; i++ {
		rec := Record{EntityID: int64(r.number())}
		if spaced {
			space := r.number()
			if space > uint64(^uint32(0)) && r.err == nil {
				r.err = fmt.Errorf("snapshot: space exceeds uint32")
			}
			rec.Space = registry.SpaceID(space)
		}
		rec.TypeName = string(r.bytes())
		rec.StateData = r.bytes()
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

// ReadFingerprint returns the schema fingerprint stored in the header of the
// snapshot file at path without decoding its records, for example to report
// both fingerprints after Load returned ErrFingerprintMismatch.
func ReadFingerprint(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("snapshot: reading file: %w", err)
	}
	_, _, fingerprint, err := decodeHeader(data)
	return fingerprint, err
}

// decodeHeader validates the magic bytes and format version and returns a
// reader positioned at the record count, whether records carry a space, and
// the stored fingerprint.
func decodeHeader(data []byte) (*snapshotReader, bool, string, error) {
	if len(data) < 4 {
		return nil, false, "", fmt.Errorf("snapshot: file too short")
	}
	if data[0] != magic[0] || data[1] != magic[1] || data[2] != magic[2] || data[3] != magic[3] {
		return nil, false, "", fmt.Errorf("snapshot: invalid magic bytes — not a Golem snapshot file")
	}

	r := &snapshotReader{data: data[4:]}
	version := r.number()
	if r.err != nil {
		return nil, false, "", r.err
	}
	if version != uint64(fileFormatVersion) && version != uint64(spacedFileFormatVersion) {
		return nil, false, "", fmt.Errorf("snapshot: unsupported format version %d (expected %d or %d)", version, fileFormatVersion, spacedFileFormatVersion)
	}

	_ = r.number() // timestamp (informational; not validated)

	fingerprint := string(r.bytes())
	if r.err != nil {
		return nil, false, "", r.err
	}
	return r, version == uint64(spacedFileFormatVersion), fingerprint, nil
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
