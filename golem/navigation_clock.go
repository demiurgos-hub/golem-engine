package golem

import "time"

// NavigationNow returns a process-local timestamp for navigation budgets and
// duration measurements. Subtract two values from NavigationNow, or pair one
// with NavigationPathAdmissionMetrics.AdmittedAt. Do not mix these values with
// time.Now, time.Since or time.Until when measuring an interval.
//
// On Windows it uses the high-resolution performance counter. Its wall-clock
// representation is anchored once and does not follow later clock corrections;
// use time.Now for civil time, persistent timestamps and timer deadlines. Other
// platforms use Go's ordinary monotonic clock.
func NavigationNow() time.Time { return navigationClockNow() }

func navigationSince(start time.Time) time.Duration { return NavigationNow().Sub(start) }
