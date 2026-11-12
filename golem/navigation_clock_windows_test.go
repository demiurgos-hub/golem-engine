package golem

import (
	"math"
	"testing"
	"time"
)

func TestNavigationCounterElapsed(t *testing.T) {
	for _, tc := range []struct {
		delta, frequency uint64
		want             time.Duration
	}{
		{0, 10000000, 0},
		{1, 10000000, 100 * time.Nanosecond},
		{1, 3, 333333333 * time.Nanosecond},
		{math.MaxInt64, uint64(time.Second), time.Duration(math.MaxInt64)},
		{math.MaxUint64, 2 * uint64(time.Second), time.Duration(math.MaxInt64)},
		{math.MaxUint64, math.MaxUint64, time.Second},
	} {
		if got := navigationCounterElapsed(tc.delta, tc.frequency); got != tc.want {
			t.Fatalf("delta=%d frequency=%d got=%s want=%s", tc.delta, tc.frequency, got, tc.want)
		}
	}
	for _, tc := range [][2]uint64{{0, 0}, {math.MaxUint64, 1}, {math.MaxUint64, uint64(time.Second)}} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("invalid or overflowing conversion accepted: %v", tc)
				}
			}()
			navigationCounterElapsed(tc[0], tc[1])
		}()
	}
}
