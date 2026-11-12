package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

func residentBytes() (uint64, error) {
	data, err := os.ReadFile("/proc/self/statm")
	if err != nil {
		return 0, err
	}
	fields := strings.Fields(string(data))
	if len(fields) < 2 {
		return 0, fmt.Errorf("invalid statm")
	}
	pages, err := strconv.ParseUint(fields[1], 10, 64)
	return pages * uint64(os.Getpagesize()), err
}
