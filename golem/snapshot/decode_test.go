package snapshot

import (
	"github.com/demiurgos-hub/golem-engine/golem/pb"
	"testing"
)

func TestDecodeRejectsMalformedSnapshots(t *testing.T) {
	w := (&pb.Writer{}).Raw(magic[:]).Uint32(1).Int64(0).String("schema").Uint32(1).Int64(1).String("Monster").Bytes([]byte{37})
	valid := w.Finish()
	if _, err := Decode(valid, "schema"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < len(valid); i++ {
		if _, err := Decode(valid[:i], "schema"); err == nil {
			t.Fatalf("accepted truncated snapshot at %d", i)
		}
	}
	if _, err := Decode(append(append([]byte(nil), valid...), 0), "schema"); err == nil {
		t.Fatal("trailing bytes accepted")
	}
	huge := (&pb.Writer{}).Raw(magic[:]).Uint32(1).Int64(0).String("schema").Uint32(^uint32(0)).Finish()
	if _, err := Decode(huge, "schema"); err == nil {
		t.Fatal("unbounded record count accepted")
	}
	bad := append(append([]byte(nil), magic[:]...), []byte{0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 1}...)
	if _, err := Decode(bad, "schema"); err == nil {
		t.Fatal("overflow varint accepted")
	}
}
