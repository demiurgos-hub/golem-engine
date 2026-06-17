package net

import (
	"bytes"
	"testing"
)

type countingBuffer struct {
	bytes.Buffer
	writes int
}

func (b *countingBuffer) Write(p []byte) (int, error) {
	b.writes++
	return b.Buffer.Write(p)
}

func TestReliableFrameRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	want := []byte("hello framed world")
	if err := writeReliableFrame(&buf, want); err != nil {
		t.Fatalf("writeReliableFrame: %v", err)
	}
	got, err := readReliableFrame(&buf)
	if err != nil {
		t.Fatalf("readReliableFrame: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("reliable frame payload = %q, want %q", got, want)
	}
}

func TestReadReliableFrameRejectsOversizeLength(t *testing.T) {
	var buf bytes.Buffer
	buf.Write([]byte{0, 0, 125, 1})
	if _, err := readReliableFrame(&buf); err == nil {
		t.Fatal("readReliableFrame returned nil for oversized frame length")
	}
}

func TestChunkReliableFramesEmptyBatch(t *testing.T) {
	chunks, err := chunkReliableFrames(nil, maxReliableChunkBytes, rawReliableChunkEncoder)
	if err != nil {
		t.Fatalf("chunkReliableFrames: %v", err)
	}
	if len(chunks) != 0 {
		t.Fatalf("chunks len = %d, want 0", len(chunks))
	}
}

func TestChunkReliableFramesRawSingleChunk(t *testing.T) {
	chunks, err := chunkReliableFrames([][]byte{[]byte("ab"), []byte("cd"), []byte("ef")}, 6, rawReliableChunkEncoder)
	if err != nil {
		t.Fatalf("chunkReliableFrames: %v", err)
	}
	if len(chunks) != 1 {
		t.Fatalf("chunks len = %d, want 1", len(chunks))
	}
	if got, want := chunks[0], []byte("abcdef"); !bytes.Equal(got, want) {
		t.Fatalf("chunk = %q, want %q", got, want)
	}
}

func TestChunkReliableFramesRawSplitsAtTarget(t *testing.T) {
	chunks, err := chunkReliableFrames([][]byte{[]byte("abc"), []byte("de"), []byte("fgh")}, 5, rawReliableChunkEncoder)
	if err != nil {
		t.Fatalf("chunkReliableFrames: %v", err)
	}
	if len(chunks) != 2 {
		t.Fatalf("chunks len = %d, want 2", len(chunks))
	}
	if got, want := chunks[0], []byte("abcde"); !bytes.Equal(got, want) {
		t.Fatalf("first chunk = %q, want %q", got, want)
	}
	if got, want := chunks[1], []byte("fgh"); !bytes.Equal(got, want) {
		t.Fatalf("second chunk = %q, want %q", got, want)
	}
}

func TestChunkReliableFramesRejectsOversizedLogicalFrame(t *testing.T) {
	frames := [][]byte{bytes.Repeat([]byte("x"), maxReliableMessageBytes+1)}
	if _, err := chunkReliableFrames(frames, maxReliableChunkBytes, rawReliableChunkEncoder); err == nil {
		t.Fatal("chunkReliableFrames returned nil for oversized logical frame")
	}
}

func TestChunkReliableFramesLengthPrefixedDecodeAcrossChunks(t *testing.T) {
	frames := [][]byte{
		[]byte("hello"),
		[]byte("x"),
		[]byte{},
		[]byte("bye"),
	}
	chunks, err := chunkReliableFrames(frames, 14, lengthPrefixedReliableChunkEncoder)
	if err != nil {
		t.Fatalf("chunkReliableFrames: %v", err)
	}
	if len(chunks) != 2 {
		t.Fatalf("chunks len = %d, want 2", len(chunks))
	}

	var buf bytes.Buffer
	for _, chunk := range chunks {
		if _, err := buf.Write(chunk); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	for i, want := range frames {
		got, err := readReliableFrame(&buf)
		if err != nil {
			t.Fatalf("readReliableFrame %d: %v", i, err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("frame %d = %q, want %q", i, got, want)
		}
	}
}

func TestChunkReliableFramesLengthPrefixedSingleFrameMayExceedChunkTarget(t *testing.T) {
	frame := bytes.Repeat([]byte("z"), maxReliableMessageBytes)
	chunks, err := chunkReliableFrames([][]byte{frame}, maxReliableChunkBytes, lengthPrefixedReliableChunkEncoder)
	if err != nil {
		t.Fatalf("chunkReliableFrames: %v", err)
	}
	if len(chunks) != 1 {
		t.Fatalf("chunks len = %d, want 1", len(chunks))
	}
	if got, want := len(chunks[0]), reliableFrameHeaderBytes+len(frame); got != want {
		t.Fatalf("chunk len = %d, want %d", got, want)
	}

	var buf bytes.Buffer
	if _, err := buf.Write(chunks[0]); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, err := readReliableFrame(&buf)
	if err != nil {
		t.Fatalf("readReliableFrame: %v", err)
	}
	if !bytes.Equal(got, frame) {
		t.Fatal("decoded frame did not round-trip")
	}
}

func TestWriteReliableFrameChunksRawMatchesChunkReliableFrames(t *testing.T) {
	frames := [][]byte{[]byte("abc"), []byte("de"), []byte("fgh")}
	chunks, err := chunkReliableFrames(frames, 5, rawReliableChunkEncoder)
	if err != nil {
		t.Fatalf("chunkReliableFrames: %v", err)
	}

	var got [][]byte
	scratch, err := writeReliableFrameChunks(frames, 5, rawReliableChunkEncoder, nil, func(chunk []byte) error {
		got = append(got, append([]byte(nil), chunk...))
		return nil
	})
	if err != nil {
		t.Fatalf("writeReliableFrameChunks: %v", err)
	}
	if len(scratch) != 0 {
		t.Fatalf("scratch len = %d, want 0", len(scratch))
	}
	if len(got) != len(chunks) {
		t.Fatalf("written chunks = %d, want %d", len(got), len(chunks))
	}
	for i := range chunks {
		if !bytes.Equal(got[i], chunks[i]) {
			t.Fatalf("chunk %d = %q, want %q", i, got[i], chunks[i])
		}
	}
}

func TestWriteReliableFrameChunksLengthPrefixedMatchesChunkReliableFrames(t *testing.T) {
	frames := [][]byte{[]byte("hello"), []byte("x"), []byte{}, []byte("bye")}
	chunks, err := chunkReliableFrames(frames, 14, lengthPrefixedReliableChunkEncoder)
	if err != nil {
		t.Fatalf("chunkReliableFrames: %v", err)
	}

	var got bytes.Buffer
	var writes int
	scratch, err := writeReliableFrameChunks(frames, 14, lengthPrefixedReliableChunkEncoder, make([]byte, 0, 32), func(chunk []byte) error {
		writes++
		_, err := got.Write(chunk)
		return err
	})
	if err != nil {
		t.Fatalf("writeReliableFrameChunks: %v", err)
	}
	if len(scratch) != 0 {
		t.Fatalf("scratch len = %d, want 0", len(scratch))
	}
	if writes != len(chunks) {
		t.Fatalf("writes = %d, want %d", writes, len(chunks))
	}

	var want bytes.Buffer
	for _, chunk := range chunks {
		if _, err := want.Write(chunk); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	if !bytes.Equal(got.Bytes(), want.Bytes()) {
		t.Fatalf("streamed bytes = %x, want %x", got.Bytes(), want.Bytes())
	}
}

func TestWriteReliableFrameChunksRejectsOversizedLogicalFrame(t *testing.T) {
	frames := [][]byte{bytes.Repeat([]byte("x"), maxReliableMessageBytes+1)}
	if _, err := writeReliableFrameChunks(frames, maxReliableChunkBytes, rawReliableChunkEncoder, nil, func([]byte) error {
		t.Fatal("write called for oversized frame")
		return nil
	}); err == nil {
		t.Fatal("writeReliableFrameChunks returned nil for oversized logical frame")
	}
}

func TestWriteReliableFrameUsesSingleWrite(t *testing.T) {
	var buf countingBuffer
	want := []byte("single-write")
	if err := writeReliableFrame(&buf, want); err != nil {
		t.Fatalf("writeReliableFrame: %v", err)
	}
	if buf.writes != 1 {
		t.Fatalf("writes = %d, want 1", buf.writes)
	}
	got, err := readReliableFrame(&buf.Buffer)
	if err != nil {
		t.Fatalf("readReliableFrame: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("reliable frame payload = %q, want %q", got, want)
	}
}

func TestChunkReliableOrderedDatagramFramesFitsBoundary(t *testing.T) {
	frame := bytes.Repeat([]byte("q"), maxReliableOrderedDatagramPayloadBytes-reliableFrameHeaderBytes)
	payloads, err := chunkReliableOrderedDatagramFrames([][]byte{frame})
	if err != nil {
		t.Fatalf("chunkReliableOrderedDatagramFrames: %v", err)
	}
	if len(payloads) != 1 {
		t.Fatalf("payloads len = %d, want 1", len(payloads))
	}
	if got, want := len(payloads[0]), maxReliableOrderedDatagramPayloadBytes; got != want {
		t.Fatalf("payload len = %d, want %d", got, want)
	}
	got, err := readReliableFrame(bytes.NewReader(payloads[0]))
	if err != nil {
		t.Fatalf("readReliableFrame: %v", err)
	}
	if !bytes.Equal(got, frame) {
		t.Fatal("decoded ordered datagram frame did not round-trip")
	}
}

func TestChunkReliableOrderedDatagramFramesRejectsOversizedFrame(t *testing.T) {
	frame := bytes.Repeat([]byte("x"), maxReliableOrderedDatagramPayloadBytes-reliableFrameHeaderBytes+1)
	if _, err := chunkReliableOrderedDatagramFrames([][]byte{frame}); err == nil {
		t.Fatal("chunkReliableOrderedDatagramFrames returned nil for oversized frame")
	}
}

func TestChunkReliableOrderedDatagramFramesSplitsPayloads(t *testing.T) {
	frames := [][]byte{
		bytes.Repeat([]byte("a"), 800),
		bytes.Repeat([]byte("b"), 800),
	}
	payloads, err := chunkReliableOrderedDatagramFrames(frames)
	if err != nil {
		t.Fatalf("chunkReliableOrderedDatagramFrames: %v", err)
	}
	if len(payloads) != 2 {
		t.Fatalf("payloads len = %d, want 2", len(payloads))
	}
	for i, want := range frames {
		got, err := readReliableFrame(bytes.NewReader(payloads[i]))
		if err != nil {
			t.Fatalf("readReliableFrame payload %d: %v", i, err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("payload %d frame mismatch", i)
		}
	}
}

func TestReliableStreamWriteChunkCountMatchesWebSocketWriter(t *testing.T) {
	frames := [][]byte{[]byte("ab"), []byte("cd"), []byte("ef")}
	n, err := ReliableStreamWriteChunkCount(TransportWebSocket, frames)
	if err != nil {
		t.Fatalf("ReliableStreamWriteChunkCount: %v", err)
	}
	if n != 1 {
		t.Fatalf("ws chunks = %d, want 1 (raw concat under cap)", n)
	}
}

func TestReliableStreamWriteChunkCountWebTransport(t *testing.T) {
	frames := [][]byte{[]byte("hello"), []byte("x"), []byte("bye")}
	n, err := ReliableStreamWriteChunkCount(TransportWebTransport, frames)
	if err != nil {
		t.Fatalf("ReliableStreamWriteChunkCount: %v", err)
	}
	ref, err := chunkReliableFrames(frames, maxReliableChunkBytes, lengthPrefixedReliableChunkEncoder)
	if err != nil {
		t.Fatalf("chunkReliableFrames: %v", err)
	}
	if n != len(ref) {
		t.Fatalf("count = %d, want %d (chunkReliableFrames len)", n, len(ref))
	}
}

func TestReliableStreamWriteChunkCountRejectsOversizedLogicalFrame(t *testing.T) {
	frames := [][]byte{bytes.Repeat([]byte("x"), maxReliableMessageBytes+1)}
	if _, err := ReliableStreamWriteChunkCount(TransportWebTransport, frames); err == nil {
		t.Fatal("ReliableStreamWriteChunkCount returned nil for oversized logical frame")
	}
}

func TestReliableOrderedDatagramPayloadCount(t *testing.T) {
	frames := [][]byte{
		bytes.Repeat([]byte("a"), 800),
		bytes.Repeat([]byte("b"), 800),
	}
	n, err := ReliableOrderedDatagramPayloadCount(frames)
	if err != nil {
		t.Fatalf("ReliableOrderedDatagramPayloadCount: %v", err)
	}
	if n != 2 {
		t.Fatalf("payloads = %d, want 2", n)
	}
}
