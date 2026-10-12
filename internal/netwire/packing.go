package netwire

import "fmt"

// ChunkSizeError distinguishes invalid targets, logical caps and strict encoded caps.
type ChunkSizeError struct {
	Kind        ChunkSizeKind
	Size, Limit int
}

// ChunkSizeKind identifies the size constraint a frame or target violated.
type ChunkSizeKind uint8

const (
	InvalidChunkTarget ChunkSizeKind = iota
	LogicalFrameTooLarge
	EncodedFrameTooLarge
)

// Error describes the failed size check without endpoint context.
func (e ChunkSizeError) Error() string {
	return fmt.Sprintf("chunk size check %d: size %d, limit %d", e.Kind, e.Size, e.Limit)
}

// ChunkSizer calculates greedy boundaries independently of output allocation.
type ChunkSizer struct {
	target, pending      int
	allowOversizedSingle bool
}

// NewChunkSizer initializes a chunk target. Empty-batch callers should return
// before constructing it when their contract skips target validation for empty input.
func NewChunkSizer(targetBytes int, allowOversizedSingle bool) (ChunkSizer, error) {
	if targetBytes <= 0 {
		return ChunkSizer{}, ChunkSizeError{Kind: InvalidChunkTarget, Size: targetBytes}
	}
	return ChunkSizer{target: targetBytes, allowOversizedSingle: allowOversizedSingle}, nil
}

// Add validates the incoming frame before advancing the boundary. On failure
// state is unchanged; flushBefore means the previous nonempty chunk is complete.
func (s *ChunkSizer) Add(logicalBytes, encodedBytes int) (flushBefore bool, err error) {
	if logicalBytes > MaxReliableMessageBytes {
		return false, ChunkSizeError{LogicalFrameTooLarge, logicalBytes, MaxReliableMessageBytes}
	}
	if !s.allowOversizedSingle && encodedBytes > s.target {
		return false, ChunkSizeError{EncodedFrameTooLarge, encodedBytes, s.target}
	}
	flushBefore = s.pending > 0 && s.pending+encodedBytes > s.target
	if flushBefore {
		s.pending = 0
	}
	s.pending += encodedBytes
	return flushBefore, nil
}

// PendingBytes returns the encoded bytes in the current chunk.
func (s *ChunkSizer) PendingBytes() int { return s.pending }
