package golem

import (
	"math"
	"math/bits"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

type navigationWindowsClock struct {
	anchor      time.Time
	start       int64
	frequency   uint64
	counterAddr uintptr
}

var navigationPerformanceClock = newNavigationWindowsClock()

func newNavigationWindowsClock() navigationWindowsClock {
	kernel := windows.NewLazySystemDLL("kernel32.dll")
	counter := kernel.NewProc("QueryPerformanceCounter")
	frequency := kernel.NewProc("QueryPerformanceFrequency")
	if err := counter.Find(); err != nil {
		panic("golem: high-resolution navigation clock unavailable: " + err.Error())
	}
	if err := frequency.Find(); err != nil {
		panic("golem: navigation clock frequency unavailable: " + err.Error())
	}
	freq := navigationReadCounter(frequency.Addr())
	if freq <= 0 {
		panic("golem: invalid navigation clock frequency")
	}
	clock := navigationWindowsClock{frequency: uint64(freq), counterAddr: counter.Addr()}
	clock.anchor = time.Now()
	clock.start = navigationReadCounter(clock.counterAddr)
	return clock
}

func navigationReadCounter(address uintptr) int64 {
	var counter int64
	// Direct SyscallN preserves the stack-local output. LazyProc.Call marks
	// pointer arguments as escaping and would allocate on every clock read.
	ok, _, _ := syscall.SyscallN(address, uintptr(unsafe.Pointer(&counter)))
	if ok == 0 {
		panic("golem: high-resolution navigation clock read failed")
	}
	return counter
}

func navigationClockNow() time.Time {
	clock := &navigationPerformanceClock
	counter := navigationReadCounter(clock.counterAddr)
	if counter < clock.start {
		panic("golem: navigation clock precedes its origin")
	}
	elapsed := navigationCounterElapsed(uint64(counter-clock.start), clock.frequency)
	return clock.anchor.Add(elapsed)
}

func navigationCounterElapsed(delta, frequency uint64) time.Duration {
	// Keep the multiplication precise even after a long process lifetime.
	hi, lo := bits.Mul64(delta, uint64(time.Second))
	if frequency == 0 || hi >= frequency {
		panic("golem: navigation clock duration overflow")
	}
	nanoseconds, _ := bits.Div64(hi, lo, frequency)
	if nanoseconds > math.MaxInt64 {
		panic("golem: navigation clock duration overflow")
	}
	return time.Duration(nanoseconds)
}
