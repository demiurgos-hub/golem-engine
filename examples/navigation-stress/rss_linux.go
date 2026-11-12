package main

import (
	"fmt"
	"os"
)

func residentBytes() (uint64, bool) {
	b, e := os.ReadFile("/proc/self/statm")
	if e != nil {
		return 0, false
	}
	var total, resident uint64
	if _, e = fmt.Sscan(string(b), &total, &resident); e != nil {
		return 0, false
	}
	return resident * uint64(os.Getpagesize()), true
}
