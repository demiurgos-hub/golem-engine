package golem

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/demiurgos-hub/golem-engine/golem/snapshot"
)

type contentSnapshotManifest struct {
	Version           int    `json:"version"`
	SchemaFingerprint string `json:"schema_fingerprint"`
	ContentHash       string `json:"content_hash"`
	Snapshot          string `json:"snapshot"`
	SnapshotHash      string `json:"snapshot_hash"`
}

// SaveContentSnapshot captures entity-only state synchronously, then writes an
// immutable snapshot and a content-pinned manifest asynchronously, manifest last.
// Call at startup or on the tick owner. The channel yields exactly one error.
// Save calls sharing a manifest path must be serialized by the host. Old data
// files remain available; this helper never deletes previous checkpoint evidence.
// Physics-configured hosts must use paired physics recovery and pin their content
// identity in their enclosing host manifest instead of taking entity-only saves.
func (s *Server) SaveContentSnapshot(fingerprint, contentHash, manifestPath string) <-chan error {
	done := make(chan error, 1)
	fail := func(err error) <-chan error { done <- err; close(done); return done }
	if !validContentHash(contentHash) || fingerprint == "" || manifestPath == "" {
		return fail(errors.New("golem: content snapshot requires fingerprint, SHA-256 content hash and manifest path"))
	}
	if s.physics != nil {
		return fail(errors.New("golem: content snapshot is entity-only; use paired physics checkpoints"))
	}
	if err := s.entitySetupAllowed(); err != nil {
		return fail(err)
	}
	data, err := snapshot.Capture(s.reg.All(), fingerprint)
	if err != nil {
		return fail(err)
	}
	go func() { done <- writeContentSnapshot(manifestPath, fingerprint, contentHash, data); close(done) }()
	return done
}

func writeContentSnapshot(path, fingerprint, contentHash string, data []byte) error {
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])
	name := "content-" + hash + ".snapshot"
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	dataPath := filepath.Join(dir, name)
	// Data filenames are immutable identities. An existing valid copy is reusable.
	if prior, err := os.ReadFile(dataPath); err == nil {
		if !bytes.Equal(prior, data) {
			return errors.New("golem: content snapshot data identity collision")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	} else {
		temp, err := os.CreateTemp(dir, ".content-data-*")
		if err != nil {
			return err
		}
		tempPath := temp.Name()
		defer os.Remove(tempPath)
		if _, err = temp.Write(data); err != nil {
			temp.Close()
			return err
		}
		if err = temp.Sync(); err != nil {
			temp.Close()
			return err
		}
		if err = temp.Close(); err != nil {
			return err
		}
		if err = os.Rename(tempPath, dataPath); err != nil {
			return err
		}
	}
	manifest := contentSnapshotManifest{Version: 1, SchemaFingerprint: fingerprint, ContentHash: contentHash, Snapshot: name, SnapshotHash: hash}
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(dir, ".content-manifest-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if _, err = temp.Write(encoded); err != nil {
		temp.Close()
		return err
	}
	if err = temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err = temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}

// LoadContentSnapshot verifies the exact content pin and snapshot digest before
// decoding once and strictly restoring all entities without ordinary spawn hooks.
// The restore callback must validate each saved definition ID and factory binding;
// a matching bundle hash alone cannot establish that those references are valid.
// It is startup-only and entity-only, like LoadSnapshotStrict.
func (s *Server) LoadContentSnapshot(manifestPath, fingerprint, contentHash string, restore func(snapshot.Record) (Entity, error)) error {
	s.lifeMu.Lock()
	created := s.life == lifecycleCreated
	s.lifeMu.Unlock()
	if !created || s.physics != nil {
		return errors.New("golem: LoadContentSnapshot requires entity-only startup before Run")
	}
	if restore == nil || !validContentHash(contentHash) {
		return errors.New("golem: content restore requires callback and SHA-256 content hash")
	}
	if err := s.entitySetupAllowed(); err != nil {
		return err
	}
	file, err := os.Open(manifestPath)
	if err != nil {
		return err
	}
	encoded, err := io.ReadAll(io.LimitReader(file, (16<<10)+1))
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if len(encoded) > 16<<10 {
		return errors.New("golem: content snapshot manifest too large")
	}
	var manifest contentSnapshotManifest
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&manifest); err != nil {
		return err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return errors.New("golem: trailing content manifest data")
	}
	if manifest.Version != 1 || manifest.ContentHash != contentHash || manifest.SchemaFingerprint != fingerprint {
		return errors.New("golem: content snapshot version/schema/content mismatch")
	}
	if !validContentHash(manifest.SnapshotHash) || manifest.Snapshot != "content-"+manifest.SnapshotHash+".snapshot" {
		return errors.New("golem: invalid content snapshot data identity")
	}
	data, err := os.ReadFile(filepath.Join(filepath.Dir(manifestPath), manifest.Snapshot))
	if err != nil {
		return err
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != manifest.SnapshotHash {
		return errors.New("golem: content snapshot data hash mismatch")
	}
	records, err := snapshot.Decode(data, fingerprint)
	if err != nil {
		return err
	}
	if err = s.restoreSnapshotRecordsStrict(records, restore); err != nil {
		return fmt.Errorf("content snapshot: %w", err)
	}
	return nil
}

func validContentHash(hash string) bool {
	if len(hash) != 64 || hash != strings.ToLower(hash) {
		return false
	}
	_, err := hex.DecodeString(hash)
	return err == nil
}
