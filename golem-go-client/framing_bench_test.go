package golemclient

import (
	"fmt"
	"testing"

	"github.com/demiurgos-hub/golem-engine/golem/pb"
)

// BenchmarkDecodeServerMessages measures decoding the envelopes of one stream
// frame: a single entity update, and 200 of them as one busy tick sends. Every
// stream frame of every game passes through here, so ServerMessage must stay
// small: before space transitions it held three payload slices (72 bytes),
// now a field number and one payload (32 bytes).
func BenchmarkDecodeServerMessages(b *testing.B) {
	for _, n := range []int{1, 200} {
		var frame []byte
		for i := 0; i < n; i++ {
			frame = append(frame, (&pb.Writer{}).Tag(serverMessageEntityField, 2).Bytes([]byte(fmt.Sprintf("entity-update-%03d", i))).Finish()...)
		}
		b.Run(fmt.Sprintf("envelopes=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, err := decodeServerMessages(frame); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// Decoding keeps every known field in order, with its payload, and skips
// fields it does not know.
func TestDecodeServerMessagesKeepsFieldsAndOrder(t *testing.T) {
	w := &pb.Writer{}
	w.Tag(serverMessageSpaceChangedField, 2).Bytes(nil)
	w.Tag(serverMessageEntityField, 2).Bytes([]byte("entity"))
	w.Tag(99, 2).Bytes([]byte("unknown"))
	w.Tag(serverMessageEventField, 2).Bytes([]byte("event"))
	w.Tag(serverMessageWorldField, 2).Bytes([]byte("world"))
	got, err := decodeServerMessages(w.Finish())
	if err != nil {
		t.Fatal(err)
	}
	want := []ServerMessage{
		{Field: serverMessageSpaceChangedField},
		{Field: serverMessageEntityField, Payload: []byte("entity")},
		{Field: serverMessageEventField, Payload: []byte("event")},
		{Field: serverMessageWorldField, Payload: []byte("world")},
	}
	if len(got) != len(want) {
		t.Fatalf("decoded %d fields, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i].Field != want[i].Field || string(got[i].Payload) != string(want[i].Payload) {
			t.Fatalf("field %d = %d %q, want %d %q", i, got[i].Field, got[i].Payload, want[i].Field, want[i].Payload)
		}
	}
}
