package golemclient

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestReliableFrameRoundTripLargeSnapshot(t *testing.T) {
	var buf bytes.Buffer
	want := bytes.Repeat([]byte("p"), 150000)
	if err := writeReliableFrame(&buf, want); err != nil {
		t.Fatalf("writeReliableFrame: %v", err)
	}
	got, err := readReliableFrame(&buf)
	if err != nil {
		t.Fatalf("readReliableFrame: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("large reliable frame did not round-trip")
	}
}

func TestReadReliableFrameRejectsOversizeLength(t *testing.T) {
	var buf bytes.Buffer
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], maxReliableMessageBytes+1)
	buf.Write(header[:])
	if _, err := readReliableFrame(&buf); err == nil {
		t.Fatal("readReliableFrame returned nil for oversized frame length")
	}
}
