package golem

import (
	"bytes"
	"testing"

	"golem-engine/golem/pb"
)

func TestWrapEntityUpdate(t *testing.T) {
	payload := []byte{0xDE, 0xAD, 0xBE, 0xEF}
	wrapped := WrapEntityUpdate(payload)

	rd := pb.NewReader(wrapped)
	if rd.Done() {
		t.Fatal("expected tag, got empty")
	}

	field, wire := rd.Tag()
	if field != 1 {
		t.Errorf("field = %d, want 1 (entity_update)", field)
	}
	if wire != 2 {
		t.Errorf("wire = %d, want 2 (length-delimited)", wire)
	}

	inner := rd.Bytes()
	if !bytes.Equal(inner, payload) {
		t.Errorf("inner = %x, want %x", inner, payload)
	}

	if !rd.Done() {
		t.Error("unexpected trailing bytes")
	}
}

func TestConcatenatedEntityBatch(t *testing.T) {
	payloads := [][]byte{
		{0x01, 0x02},
		{0x03, 0x04, 0x05},
		{0x06},
	}

	var batch []byte
	for _, p := range payloads {
		batch = append(batch, WrapEntityUpdate(p)...)
	}

	rd := pb.NewReader(batch)
	for i, want := range payloads {
		if rd.Done() {
			t.Fatalf("reader done before payload %d", i)
		}
		field, wire := rd.Tag()
		if field != 1 || wire != 2 {
			t.Fatalf("payload %d: field=%d wire=%d, want 1/2", i, field, wire)
		}
		got := rd.Bytes()
		if !bytes.Equal(got, want) {
			t.Fatalf("payload %d: got %x, want %x", i, got, want)
		}
	}
	if !rd.Done() {
		t.Error("unexpected trailing bytes after batch")
	}
}

func TestWrapWorldUpdate(t *testing.T) {
	payload := []byte{0xCA, 0xFE, 0xBA, 0xBE}
	wrapped := WrapWorldUpdate(payload)

	rd := pb.NewReader(wrapped)
	if rd.Done() {
		t.Fatal("expected tag, got empty")
	}

	field, wire := rd.Tag()
	if field != 2 {
		t.Errorf("field = %d, want 2 (world_update)", field)
	}
	if wire != 2 {
		t.Errorf("wire = %d, want 2 (length-delimited)", wire)
	}

	inner := rd.Bytes()
	if !bytes.Equal(inner, payload) {
		t.Errorf("inner = %x, want %x", inner, payload)
	}

	if !rd.Done() {
		t.Error("unexpected trailing bytes")
	}
}
