// Command cputopologyprobe only reads Windows CPU-set and process settings.
package main

import (
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"syscall"
	"time"
	"unsafe"
)

type cpuSet struct {
	ID                                                    uint32
	Group                                                 uint16
	LogicalProcessor, Core, LastLevelCache, NUMANode      uint8
	EfficiencyClass, SchedulingClass, Flags               uint8
	Parked, Allocated, AllocatedToTargetProcess, RealTime bool
	AllocationTag                                         uint64
}

type powerThrottling struct {
	Version, ControlMask, StateMask uint32
}

func processPower(kernel *syscall.LazyDLL, process uintptr) (powerThrottling, error) {
	state := powerThrottling{Version: 1}
	ok, _, err := kernel.NewProc("GetProcessInformation").Call(process, 4, uintptr(unsafe.Pointer(&state)), unsafe.Sizeof(state))
	if ok == 0 {
		return state, err
	}
	return state, nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	kernel := syscall.NewLazyDLL("kernel32.dll")
	process, _, _ := kernel.NewProc("GetCurrentProcess").Call()
	query := kernel.NewProc("GetSystemCpuSetInformation")
	var required uint32
	ok, _, callErr := query.Call(0, 0, uintptr(unsafe.Pointer(&required)), process, 0)
	if ok == 0 && callErr != syscall.ERROR_INSUFFICIENT_BUFFER {
		return fmt.Errorf("CPU set sizing: %w", callErr)
	}
	if required == 0 || required > 1<<20 {
		return fmt.Errorf("unexpected CPU set buffer size %d", required)
	}
	data := make([]byte, required)
	ok, _, callErr = query.Call(uintptr(unsafe.Pointer(&data[0])), uintptr(len(data)), uintptr(unsafe.Pointer(&required)), process, 0)
	if ok == 0 {
		return fmt.Errorf("CPU set enumeration: %w", callErr)
	}
	data = data[:required]
	var sets []cpuSet
	classes := make(map[uint8]int)
	cores := make(map[[2]uint16]bool)
	var highest uint8
	for at := 0; at < len(data); {
		if len(data)-at < 8 {
			return fmt.Errorf("truncated CPU set header at %d", at)
		}
		size := int(binary.LittleEndian.Uint32(data[at:]))
		kind := binary.LittleEndian.Uint32(data[at+4:])
		if size < 8 || size > len(data)-at {
			return fmt.Errorf("invalid CPU set record size %d at %d", size, at)
		}
		if kind == 0 {
			if size < 32 {
				return fmt.Errorf("CPU set record lacks documented fields: %d", size)
			}
			b := data[at : at+size]
			s := cpuSet{ID: binary.LittleEndian.Uint32(b[8:]), Group: binary.LittleEndian.Uint16(b[12:]), LogicalProcessor: b[14], Core: b[15], LastLevelCache: b[16], NUMANode: b[17], EfficiencyClass: b[18], Flags: b[19], SchedulingClass: b[20], AllocationTag: binary.LittleEndian.Uint64(b[24:])}
			s.Parked, s.Allocated, s.AllocatedToTargetProcess, s.RealTime = s.Flags&1 != 0, s.Flags&2 != 0, s.Flags&4 != 0, s.Flags&8 != 0
			sets = append(sets, s)
			classes[s.EfficiencyClass]++
			cores[[2]uint16{s.Group, uint16(s.Core)}] = true
			if s.EfficiencyClass > highest {
				highest = s.EfficiencyClass
			}
		}
		at += size
	}
	var processMask, systemMask uintptr
	ok, _, callErr = kernel.NewProc("GetProcessAffinityMask").Call(process, uintptr(unsafe.Pointer(&processMask)), uintptr(unsafe.Pointer(&systemMask)))
	if ok == 0 {
		return fmt.Errorf("process affinity: %w", callErr)
	}
	defaults := kernel.NewProc("GetProcessDefaultCpuSets")
	var count uint32
	ok, _, callErr = defaults.Call(process, 0, 0, uintptr(unsafe.Pointer(&count)))
	if ok == 0 && callErr != syscall.ERROR_INSUFFICIENT_BUFFER {
		return fmt.Errorf("default CPU set sizing: %w", callErr)
	}
	ids := make([]uint32, count)
	if count > 0 {
		ok, _, callErr = defaults.Call(process, uintptr(unsafe.Pointer(&ids[0])), uintptr(count), uintptr(unsafe.Pointer(&count)))
		if ok == 0 {
			return fmt.Errorf("default CPU sets: %w", callErr)
		}
		ids = ids[:count]
	}
	priority, _, callErr := kernel.NewProc("GetPriorityClass").Call(process)
	if priority == 0 {
		return fmt.Errorf("priority class: %w", callErr)
	}
	power, err := processPower(kernel, process)
	if err != nil {
		return fmt.Errorf("process power throttling: %w", err)
	}
	parent := map[string]any{"pid": os.Getppid()}
	parentHandle, _, parentErr := kernel.NewProc("OpenProcess").Call(0x1000, 0, uintptr(os.Getppid()))
	if parentHandle == 0 {
		parent["error"] = parentErr.Error()
	} else {
		defer kernel.NewProc("CloseHandle").Call(parentHandle)
		parentPower, powerErr := processPower(kernel, parentHandle)
		parent["power_throttling"] = parentPower
		if powerErr != nil {
			parent["power_error"] = powerErr.Error()
		}
		parentPriority, _, priorityErr := kernel.NewProc("GetPriorityClass").Call(parentHandle)
		parent["priority_class"] = parentPriority
		if parentPriority == 0 {
			parent["priority_error"] = priorityErr.Error()
		}
	}
	highestCPUs := make([][2]uint16, 0)
	for _, s := range sets {
		if s.EfficiencyClass == highest {
			highestCPUs = append(highestCPUs, [2]uint16{s.Group, uint16(s.LogicalProcessor)})
		}
	}
	result := map[string]any{
		"observed_utc": time.Now().UTC(), "pid": os.Getpid(), "go_version": runtime.Version(), "go_num_cpu": runtime.NumCPU(), "go_max_procs": runtime.GOMAXPROCS(0),
		"process_affinity_mask_hex": fmt.Sprintf("%X", processMask), "system_affinity_mask_hex": fmt.Sprintf("%X", systemMask), "process_default_cpu_set_ids": ids, "process_priority_class": priority,
		"process_power_throttling": power, "parent_process": parent,
		"power_throttling_interpretation": "Execution-speed bit is 0x1; ignore-timer-resolution bit is 0x4. ControlMask=0 leaves policy to Windows heuristics: it does not prove effective HighQoS or absence of automatic background throttling. Explicit execution-speed control+state=1 requests EcoQoS. This probe only calls GetProcessInformation.",
		"cpu_sets":                        sets, "reported_physical_core_count": len(cores), "efficiency_class_counts": classes, "highest_efficiency_class": highest, "highest_class_group_logical_cpu_pairs": highestCPUs, "heterogeneous_classes_reported": len(classes) > 1,
		"raw_cpu_set_information_hex": hex.EncodeToString(data),
		"interpretation":              "Microsoft documents higher EfficiencyClass as faster and less power-efficient. This is OS topology, not a benchmark or proof of where prior workload ticks executed. Go GOMAXPROCS counts scheduler execution slots, not hardware performance cores. No affinity, CPU-set, priority, power or scheduler settings were changed.",
		"sources":                     []string{"https://learn.microsoft.com/en-us/windows/win32/api/processthreadsapi/nf-processthreadsapi-getsystemcpusetinformation", "https://learn.microsoft.com/en-us/windows/win32/api/winnt/ns-winnt-system_cpu_set_information", "https://learn.microsoft.com/en-us/windows/win32/api/processthreadsapi/nf-processthreadsapi-getprocessinformation", "https://learn.microsoft.com/en-us/windows/win32/api/processthreadsapi/nf-processthreadsapi-setprocessinformation"},
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(result)
}
