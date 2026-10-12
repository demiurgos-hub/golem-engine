package netwire

import (
	"bytes"
	"fmt"
	"testing"
	"time"
)

func TestSerialArithmeticAndACKWindow(t *testing.T) {
	for _, base := range []uint16{0, 1, 32768, 65535} {
		for _, distance := range []uint16{0, 1, 31, 32, 33, 127, 128, 129, 32767, 32768, 32769, 65535} {
			next := base + distance
			if got := Newer(next, base); got != (distance > 0 && distance < 32768) {
				t.Fatalf("Newer(%d,%d) = %v", next, base, got)
			}
			var window SequenceWindow
			if !window.Accept(base) || window.Accept(base) {
				t.Fatal("first receipt or duplicate was classified incorrectly")
			}
			accepted := window.Accept(next)
			want := distance > 0 && (distance < 32768 || Distance(next, base) <= AckWindow)
			if accepted != want {
				t.Fatalf("window base=%d distance=%d accepted=%v", base, distance, accepted)
			}
			ack := window.Snapshot()
			if !ack.Acknowledges(ack.Sequence) || ack.Acknowledges(ack.Sequence+1) {
				t.Fatal("ACK latest/future classification")
			}
			if Newer(next, base) && ack.Acknowledges(base) != (distance <= AckWindow) {
				t.Fatalf("ACK predecessor base=%d distance=%d", base, distance)
			}
		}
	}
}

func TestACKMaskShifts(t *testing.T) {
	for _, shift := range []int{0, 1, 31, 32, 33, 127, 128, 129} {
		t.Run(fmt.Sprint(shift), func(t *testing.T) {
			var mask AckMask
			bits := []int{0, 3, 31, 32, 63, 64, 96, 127}
			for _, bit := range bits {
				mask.SetBit(bit)
			}
			mask.ShiftLeft(shift)
			for bit := 0; bit < AckWindow; bit++ {
				want := false
				for _, original := range bits {
					want = want || original+shift == bit
				}
				if mask.HasBit(bit) != want {
					t.Fatalf("bit %d after shift %d", bit, shift)
				}
			}
		})
	}
}

func TestOrderedBufferPoliciesOwnershipAndGaps(t *testing.T) {
	now := time.Unix(100, 0)
	for _, policy := range []PendingDuplicatePolicy{KeepFirst, ReplacePending} {
		var buffer OrderedBuffer
		first := []byte("first")
		buffer.AcceptCopy(2, first, now, policy)
		first[0] = 'X'
		buffer.AcceptCopy(2, []byte("replacement"), now.Add(time.Second), policy)
		buffer.AcceptCopy(4, []byte("four"), now.Add(time.Second), policy)
		if got := buffer.AcceptCopy(32768, nil, now, policy); len(got) != 0 {
			t.Fatal("ambiguous sequence delivered")
		}
		zero := []byte("zero")
		ready := buffer.AcceptCopy(0, zero, now, policy)
		zero[0] = 'X'
		if len(ready) != 1 || string(ready[0]) != "zero" {
			t.Fatal("copy delivery aliases input")
		}
		ready = buffer.AcceptOwned(1, []byte("one"), now.Add(2*time.Second), policy)
		want := "first"
		if policy == ReplacePending {
			want = "replacement"
		}
		if len(ready) != 2 || string(ready[1]) != want {
			t.Fatalf("pending duplicate policy %d: %q", policy, ready)
		}
		if buffer.GapSince() != now {
			t.Fatal("partial drain postponed unresolved gap")
		}
		owned := []byte("three")
		ready = buffer.AcceptOwned(3, owned, now.Add(3*time.Second), policy)
		if len(ready) != 2 || &ready[0][0] != &owned[0] || string(ready[1]) != "four" {
			t.Fatal("owned contiguous delivery")
		}
		if !buffer.GapSince().IsZero() || buffer.Next() != 5 {
			t.Fatal("drained gap was not cleared")
		}
		if got := buffer.AcceptOwned(4, nil, now, policy); len(got) != 0 {
			t.Fatal("old sequence delivered")
		}
	}
}

func TestOrderedBufferWrap(t *testing.T) {
	var buffer OrderedBuffer
	for i := 0; i < 65536; i++ {
		if got := buffer.AcceptOwned(uint16(i), nil, time.Time{}, KeepFirst); len(got) != 1 {
			t.Fatalf("sequence %d", i)
		}
	}
	payload := []byte("wrapped")
	if got := buffer.AcceptOwned(0, payload, time.Time{}, KeepFirst); len(got) != 1 || !bytes.Equal(got[0], payload) {
		t.Fatal("wrap did not deliver")
	}
}
