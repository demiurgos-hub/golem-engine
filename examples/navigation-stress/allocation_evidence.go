package main

import "time"

type allocationWindowEvidence struct {
	NominalStart, NominalEnd        time.Time
	StartReadBefore, StartReadAfter time.Time
	EndReadBefore, EndReadAfter     time.Time
	TailBytes, TailObjects          uint64
	EndReadDelay                    time.Duration
}

type allocationCounter struct {
	bytes, objects uint64
	active         bool
}

func (c *allocationCounter) start(bytes, objects uint64) {
	c.bytes, c.objects, c.active = bytes, objects, true
}

func (c *allocationCounter) sample(bytes, objects uint64) (uint64, uint64) {
	if !c.active {
		return 0, 0
	}
	deltaBytes, deltaObjects := bytes-c.bytes, objects-c.objects
	c.bytes, c.objects = bytes, objects
	return deltaBytes, deltaObjects
}

func (c *allocationCounter) finish(bytes, objects uint64) (uint64, uint64) {
	deltaBytes, deltaObjects := c.sample(bytes, objects)
	c.active = false
	return deltaBytes, deltaObjects
}
