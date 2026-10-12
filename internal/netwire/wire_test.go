package netwire

import (
	"bytes"
	"encoding/hex"
	"errors"
	"io"
	"reflect"
	"testing"
)

func unhex(t *testing.T, text string) []byte {
	t.Helper()
	data, err := hex.DecodeString(text)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestPacketGoldenBytes(t *testing.T) {
	for _, tt := range []struct {
		name  string
		lane  Lane
		flags uint8
		hex   string
	}{
		{"unreliable", LaneUnreliable, 0, "1234abcd000000018000000001020304ffffffff0001dead"},
		{"unordered", LaneReliableUnordered, 0, "1234abcd000000018000000001020304ffffffff00025678dead"},
		{"ordered", LaneReliableOrdered, 0, "1234abcd000000018000000001020304ffffffff000356789abcdead"},
		{"state", LaneEventualState, 0, "1234abcd000000018000000001020304ffffffff00040123456789abcdefdead"},
		{"ack", 255, FlagACKOnly, "1234abcd000000018000000001020304ffffffff01"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p := Packet{Sequence: 0x1234, ACK: ACK{0xabcd, AckMask{1, 0x80000000, 0x01020304, 0xffffffff}}, Flags: tt.flags, Lane: tt.lane, MessageID: 0x5678, OrderedSequence: 0x9abc, StateToken: 0x0123456789abcdef, Payload: []byte{0xde, 0xad}}
			want := unhex(t, tt.hex)
			storage := make([]byte, MaxDatagramBytes)
			got, err := EncodePacketInto(storage[:7], p)
			if err != nil || !bytes.Equal(got, want) {
				t.Fatalf("encode = %x, %v; want %x", got, err, want)
			}
			if &got[0] != &storage[0] {
				t.Fatal("scratch not reused")
			}
			decoded, err := DecodePacket(want)
			if err != nil {
				t.Fatal(err)
			}
			if decoded.Sequence != p.Sequence || decoded.ACK != p.ACK || decoded.Flags != p.Flags {
				t.Fatalf("header = %+v", decoded)
			}
			if tt.flags == FlagACKOnly {
				if decoded.Payload != nil {
					t.Fatal("ACK payload")
				}
				return
			}
			if decoded.Lane != p.Lane || !bytes.Equal(decoded.Payload, p.Payload) {
				t.Fatalf("body = %+v", decoded)
			}
			if (p.Lane == LaneReliableOrdered || p.Lane == LaneReliableUnordered) && decoded.MessageID != p.MessageID {
				t.Fatalf("message ID = %x", decoded.MessageID)
			}
			if p.Lane == LaneReliableOrdered && decoded.OrderedSequence != p.OrderedSequence {
				t.Fatalf("ordered sequence = %x", decoded.OrderedSequence)
			}
			if p.Lane == LaneEventualState && decoded.StateToken != p.StateToken {
				t.Fatalf("state token = %x", decoded.StateToken)
			}
			want[len(want)-1] = 0x55
			if decoded.Payload[1] != 0x55 {
				t.Fatal("decode must borrow payload")
			}
		})
	}
}

func TestPacketStructuralErrorsAndToleratedFlags(t *testing.T) {
	for _, tt := range []struct {
		data []byte
		err  error
	}{
		{make([]byte, PacketHeaderBytes-1), ErrPacketHeader},
		{make([]byte, PacketHeaderBytes), ErrLaneHeader},
		{append(make([]byte, PacketHeaderBytes), LaneReliableUnordered), ErrMessageID},
		{append(make([]byte, PacketHeaderBytes), LaneReliableOrdered, 0, 1, 0), ErrOrderedHeader},
		{append(make([]byte, PacketHeaderBytes), LaneEventualState, 0, 1), ErrStateToken},
	} {
		if _, err := DecodePacket(tt.data); !errors.Is(err, tt.err) {
			t.Fatalf("decode %x: %v, want %v", tt.data, err, tt.err)
		}
	}
	if _, err := DecodePacket(append(make([]byte, PacketHeaderBytes), 255)); err != (UnknownLaneError{255}) {
		t.Fatalf("unknown lane: %v", err)
	}
	data := make([]byte, PacketHeaderBytes)
	data[len(data)-1] = 0x81
	p, err := DecodePacket(append(data, 255, 42))
	if err != nil || p.Flags != 0x81 || p.Payload != nil {
		t.Fatalf("ACK trailing bytes: %+v, %v", p, err)
	}
	data[len(data)-1] = 0x80
	p, err = DecodePacket(append(data, LaneUnreliable, 42))
	if err != nil || p.Flags != 0x80 || !bytes.Equal(p.Payload, []byte{42}) {
		t.Fatalf("unknown flag: %+v, %v", p, err)
	}
}

func TestPayloadBudgetsAndPacketCeilings(t *testing.T) {
	for lane, overhead := range map[Lane]int{LaneUnreliable: 22, LaneReliableUnordered: 24, LaneReliableOrdered: 26, LaneEventualState: 30} {
		for _, limit := range []int{1, overhead - 1, overhead, 1200, 1500} {
			budget, err := PayloadBudget(limit, lane)
			if err != nil || budget != limit-overhead {
				t.Fatalf("budget(%d,%d) = %d, %v", limit, lane, budget, err)
			}
			if budget < 0 {
				continue
			}
			packet, err := EncodePacketInto(nil, Packet{Lane: lane, Payload: make([]byte, budget)})
			if err != nil || len(packet) != limit {
				t.Fatalf("encoded length = %d, %v, want %d", len(packet), err, limit)
			}
		}
	}
}

func TestControlGoldenBytesAndOwnership(t *testing.T) {
	ack := ACK{0x1234, AckMask{1, 2, 3, 0x80000000}}
	want := unhex(t, "004f475302123400000001000000020000000380000000dead")
	got := AppendACKControl(nil, ack, []byte{0xde, 0xad})
	if !bytes.Equal(got, want) {
		t.Fatalf("ACK control = %x", got)
	}
	payload, decoded, matched, err := DecodeACKControl(want)
	if err != nil || !matched || decoded != ack || !bytes.Equal(payload, []byte{0xde, 0xad}) {
		t.Fatalf("decoded = %x %+v %v %v", payload, decoded, matched, err)
	}
	want[len(want)-1] = 42
	if payload[1] != 42 {
		t.Fatal("control payload must borrow")
	}
	plain := []byte("plain")
	got, _, matched, err = DecodeACKControl(plain)
	if matched || err != nil || &got[0] != &plain[0] {
		t.Fatal("unmatched data must pass through")
	}
	if _, _, matched, err = DecodeACKControl([]byte(ACKControlPrefix)); !matched || !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatal("short control not rejected")
	}
	close1, close2 := AppendCloseControl(nil), AppendCloseControl(nil)
	if !bytes.Equal(close1, unhex(t, "004f475301")) || !IsCloseControl(close1) || IsCloseControl(append(close1, 0)) {
		t.Fatal("close recognition")
	}
	close1[0] = 1
	if !IsCloseControl(close2) {
		t.Fatal("close bytes share mutable storage")
	}
}

type testWriter struct {
	writes int
	data   []byte
	n      int
	err    error
}

func (w *testWriter) Write(data []byte) (int, error) {
	w.writes++
	w.data = append([]byte(nil), data...)
	return w.n, w.err
}

func TestFrameIOContracts(t *testing.T) {
	for _, payload := range [][]byte{nil, {}, []byte("abc"), make([]byte, MaxReliableMessageBytes)} {
		w := &testWriter{} // A short nil-error write is intentionally not retried.
		if err := WriteFrame(w, payload); err != nil || w.writes != 1 {
			t.Fatalf("write: %v, calls %d", err, w.writes)
		}
		got, err := ReadFrame(bytes.NewReader(w.data))
		if err != nil || !bytes.Equal(got, payload) || got == nil {
			t.Fatalf("read: len=%d %v", len(got), err)
		}
	}
	for _, data := range [][]byte{{}, {0, 0}, {0, 0, 0, 2, 1}} {
		_, err := ReadFrame(bytes.NewReader(data))
		want := io.ErrUnexpectedEOF
		if len(data) == 0 {
			want = io.EOF
		}
		if !errors.Is(err, want) {
			t.Fatalf("read %x: %v", data, err)
		}
	}
	_, err := ReadFrame(bytes.NewReader([]byte{255, 255, 255, 255}))
	if !reflect.DeepEqual(err, SizeError{0xffffffff, MaxReliableMessageBytes}) {
		t.Fatalf("oversize: %v", err)
	}
	w := &testWriter{}
	if err := WriteFrame(w, make([]byte, MaxReliableMessageBytes+1)); err == nil || w.writes != 0 {
		t.Fatal("oversize write")
	}
	w.err = io.ErrClosedPipe
	if err := WriteFrame(w, []byte("x")); err != io.ErrClosedPipe {
		t.Fatalf("writer error: %v", err)
	}
}

func TestEncodePacketScratchAllocations(t *testing.T) {
	buf := make([]byte, MaxDatagramBytes)
	p := Packet{Lane: LaneReliableOrdered, Payload: make([]byte, 100)}
	if n := testing.AllocsPerRun(100, func() {
		_, err := EncodePacketInto(buf, p)
		if err != nil {
			panic(err)
		}
	}); n != 0 {
		t.Fatalf("allocations = %v", n)
	}
}

func TestACKControlAllocations(t *testing.T) {
	payload := make([]byte, 100)
	var encoded []byte
	if n := testing.AllocsPerRun(100, func() { encoded = AppendACKControl(nil, ACK{}, payload) }); n != 1 {
		t.Fatalf("new ACK control allocations = %v", n)
	}
	scratch := encoded[:0]
	if n := testing.AllocsPerRun(100, func() { encoded = AppendACKControl(scratch, ACK{}, payload) }); n != 0 {
		t.Fatalf("scratch ACK control allocations = %v", n)
	}
}
