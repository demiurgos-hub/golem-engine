package snapshot

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestWriteAsyncSamePathEndsWithLastCall issues many writes to one path
// without waiting; the chain must apply them in call order, so the file ends
// with the last call's bytes, every write reports success, no temp file is
// left behind and the chain map is emptied.
func TestWriteAsyncSamePathEndsWithLastCall(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "world.snap")
	const writes = 32
	results := make([]<-chan error, writes)
	var last []byte
	for i := range results {
		// Large payloads widen the window an unordered write would need.
		last = bytes.Repeat([]byte(fmt.Sprintf("write-%02d;", i)), 64<<10)
		results[i] = WriteAsync(path, last)
	}
	for i, errc := range results {
		if err := <-errc; err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, last) {
		t.Fatalf("file holds %q..., want the last write", got[:9])
	}
	assertOnlyFile(t, dir, "world.snap")
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if mode := info.Mode().Perm(); mode != 0o644 {
			t.Fatalf("mode = %v, want 0644", mode)
		}
	}
	writeChains.mu.Lock()
	pending := len(writeChains.last)
	writeChains.mu.Unlock()
	if pending != 0 {
		t.Fatalf("%d write chains left after all writes finished", pending)
	}
}

// TestWriteAsyncSpellingsShareOneChain orders writes to one file named by a
// relative and an absolute path.
func TestWriteAsyncSpellingsShareOneChain(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	abs := filepath.Join(dir, "world.snap")
	first := WriteAsync("world.snap", bytes.Repeat([]byte{1}, 1<<20))
	second := WriteAsync(abs, []byte{2})
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	if err := <-second; err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(abs); err != nil || !bytes.Equal(got, []byte{2}) {
		t.Fatalf("file = %v, %v; want the second write", got, err)
	}
}

// TestWriteAsyncFailureKeepsChainMoving checks that a write queued behind one
// that may fail (its directory is created concurrently) still completes, and
// that a write into a missing directory reports an error.
func TestWriteAsyncFailureKeepsChainMoving(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "missing", "world.snap")
	failed := WriteAsync(path, []byte{1})
	if err := os.Mkdir(filepath.Join(dir, "missing"), 0o755); err != nil {
		t.Fatal(err)
	}
	retry := WriteAsync(path, []byte{2})
	// The first write may run before or after the directory exists; the
	// retry is ordered after it and must succeed either way.
	<-failed
	if err := <-retry; err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, []byte{2}) {
		t.Fatalf("file = %v, %v; want the retry", got, err)
	}
	assertOnlyFile(t, filepath.Join(dir, "missing"), "world.snap")

	gone := filepath.Join(dir, "absent", "world.snap")
	if err := <-WriteAsync(gone, []byte{1}); err == nil {
		t.Fatal("write into a missing directory succeeded")
	}
}

func assertOnlyFile(t *testing.T, dir, name string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != name {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("directory holds %v, want only %s", names, name)
	}
}
