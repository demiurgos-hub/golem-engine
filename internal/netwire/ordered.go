package netwire

import "time"

// PendingDuplicatePolicy chooses which payload survives a duplicate future sequence.
type PendingDuplicatePolicy uint8

const (
	KeepFirst PendingDuplicatePolicy = iota
	ReplacePending
)

// OrderedBuffer retains future payloads and releases contiguous sequences from
// zero, with modulo-65536 wrap. Callers own locking, timeouts and delivery.
type OrderedBuffer struct {
	next     uint16
	pending  map[uint16][]byte
	gapSince time.Time
}

// Next returns the sequence required for the next delivery.
func (b *OrderedBuffer) Next() uint16 { return b.next }

// GapSince returns the first unresolved gap time, or zero with no buffered gap.
func (b *OrderedBuffer) GapSince() time.Time { return b.gapSince }

// AcceptCopy copies payloads only when they will be retained or delivered.
func (b *OrderedBuffer) AcceptCopy(sequence uint16, payload []byte, now time.Time, duplicates PendingDuplicatePolicy) [][]byte {
	return b.accept(sequence, payload, now, duplicates, true)
}

// AcceptOwned transfers an owned payload when it is retained or delivered.
// Callers must not reuse or mutate transferred bytes afterward.
func (b *OrderedBuffer) AcceptOwned(sequence uint16, payload []byte, now time.Time, duplicates PendingDuplicatePolicy) [][]byte {
	return b.accept(sequence, payload, now, duplicates, false)
}

func (b *OrderedBuffer) accept(sequence uint16, payload []byte, now time.Time, duplicates PendingDuplicatePolicy, copyPayload bool) [][]byte {
	if sequence == b.next {
		if copyPayload {
			payload = append([]byte(nil), payload...)
		}
		ready := [][]byte{payload}
		b.next++
		for {
			next, ok := b.pending[b.next]
			if !ok {
				break
			}
			delete(b.pending, b.next)
			ready = append(ready, next)
			b.next++
		}
		if len(b.pending) == 0 {
			b.gapSince = time.Time{}
		}
		return ready
	}
	if !Newer(sequence, b.next) {
		return nil
	}
	if b.pending == nil {
		b.pending = make(map[uint16][]byte)
	}
	if _, exists := b.pending[sequence]; exists && duplicates == KeepFirst {
		return nil
	}
	if copyPayload {
		payload = append([]byte(nil), payload...)
	}
	b.pending[sequence] = payload
	if b.gapSince.IsZero() {
		b.gapSince = now
	}
	return nil
}
