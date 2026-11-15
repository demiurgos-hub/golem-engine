package schema

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadEventsParsesDeliveryAndDefaultsToStream(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"hit.yaml":   "event: Hit\ntarget: global\ndelivery: unreliable\nfields:\n  amount: int32\n",
		"toast.yaml": "event: Toast\ntarget: session\nfields:\n  text: string\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	events, err := LoadEvents(dir, nil)
	if err != nil {
		t.Fatalf("LoadEvents: %v", err)
	}
	if err := ValidateEvents(events, nil); err != nil {
		t.Fatalf("ValidateEvents: %v", err)
	}
	got := make(map[string]string, len(events))
	for _, ev := range events {
		got[ev.Name] = ev.Delivery + "/" + ev.DeliveryConst()
	}
	if got["Hit"] != "unreliable/EventUnreliable" || got["Toast"] != "stream/EventStream" {
		t.Fatalf("deliveries = %v", got)
	}
}

func TestValidateEventsRejectsUnknownDelivery(t *testing.T) {
	err := ValidateEvents([]EventData{{Name: "Hit", Target: "global", Delivery: "datagram"}}, nil)
	if err == nil || !strings.Contains(err.Error(), `delivery "datagram" is invalid`) {
		t.Fatalf("error = %v, want an invalid delivery error", err)
	}
}
