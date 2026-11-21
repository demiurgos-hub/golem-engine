package golem

import (
	"fmt"
	"path/filepath"
	"testing"
)

// snapshotBenchEntity serializes like a generated entity: every FullUpdate
// allocates a fresh payload.
type snapshotBenchEntity struct {
	id      int64
	payload [96]byte
}

func (e *snapshotBenchEntity) EntityID() int64              { return e.id }
func (e *snapshotBenchEntity) TypeName() string             { return "SnapshotBench" }
func (e *snapshotBenchEntity) Position() (float32, float32) { return 0, 0 }
func (e *snapshotBenchEntity) IsGlobal() bool               { return false }
func (e *snapshotBenchEntity) FlushUpdate() ([]byte, error) { return nil, nil }
func (e *snapshotBenchEntity) FullUpdate() ([]byte, error) {
	return append([]byte(nil), e.payload[:]...), nil
}

// BenchmarkSaveSnapshotCallerTime measures how long SaveSnapshot blocks its
// caller, which is the tick when a game autosaves, and what it allocates
// there. The background file write is waited for outside the timer.
func BenchmarkSaveSnapshotCallerTime(b *testing.B) {
	for _, n := range []int{1000, 20000} {
		b.Run(fmt.Sprintf("entities=%d", n), func(b *testing.B) {
			s := NewServer(ServerConfig{})
			for i := 0; i < n; i++ {
				if err := s.CreateEntity(&snapshotBenchEntity{id: int64(i + 1)}); err != nil {
					b.Fatal(err)
				}
			}
			path := filepath.Join(b.TempDir(), "snap.bin")
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				done := s.SaveSnapshot("fp", path)
				b.StopTimer()
				if err := <-done; err != nil {
					b.Fatal(err)
				}
				b.StartTimer()
			}
		})
	}
}
