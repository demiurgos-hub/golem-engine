package physics3d

import (
	"bytes"
	"strings"
	"testing"
)

func TestDebugMalformedAndBoundedInput(t *testing.T) {
	for _, s := range []string{`{}`, `{"Version":2}`, `{"Version":1,"Status":"accepted","unexpected":1}`, `{"Version":1,"Status":"accepted"} {}`, `{"Version":1,"Status":"accepted","Incomplete":true}`} {
		if _, err := ReadDebug(strings.NewReader(s)); err == nil {
			t.Fatal("accepted", s)
		}
	}
	if _, err := ReadDebug(ioRepeatByte{}); err == nil {
		t.Fatal("oversized capture accepted")
	}
	c := DebugCapture{Version: DebugVersion, Config: DefaultConfig(), Status: "accepted"}
	o := DefaultDebugOptions()
	o.MaxRecords = 2
	o.Frame = &FrameResult{ID: 3, Paths: []BodyPath{{Positions: []Vec3{{}, {X: 1}, {X: 2}}}}}
	if err := c.CopyAnnotations(o); err != nil {
		t.Fatal(err)
	}
	if !c.Incomplete || c.Omitted.Records != 2 || len(c.Paths[0].Positions) != 1 {
		t.Fatal(c)
	}
	var buf bytes.Buffer
	if err := WriteDebug(&buf, c); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadDebug(&buf); err != nil {
		t.Fatal(err)
	}
}

type ioRepeatByte struct{}

func (r ioRepeatByte) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = ' '
	}
	return len(p), nil
}
