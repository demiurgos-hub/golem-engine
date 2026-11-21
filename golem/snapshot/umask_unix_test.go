//go:build unix

package snapshot

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// A snapshot can hold owner-only entity state, so a restrictive process umask
// must apply to it, as it did when snapshots were written with os.WriteFile.
func TestWriteAsyncHonoursUmask(t *testing.T) {
	for _, tc := range []struct{ umask, want os.FileMode }{{0o077, 0o600}, {0o027, 0o640}, {0o022, 0o644}} {
		old := syscall.Umask(int(tc.umask))
		path := filepath.Join(t.TempDir(), "world.snap")
		err := <-WriteAsync(path, []byte("state"))
		syscall.Umask(old)
		if err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != tc.want {
			t.Fatalf("umask %04o: snapshot mode = %04o, want %04o", tc.umask, got, tc.want)
		}
	}
}
