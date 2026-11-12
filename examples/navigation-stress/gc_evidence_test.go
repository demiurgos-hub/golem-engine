package main

import (
	"runtime"
	"testing"
)

func TestGCPauseRingReportsOverwriteAndChronologicalCycles(t *testing.T) {
	var stats runtime.MemStats
	stats.NumGC = 300
	for cycle := uint32(44); cycle < 300; cycle++ {
		stats.PauseEnd[cycle%256] = uint64(cycle+1) * 1000
		stats.PauseNs[cycle%256] = uint64(cycle + 1)
	}
	var observations []gcPauseObservation
	omitted := forEachGCPause(&stats, 0, func(p gcPauseObservation) { observations = append(observations, p) })
	if omitted != 44 || len(observations) != 256 {
		t.Fatalf("ring truncation hidden: %d omitted, %d records", omitted, len(observations))
	}
	for i, p := range observations {
		if p.Cycle != uint32(i+45) || p.EndUnixNano != uint64(i+45)*1000 || p.PauseNanosecond != uint64(i+45) {
			t.Fatalf("unordered or mismatched cycle %d: %+v", i, p)
		}
	}
	observations = observations[:0]
	if forEachGCPause(&stats, 299, func(p gcPauseObservation) { observations = append(observations, p) }) != 0 || len(observations) != 1 || observations[0].Cycle != 300 {
		t.Fatal("incremental sampling duplicated old cycles")
	}
	if got := testing.AllocsPerRun(100, func() { forEachGCPause(&stats, 0, func(gcPauseObservation) {}) }); got != 0 {
		t.Fatalf("ring iteration allocated %v times", got)
	}
}
