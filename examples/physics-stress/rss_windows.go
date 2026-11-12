package main

import (
	"fmt"
	"syscall"
	"unsafe"
)

var processMemoryInfo = syscall.NewLazyDLL("psapi.dll").NewProc("GetProcessMemoryInfo")

type memoryCounters struct {
	size, pageFaults                                                                             uint32
	peakWorkingSet, workingSet, peakPaged, paged, peakNonPaged, nonPaged, pageFile, peakPageFile uintptr
}

func residentBytes() (uint64, error) {
	v := memoryCounters{}
	v.size = uint32(unsafe.Sizeof(v))
	handle, err := syscall.GetCurrentProcess()
	if err != nil {
		return 0, err
	}
	ok, _, callErr := processMemoryInfo.Call(uintptr(handle), uintptr(unsafe.Pointer(&v)), uintptr(v.size))
	if ok == 0 {
		return 0, fmt.Errorf("GetProcessMemoryInfo: %w", callErr)
	}
	return uint64(v.workingSet), nil
}
