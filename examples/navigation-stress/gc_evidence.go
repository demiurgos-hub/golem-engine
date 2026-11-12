package main

import "runtime"

type gcPauseObservation struct {
	Cycle           uint32
	EndUnixNano     uint64
	PauseNanosecond uint64
	Measured        bool
}

// forEachGCPause reads the existing bounded runtime ring without requesting a
// second memory sample. The returned count makes overwritten evidence visible.
func forEachGCPause(stats *runtime.MemStats, previous uint32, emit func(gcPauseObservation)) uint64 {
	count := stats.NumGC - previous
	retained := min(count, uint32(len(stats.PauseNs)))
	for offset := retained; offset > 0; offset-- {
		cycle := stats.NumGC - offset
		index := cycle % uint32(len(stats.PauseNs))
		emit(gcPauseObservation{Cycle: cycle + 1, EndUnixNano: stats.PauseEnd[index], PauseNanosecond: stats.PauseNs[index]})
	}
	return uint64(count - retained)
}
