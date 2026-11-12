package main

import (
	"fmt"
	"time"

	"github.com/demiurgos-hub/golem-engine/golem"
	"github.com/demiurgos-hub/navmesh-go/dynamic"
)

type liveProfileEvidence struct {
	Retention     dynamic.RetentionStats
	BuildPending  bool
	HasQuarantine bool
}

type liveMemoryEvidence struct {
	CapturedAt                                         time.Time
	Tick                                               uint64
	ExpectedAgents, RegisteredEntities, AttachedAgents int
	AgentsWithDestinations, QuarantinedAgents          int
	UnquarantinedPendingAgents, RecoveringAgents       int
	Modes                                              [8]int
	Queues                                             golem.NavigationStats
	Profiles                                           []liveProfileEvidence
	SourceEditsSubmitted, SourceEditsCompleted         uint64
	PhysicalPostsPending                               int64
	HeapBytes, RSSBytes                                uint64
	RSSAvailable, CapturedWhileRunning                 bool
}

func (e liveMemoryEvidence) allAgentsLive() bool {
	return e.CapturedWhileRunning && e.ExpectedAgents > 0 && e.RegisteredEntities == e.ExpectedAgents && e.AttachedAgents == e.ExpectedAgents
}

func (e liveMemoryEvidence) settled() bool {
	if !e.allAgentsLive() || e.SourceEditsSubmitted != e.SourceEditsCompleted || e.PhysicalPostsPending != 0 || e.UnquarantinedPendingAgents != 0 || e.RecoveringAgents != 0 {
		return false
	}
	for _, q := range []golem.NavigationQueueStats{e.Queues.Paths, e.Queues.Builds} {
		if q.Delayed != 0 || q.Queued != 0 || q.Running != 0 || q.Completions != 0 {
			return false
		}
	}
	if len(e.Profiles) == 0 {
		return false
	}
	for _, p := range e.Profiles {
		r := p.Retention
		if p.BuildPending || r.Building || r.Prepared || r.RetiredGenerations != 0 || r.DesiredRevision != r.CommittedRevision {
			return false
		}
	}
	return true
}

type drainEvidence struct {
	Minimum, Timeout, Elapsed time.Duration
	ConsecutiveSettledTicks   int
	Settled, TimedOut         bool
	Live                      liveMemoryEvidence
}

// Two adjacent idle tick ends avoid accepting a transient queue transition.
func (e *drainEvidence) observe(elapsed time.Duration, live liveMemoryEvidence) bool {
	e.Elapsed = elapsed
	if live.settled() {
		e.ConsecutiveSettledTicks++
	} else {
		e.ConsecutiveSettledTicks = 0
	}
	e.Settled = e.ConsecutiveSettledTicks >= 2
	if elapsed < e.Minimum {
		return false
	}
	e.TimedOut = !e.Settled && elapsed >= e.Timeout
	return e.Settled || e.TimedOut
}

func captureLiveState(s *golem.Server, actors []*actor, worlds []*dynamic.World, submitted, completed uint64, pendingPosts int64) liveMemoryEvidence {
	e := liveMemoryEvidence{CapturedAt: golem.NavigationNow(), Tick: s.Tick(), ExpectedAgents: len(actors), RegisteredEntities: s.Len(), Queues: s.NavigationStats(), SourceEditsSubmitted: submitted, SourceEditsCompleted: completed, PhysicalPostsPending: pendingPosts, CapturedWhileRunning: true, Profiles: make([]liveProfileEvidence, len(worlds))}
	for _, a := range actors {
		state, ok := s.NavigationAgentState(a.ID)
		if !ok {
			continue
		}
		e.AttachedAgents++
		if int(state.Mode) < len(e.Modes) {
			e.Modes[state.Mode]++
		}
		if state.HasDestination {
			e.AgentsWithDestinations++
		}
		if state.Quarantined {
			e.QuarantinedAgents++
		} else if state.Mode == golem.NavigationPending {
			e.UnquarantinedPendingAgents++
		}
		if a.recoveryPending || a.recovering {
			e.RecoveringAgents++
		}
	}
	for i, w := range worlds {
		build, _ := s.NavigationBuildStatus(fmt.Sprintf("profile-%d", i))
		e.Profiles[i] = liveProfileEvidence{Retention: w.RetentionStats(), BuildPending: build.Pending, HasQuarantine: w.Safety().HasQuarantine()}
	}
	return e
}
