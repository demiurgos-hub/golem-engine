package netwire

// Distance returns the forward modulo-65536 distance between sequence numbers.
func Distance(older, newer uint16) uint16 { return newer - older }

// Newer reports a strictly newer serial number. Half-range separation is
// ambiguous, so neither direction is newer at a distance of exactly 32768.
func Newer(a, b uint16) bool { return a != b && Distance(b, a) < 1<<15 }

// Acknowledges reports positive receipt evidence, without classifying losses.
func (ack ACK) Acknowledges(sequence uint16) bool {
	if sequence == ack.Sequence {
		return true
	}
	distance := Distance(sequence, ack.Sequence)
	return distance > 0 && distance <= AckWindow && ack.Mask.HasBit(int(distance-1))
}

// SequenceWindow deduplicates the newest sequence and its 128 predecessors.
// The zero value accepts its first observed sequence. Callers provide locking.
type SequenceWindow struct {
	initialized bool
	latest      uint16
	mask        AckMask
}

// Snapshot returns the current selective ACK fields, or zero before first receipt.
func (w *SequenceWindow) Snapshot() ACK { return ACK{Sequence: w.latest, Mask: w.mask} }

// Accept records a new sequence or previously unseen in-window predecessor.
func (w *SequenceWindow) Accept(sequence uint16) bool {
	if !w.initialized {
		w.initialized = true
		w.latest = sequence
		w.mask = AckMask{}
		return true
	}
	if sequence == w.latest {
		return false
	}
	if Newer(sequence, w.latest) {
		distance := Distance(w.latest, sequence)
		w.mask.ShiftLeft(int(distance))
		w.mask.SetBit(int(distance) - 1)
		w.latest = sequence
		return true
	}
	distance := Distance(sequence, w.latest)
	if distance == 0 || distance > AckWindow || w.mask.HasBit(int(distance)-1) {
		return false
	}
	w.mask.SetBit(int(distance) - 1)
	return true
}
