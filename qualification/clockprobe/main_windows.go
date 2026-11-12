package main

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"sort"
	"syscall"
	"time"
	"unsafe"
)

var kernel = syscall.NewLazyDLL("kernel32.dll")
var qpc = kernel.NewProc("QueryPerformanceCounter")
var qpf = kernel.NewProc("QueryPerformanceFrequency")
var frequency int64

func counter() int64 {
	var value int64
	if result, _, err := qpc.Call(uintptr(unsafe.Pointer(&value))); result == 0 {
		panic(err)
	}
	return value
}

func ns(ticks int64) int64 { return ticks * 1_000_000_000 / frequency }

func summarize(values []int64) map[string]any {
	zero, negative, minPositive := 0, 0, int64(0)
	for _, value := range values {
		if value == 0 {
			zero++
		}
		if value < 0 {
			negative++
		}
		if value > 0 && (minPositive == 0 || value < minPositive) {
			minPositive = value
		}
	}
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	return map[string]any{"count": len(values), "zero": zero, "negative": negative, "min_positive_ns": minPositive, "p50_ns": values[len(values)/2], "p95_ns": values[len(values)*95/100], "p99_ns": values[len(values)*99/100], "max_ns": values[len(values)-1]}
}

func paired(count int, action func()) map[string]any {
	standard, precise := make([]int64, 0, count), make([]int64, 0, count)
	for i := 0; i < count; i++ {
		beginQPC := counter()
		beginGo := time.Now()
		action()
		standard = append(standard, int64(time.Since(beginGo)))
		precise = append(precise, ns(counter()-beginQPC))
	}
	return map[string]any{"time_since": summarize(standard), "qpc_envelope": summarize(precise)}
}

func main() {
	if err := qpc.Find(); err != nil {
		panic(err)
	}
	if result, _, err := qpf.Call(uintptr(unsafe.Pointer(&frequency))); result == 0 {
		panic(err)
	}
	started := time.Now()
	values := make([]int64, 100000)
	lastGo := time.Now()
	for i := range values {
		current := time.Now()
		values[i] = int64(current.Sub(lastGo))
		lastGo = current
	}
	standardReads := summarize(values)
	lastQPC := counter()
	for i := range values {
		current := counter()
		values[i] = ns(current - lastQPC)
		lastQPC = current
	}
	qpcReads := summarize(values)
	busy := paired(1000, func() {
		begin := counter()
		for ns(counter()-begin) < 500000 {
		}
	})
	yield := paired(10000, runtime.Gosched)
	sleep := paired(100, func() { time.Sleep(100 * time.Microsecond) })
	baseTime, baseQPC := time.Now(), counter()
	anchoredNow := func() time.Time { return baseTime.Add(time.Duration(ns(counter() - baseQPC))) }
	mixedSince, sameDomain := make([]int64, 10000), make([]int64, 10000)
	for i := range mixedSince {
		begin := anchoredNow()
		runtime.Gosched()
		sameDomain[i] = int64(anchoredNow().Sub(begin))
		mixedSince[i] = int64(time.Since(begin))
	}
	result := map[string]any{"diagnostic_only": true, "version": runtime.Version(), "goos": runtime.GOOS, "goarch": runtime.GOARCH, "gomaxprocs": runtime.GOMAXPROCS(0), "frequency_hz": frequency, "started_utc": started.UTC(), "finished_utc": time.Now().UTC(), "time_now_adjacent_reads": standardReads, "qpc_adjacent_reads": qpcReads, "busy_500us_pairs": busy, "gosched_pairs": yield, "sleep_100us_pairs": sleep, "qpc_anchored_time_sub": summarize(sameDomain), "invalid_mixed_time_since_qpc_anchor": summarize(mixedSince), "limits": "QPC envelope includes both Go clock reads; syscall/counter read overhead is visible in adjacent reads. Diagnostic of clock granularity, not navigation capacity. ticks-to-ns multiplication is safe only for these short intervals."}
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
