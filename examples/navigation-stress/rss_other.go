//go:build !linux && !windows

package main

func residentBytes() (uint64, bool) { return 0, false }
