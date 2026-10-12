package netwire

// SetBit marks one zero-based ACK bit inside the selective-ACK window.
func (m *AckMask) SetBit(bit int) {
	if bit < 0 || bit >= AckWindow {
		return
	}
	word := bit / 32
	shift := uint(bit % 32)
	m[word] |= uint32(1) << shift
}

// HasBit reports whether one zero-based ACK bit is set.
func (m AckMask) HasBit(bit int) bool {
	if bit < 0 || bit >= AckWindow {
		return false
	}
	word := bit / 32
	shift := uint(bit % 32)
	return m[word]&(uint32(1)<<shift) != 0
}

// ShiftLeft moves the selective-ACK window toward older packets.
func (m *AckMask) ShiftLeft(bits int) {
	if bits <= 0 {
		return
	}
	if bits >= AckWindow {
		*m = AckMask{}
		return
	}
	wordShift := bits / 32
	bitShift := uint(bits % 32)
	var shifted AckMask
	for i := len(shifted) - 1; i >= 0; i-- {
		src := i - wordShift
		if src < 0 {
			continue
		}
		shifted[i] = m[src] << bitShift
		if bitShift > 0 && src > 0 {
			shifted[i] |= m[src-1] >> (32 - bitShift)
		}
	}
	*m = shifted
}
