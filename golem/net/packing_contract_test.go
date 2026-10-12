package net

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestChunkConsumersAgreeAtBoundaries(t *testing.T) {
	for _, encoder := range []reliableChunkEncoder{rawReliableChunkEncoder, lengthPrefixedReliableChunkEncoder} {
		for _, sizes := range [][]int{{}, {0}, {0, 0}, {4, 5, 6}, {10, 0, 1}, {11}, {maxReliableMessageBytes}, {32000 - 4, 1}} {
			for _, target := range []int{5, 10, 32000, maxReliableMessageBytes} {
				for _, allow := range []bool{false, true} {
					frames := make([][]byte, len(sizes))
					for i, n := range sizes {
						frames[i] = bytes.Repeat([]byte{byte(i + 1)}, n)
					}
					chunks, packErr := chunkFrames(frames, target, encoder, allow)
					count, countErr := countFrameChunks(frames, target, encoder, allow, "ordered datagram")
					if (packErr == nil) != (countErr == nil) || (packErr != nil && packErr.Error() != countErr.Error()) || (packErr == nil && count != len(chunks)) {
						t.Fatalf("sizes=%v target=%d allow=%v: count=%d/%v, chunks=%d/%v", sizes, target, allow, count, countErr, len(chunks), packErr)
					}
					if !allow {
						continue
					}
					var written [][]byte
					scratch, err := writeReliableFrameChunks(frames, target, encoder, make([]byte, 0, maxReliableMessageBytes+4), func(b []byte) error { written = append(written, append([]byte(nil), b...)); return nil })
					if err != nil || len(scratch) != 0 || !reflect.DeepEqual(written, chunks) {
						t.Fatalf("write differs: sizes=%v target=%d: %v", sizes, target, err)
					}
				}
			}
		}
	}
}

func TestChunkValidationAndPartialWriteOrdering(t *testing.T) {
	oversized := make([]byte, maxReliableMessageBytes+1)
	for _, tt := range []struct {
		frames [][]byte
		writes int
	}{
		{[][]byte{[]byte("abc"), oversized}, 0},
		{[][]byte{[]byte("abc"), []byte("def"), oversized}, 1},
	} {
		calls := 0
		_, err := writeReliableFrameChunks(tt.frames, 5, rawReliableChunkEncoder, nil, func(b []byte) error {
			calls++
			if string(b) != "abc" {
				t.Fatalf("unexpected write %q", b)
			}
			return nil
		})
		if err == nil || calls != tt.writes || !strings.HasPrefix(err.Error(), "frame ") {
			t.Fatalf("calls=%d, err=%v", calls, err)
		}
	}
	want := errors.New("physical write failed")
	calls := 0
	_, err := writeReliableFrameChunks([][]byte{[]byte("abc"), []byte("def"), oversized}, 5, rawReliableChunkEncoder, nil, func([]byte) error { calls++; return want })
	if err != want || calls != 1 {
		t.Fatalf("write failure precedence: %v, calls=%d", err, calls)
	}
	if n, err := countReliableFrameChunks(nil, 0, rawReliableChunkEncoder); err != nil || n != 0 {
		t.Fatal("empty batch validated target")
	}
	if _, err := countReliableFrameChunks([][]byte{oversized}, 0, rawReliableChunkEncoder); err == nil || !strings.Contains(err.Error(), "invalid reliable chunk size") {
		t.Fatalf("target precedence: %v", err)
	}
	if n, err := ReliableStreamWriteChunkCount(Transport("invalid"), nil); err != nil || n != 0 {
		t.Fatal("empty batch validated transport")
	}
}

func TestPayloadCountsAllocateNoBuffers(t *testing.T) {
	frames := [][]byte{make([]byte, 700), make([]byte, 700)}
	for name, count := range map[string]func([][]byte) (int, error){"raw": UnreliableStateDatagramPayloadCount, "ordered": ReliableOrderedDatagramPayloadCount, "eventual": EventualStateDatagramPayloadCount, "stream": func(f [][]byte) (int, error) { return ReliableStreamWriteChunkCount(TransportWebTransport, f) }} {
		t.Run(name, func(t *testing.T) {
			if n := testing.AllocsPerRun(100, func() {
				if _, err := count(frames); err != nil {
					panic(err)
				}
			}); n != 0 {
				t.Fatalf("allocations=%v", n)
			}
		})
	}
	payloads, err := EventualStateDatagramPayloads(frames)
	if err != nil || len(payloads) != 2 {
		t.Fatal(err)
	}
	payloads[0][4] = 42
	if payloads[1][4] != 0 || frames[0][0] != 0 {
		t.Fatal("packed chunks alias other storage")
	}
}
