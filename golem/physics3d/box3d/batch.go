package box3d

import (
	"fmt"
	"sync"
	"sync/atomic"

	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
)

// QueryBatch executes bounded parallel read-only queries, joining every worker
// before the tick owner can mutate or simulate the world. See physics3d.QueryTask
// for the caller's buffer ownership requirements.
func (w *World) QueryBatch(tasks []p.QueryTask, outcomes []p.QueryOutcome, workers int) error {
	if err := w.queryReady(); err != nil {
		return err
	}
	if workers < 1 || workers > 64 || len(outcomes) < len(tasks) {
		return p.ErrInvalid
	}
	for i, task := range tasks {
		if err := task.Query.Validate(); err != nil {
			return fmt.Errorf("query %d: %w", i, err)
		}
	}
	var next atomic.Int64
	var group sync.WaitGroup
	run := func() {
		defer group.Done()
		for {
			start := int(next.Add(32)) - 32
			if start >= len(tasks) {
				return
			}
			for i := start; i < min(start+32, len(tasks)); i++ {
				task := tasks[i]
				r, err := w.query(task.Query, task.Hits)
				outcomes[i] = p.QueryOutcome{Result: r, Err: err}
			}
		}
	}
	for i := 0; i < min(workers, len(tasks)); i++ {
		group.Add(1)
		go run()
	}
	group.Wait()
	w.stats.Queries += uint64(len(tasks))
	for _, outcome := range outcomes[:len(tasks)] {
		if outcome.Result.Incomplete {
			w.stats.Overflows++
		}
	}
	return nil
}

var _ p.BatchQuerier = (*World)(nil)
