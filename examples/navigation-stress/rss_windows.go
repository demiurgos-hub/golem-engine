package main

import (
	"syscall"
	"unsafe"
)

// OS process accounting only; navigation/baking still has no native backend.
var memoryInfo = syscall.NewLazyDLL("psapi.dll").NewProc("GetProcessMemoryInfo")

type processMemory struct {
	Size, PageFaults                                                                       uint32
	PeakWorking, Working, PeakPaged, Paged, PeakNonPaged, NonPaged, Pagefile, PeakPagefile uintptr
}

func residentBytes() (uint64, bool) {
	m := processMemory{}
	m.Size = uint32(unsafe.Sizeof(m))
	ok, _, _ := memoryInfo.Call(^uintptr(0), uintptr(unsafe.Pointer(&m)), uintptr(m.Size))
	return uint64(m.Working), ok != 0
}
