//go:build !windows

package golem

import "time"

func navigationClockNow() time.Time { return time.Now() }
