package main

import "testing"

func TestAllocationWindowIncludesTailOnceAndExcludesDrain(t *testing.T) {
	var counter allocationCounter
	counter.start(1000, 10)
	var totalBytes, totalObjects uint64
	for _, snapshot := range [][2]uint64{{1400, 14}, {1900, 19}} {
		bytes, objects := counter.sample(snapshot[0], snapshot[1])
		totalBytes, totalObjects = totalBytes+bytes, totalObjects+objects
	}
	tailBytes, tailObjects := counter.finish(2250, 23)
	totalBytes, totalObjects = totalBytes+tailBytes, totalObjects+tailObjects
	if totalBytes != 1250 || totalObjects != 13 || tailBytes != 350 || tailObjects != 4 {
		t.Fatalf("lost or duplicated allocation tail: totals=%d/%d tail=%d/%d", totalBytes, totalObjects, tailBytes, tailObjects)
	}
	if bytes, objects := counter.sample(10000, 100); bytes != 0 || objects != 0 {
		t.Fatal("background drain allocations entered the closed window")
	}
	if bytes, objects := counter.finish(11000, 110); bytes != 0 || objects != 0 {
		t.Fatal("closing the window twice counted post-measurement work")
	}
}
