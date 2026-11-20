package main

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

func TestParseOptionsFlagsOverrideEnv(t *testing.T) {
	env := map[string]string{
		"GOLEM_INSPECTOR_ADDR":           ":9000",
		"GOLEM_INSPECTOR_AGENT_TOKEN":    "from-env",
		"GOLEM_INSPECTOR_PUBLIC_ORIGINS": "https://a.example/, https://b.example",
		"GOLEM_INSPECTOR_BEHIND_PROXY":   "true",
		"GOLEM_INSPECTOR_OBSERVE_GRACE":  "5s",
	}
	o, err := parseOptions([]string{"-agent-token", "from-flag"}, func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if o.addr != ":9000" || o.hub.AgentToken != "from-flag" || o.hub.ObserveGrace != 5*time.Second {
		t.Fatalf("options = %+v", o)
	}
	if len(o.hub.PublicOrigins) != 2 || o.hub.PublicOrigins[0] != "https://a.example" {
		t.Fatalf("origins = %v", o.hub.PublicOrigins)
	}
	if !o.hub.BehindProxy || !o.hub.CookieSecure || o.hub.TLS {
		t.Fatalf("proxy/cookie/tls = %v/%v/%v", o.hub.BehindProxy, o.hub.CookieSecure, o.hub.TLS)
	}
	o, err = parseOptions([]string{"-cookie-secure=false", "-observe-grace=0"}, func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	if o.hub.CookieSecure || o.hub.ObserveGrace >= 0 || o.addr != "127.0.0.1:8787" {
		t.Fatalf("defaults = %+v", o)
	}
	if _, err := parseOptions([]string{"stray"}, func(string) string { return "" }); err == nil {
		t.Fatal("stray argument accepted")
	}
}

func TestUIFSFallsBackToPlaceholder(t *testing.T) {
	ui, err := uiFS("")
	if err != nil {
		t.Fatal(err)
	}
	index, err := fs.ReadFile(ui, "index.html")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(index), "Golem Inspector") {
		t.Fatalf("index = %q", index)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("disk"), 0o644); err != nil {
		t.Fatal(err)
	}
	ui, err = uiFS(dir)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := fs.ReadFile(ui, "index.html"); string(b) != "disk" {
		t.Fatalf("web dir index = %q", b)
	}
	if _, err := uiFS(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("missing web dir accepted")
	}
}

func TestHashPassword(t *testing.T) {
	var out bytes.Buffer
	if err := hashPassword(strings.NewReader("correct horse battery\n"), &out); err != nil {
		t.Fatal(err)
	}
	hash := strings.TrimSpace(out.String())
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte("correct horse battery")) != nil {
		t.Fatal("hash does not verify")
	}
	if cost, _ := bcrypt.Cost([]byte(hash)); cost < 12 {
		t.Fatalf("cost = %d", cost)
	}
	if err := hashPassword(strings.NewReader("short\n"), &out); err == nil {
		t.Fatal("short password accepted")
	}
}
