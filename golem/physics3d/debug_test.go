package physics3d

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestDebugMalformedAndBoundedInput(t *testing.T) {
	for _, s := range []string{`{}`, `{"Version":3}`, `{"Version":2,"Status":"accepted","unexpected":1}`, `{"Version":2,"Status":"accepted"} {}`, `{"Version":2,"Status":"accepted","Incomplete":true}`} {
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

func TestDebugRejectsRetiredSolverCaptureVersion(t *testing.T) {
	legacy := `{"Config":{"CCDMaxDelta":0.004166667},"Version":1}`
	if _, err := ReadDebug(strings.NewReader(legacy)); !errors.Is(err, ErrUnsupported) || !strings.Contains(err.Error(), "retired solver settings") {
		t.Fatalf("obsolete debug capture was not explicitly rejected: %v", err)
	}
	if err := (DebugCapture{Version: 1}).Validate(); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("typed obsolete capture accepted: %v", err)
	}
}

func TestDebugCopiesAndResetsNativeCCDWork(t *testing.T) {
	c := DebugCapture{Version: DebugVersion, Config: DefaultConfig(), Status: "faulted"}
	o := DefaultDebugOptions()
	o.Frame = &FrameResult{ID: 3, CCDCandidatePairs: 12, CCDPairTests: 9, CCDPredictiveContacts: 2}
	if err := c.CopyAnnotations(o); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := WriteDebug(&buf, c); err != nil {
		t.Fatal(err)
	}
	read, err := ReadDebug(&buf)
	if err != nil || read.CCDCandidatePairs != 12 || read.CCDPairTests != 9 || read.CCDPredictiveContacts != 2 {
		t.Fatalf("native counts not preserved: %+v error=%v", read, err)
	}
	c.CCDPairTests = -1
	if err = c.Validate(); err == nil {
		t.Fatal("negative work accepted")
	}
	if err = c.CopyAnnotations(DefaultDebugOptions()); err != nil || c.CCDCandidatePairs != 0 || c.CCDPairTests != 0 || c.CCDPredictiveContacts != 0 {
		t.Fatalf("stale frame work survived reset: %+v error=%v", c, err)
	}
}

func TestDebugContinuationWorkRetainsFailedAttemptsAndRejectsNegativeCounts(t *testing.T) {
	c := DebugCapture{Version: DebugVersion, Config: DefaultConfig(), Status: "faulted"}
	o := DefaultDebugOptions()
	o.Frame = &FrameResult{ID: 7, TOIContinuationCandidates: 31, TOIContinuationPairTests: 23,
		TOIContinuationEvents: 3, TOIContinuationIslandBodies: 8, TOIContinuationContactRows: 19}
	counts := func(c DebugCapture) [5]int {
		return [5]int{c.TOIContinuationCandidates, c.TOIContinuationPairTests, c.TOIContinuationEvents, c.TOIContinuationIslandBodies, c.TOIContinuationContactRows}
	}
	if err := c.CopyAnnotations(o); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := WriteDebug(&buf, c); err != nil {
		t.Fatal(err)
	}
	read, err := ReadDebug(bytes.NewReader(buf.Bytes()))
	if err != nil || counts(read) != [5]int{31, 23, 3, 8, 19} || read.FrameAccepted || read.Frame != 7 || len(read.Contacts) != 0 {
		t.Fatalf("attempted work was lost or treated as accepted contacts: %+v error=%v", read, err)
	}
	for i := range 5 {
		bad := c
		fields := []*int{&bad.TOIContinuationCandidates, &bad.TOIContinuationPairTests, &bad.TOIContinuationEvents, &bad.TOIContinuationIslandBodies, &bad.TOIContinuationContactRows}
		*fields[i] = -1
		if err := bad.Validate(); err == nil {
			t.Fatalf("negative continuation count %d accepted", i)
		}
		data, err := json.Marshal(bad)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ReadDebug(bytes.NewReader(data)); err == nil {
			t.Fatalf("negative serialized continuation count %d accepted", i)
		}
	}
	if err := c.CopyAnnotations(DefaultDebugOptions()); err != nil || counts(c) != [5]int{} {
		t.Fatalf("continuation work survived annotation reset: %+v error=%v", c, err)
	}
}

type ioRepeatByte struct{}

func (r ioRepeatByte) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = ' '
	}
	return len(p), nil
}
