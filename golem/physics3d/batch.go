package physics3d

// QueryTask provides one query and caller-owned output storage. Hit buffers in
// one batch must not overlap; inputs and outputs must remain untouched until
// QueryBatch returns. The world remains owned by the calling tick goroutine.
type QueryTask struct {
	Query Query
	Hits  []Hit
}

// QueryOutcome records a query's bounded result or its failure. A failed query
// is never evidence that a path is clear.
type QueryOutcome struct {
	Result QueryResult
	Err    error
}

// BatchQuerier optionally executes independent queries against one synchronized
// world revision. It joins all workers before returning and never overlaps a
// simulation or mutation. Outcomes correspond to input order. A batch-level
// validation failure runs no queries and leaves the output buffers untouched.
type BatchQuerier interface {
	QueryBatch(tasks []QueryTask, outcomes []QueryOutcome, workers int) error
}
