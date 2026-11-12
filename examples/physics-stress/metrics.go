package main

import (
	"encoding/json"
	"math"
	"time"
)

// Fixed buckets keep a two-hour run's retained metrics memory bounded. Reported
// percentiles are upper bounds, never lower bucket edges that hide gate failures.
type histogram struct {
	bins    [10001]uint64
	count   uint64
	maximum time.Duration
}

func (h *histogram) add(d time.Duration) {
	h.count++
	h.maximum = max(h.maximum, d)
	i := max(0, int((d+100*time.Microsecond-1)/(100*time.Microsecond)))
	h.bins[min(i, len(h.bins)-1)]++
}
func (h *histogram) percentile(p float64) time.Duration {
	if h.count == 0 {
		return 0
	}
	target := uint64(math.Ceil(p * float64(h.count)))
	var n uint64
	for i, c := range h.bins {
		n += c
		if n >= target {
			if i == len(h.bins)-1 {
				return h.maximum
			}
			return time.Duration(i) * 100 * time.Microsecond
		}
	}
	return h.maximum
}

// MarshalJSON exports bounded percentile estimates and the exact maximum.
func (h histogram) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]any{"samples": h.count, "bucketUpperBoundNanoseconds": 100000, "p50NS": h.percentile(.5), "p95NS": h.percentile(.95), "p99NS": h.percentile(.99), "maxNS": h.maximum})
}
