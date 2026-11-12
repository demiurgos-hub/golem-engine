package golem

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// navigationPool owns one independently bounded class of navigation work.
// Jobs and completion values cross goroutine boundaries; completions alone
// touch tick-owned state. A replaced job's result is discarded by its owner.
type navigationPool struct {
	mu                                           sync.Mutex
	ctx                                          context.Context
	cancel                                       context.CancelFunc
	wg                                           sync.WaitGroup
	pending                                      map[string]*navigationJob
	running                                      map[uint64]*navigationJob
	wake                                         chan struct{}
	completed                                    chan navigationCompletion
	capacity                                     int
	workers                                      int
	next, turns                                  uint64
	stopped                                      bool
	accepted, rejected, finished                 uint64
	queueLatency, workLatency, completionLatency [8]uint64
	queueWaitMax, workMax, completionLagMax      time.Duration
}

type navigationJob struct {
	key       string
	id        uint64
	priority  int
	turn      uint64
	queuedAt  time.Time
	startedAt time.Time
	ctx       context.Context
	cancel    context.CancelFunc
	work      func(context.Context) (any, error)
	complete  func(any, error)
	discard   func(any)
}

type navigationCompletion struct {
	job        *navigationJob
	value      any
	err        error
	finishedAt time.Time
}

func newNavigationPool(workers, capacity, completions int) *navigationPool {
	return &navigationPool{
		workers: workers, capacity: capacity,
		pending: make(map[string]*navigationJob), running: make(map[uint64]*navigationJob),
		wake: make(chan struct{}, 1), completed: make(chan navigationCompletion, completions),
	}
}

func (p *navigationPool) start(ctx context.Context) {
	p.mu.Lock()
	p.ctx, p.cancel = context.WithCancel(ctx)
	p.wg.Add(p.workers)
	p.mu.Unlock()
	for i := 0; i < p.workers; i++ {
		go p.run()
	}
}

func (p *navigationPool) submit(key string, priority int, work func(context.Context) (any, error), complete func(any, error), discard func(any)) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.ctx == nil || p.stopped || p.ctx.Err() != nil {
		return ErrServerNotRunning
	}
	old := p.pending[key]
	if old == nil && len(p.pending) >= p.capacity {
		p.rejected++
		return ErrNavigationQueueFull
	}
	if old != nil {
		old.cancel()
	}
	for _, running := range p.running {
		if running.key == key {
			running.cancel()
		}
	}
	if priority < -8 {
		priority = -8
	}
	if priority > 8 {
		priority = 8
	}
	p.next++
	ctx, cancel := context.WithCancel(p.ctx)
	p.pending[key] = &navigationJob{key: key, id: p.next, priority: priority, turn: p.turns,
		ctx: ctx, cancel: cancel, work: work, complete: complete, discard: discard, queuedAt: NavigationNow()}
	p.accepted++
	p.signal()
	return nil
}

func (p *navigationPool) signal() {
	select {
	case p.wake <- struct{}{}:
	default:
	}
}

func (p *navigationPool) cancelKey(key string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if job := p.pending[key]; job != nil {
		job.cancel()
		delete(p.pending, key)
	}
	for _, job := range p.running {
		if job.key == key {
			job.cancel()
		}
	}
}

func (p *navigationPool) take() *navigationJob {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stopped || p.ctx.Err() != nil {
		return nil
	}
	var selected *navigationJob
	var score int64
	for _, job := range p.pending {
		candidate := int64(job.priority)*8 + int64(p.turns-job.turn)
		if selected == nil || candidate > score || candidate == score && job.id < selected.id {
			selected, score = job, candidate
		}
	}
	if selected != nil {
		delete(p.pending, selected.key)
		p.running[selected.id] = selected
		selected.startedAt = NavigationNow()
		recordNavigationLatency(&p.queueLatency, &p.queueWaitMax, selected.startedAt.Sub(selected.queuedAt))
		p.turns++
		if len(p.pending) > 0 {
			p.signal()
		}
	}
	return selected
}

func (p *navigationPool) run() {
	defer p.wg.Done()
	for {
		job := p.take()
		if job == nil {
			select {
			case <-p.ctx.Done():
				return
			case <-p.wake:
				continue
			}
		}
		value, err := runNavigationWork(job)
		finishedAt := NavigationNow()
		p.mu.Lock()
		delete(p.running, job.id)
		p.finished++
		recordNavigationLatency(&p.workLatency, &p.workMax, finishedAt.Sub(job.startedAt))
		p.mu.Unlock()
		if job.ctx.Err() != nil {
			if job.discard != nil {
				job.discard(value)
			}
			job.cancel()
			continue
		}
		select {
		case p.completed <- navigationCompletion{job: job, value: value, err: err, finishedAt: finishedAt}:
		case <-p.ctx.Done():
			if job.discard != nil {
				job.discard(value)
			}
		}
		job.cancel()
	}
}

func runNavigationWork(job *navigationJob) (value any, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("golem: navigation worker panicked: %v", r)
		}
	}()
	return job.work(job.ctx)
}

func (p *navigationPool) drain(limit int, budget time.Duration) {
	start := NavigationNow()
	n := len(p.completed)
	if n > limit {
		n = limit
	}
	for i := 0; i < n; i++ {
		if p.ctx == nil || p.ctx.Err() != nil {
			return
		}
		if i > 0 && budget > 0 && navigationSince(start) >= budget {
			return
		}
		select {
		case completion := <-p.completed:
			p.mu.Lock()
			recordNavigationLatency(&p.completionLatency, &p.completionLagMax, navigationSince(completion.finishedAt))
			p.mu.Unlock()
			completion.job.complete(completion.value, completion.err)
		default:
			return
		}
	}
}

func (p *navigationPool) stop() {
	p.mu.Lock()
	p.stopped = true
	if p.cancel != nil {
		p.cancel()
	}
	for key, job := range p.pending {
		job.cancel()
		delete(p.pending, key)
	}
	p.mu.Unlock()
	p.wg.Wait()
	for {
		select {
		case completion := <-p.completed:
			if completion.job.discard != nil {
				completion.job.discard(completion.value)
			}
		default:
			return
		}
	}
}

func (p *navigationPool) stats() NavigationQueueStats {
	p.mu.Lock()
	defer p.mu.Unlock()
	return NavigationQueueStats{Queued: len(p.pending), Running: len(p.running), Completions: len(p.completed),
		Accepted: p.accepted, Rejected: p.rejected, Finished: p.finished,
		QueueLatency: p.queueLatency, WorkLatency: p.workLatency, CompletionLatency: p.completionLatency,
		QueueWaitMax: p.queueWaitMax, WorkMax: p.workMax, CompletionLagMax: p.completionLagMax}
}

func recordNavigationLatency(buckets *[8]uint64, maximum *time.Duration, duration time.Duration) {
	if duration > *maximum {
		*maximum = duration
	}
	for i, bound := range NavigationLatencyUpperBounds() {
		if duration <= bound {
			buckets[i]++
			return
		}
	}
	buckets[7]++
}
