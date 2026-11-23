package net

import (
	"bytes"
	"context"
	"crypto/sha256"
	"path/filepath"
	"testing"
	"time"
)

func servedLeafHash(t *testing.T, c *ReloadableCertificate) []byte {
	t.Helper()
	certificate, err := c.GetCertificate(nil)
	if err != nil {
		t.Fatalf("GetCertificate: %v", err)
	}
	sum := sha256.Sum256(certificate.Certificate[0])
	return sum[:]
}

func TestNewReloadableCertificateRequiresBothFiles(t *testing.T) {
	for _, files := range [][2]string{{"", ""}, {"c.pem", ""}, {"", "k.pem"}} {
		if _, err := NewReloadableCertificate("HTTP", files[0], files[1]); err == nil {
			t.Fatalf("NewReloadableCertificate(%q, %q) succeeded", files[0], files[1])
		}
	}
}

func TestReloadableCertificateLifecycle(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	pairA, pairB := newTestCertificatePair(t), newTestCertificatePair(t)
	mtime := time.Now().Add(-time.Hour).Truncate(time.Second)
	writeTestFile(t, certPath, pairA.certPEM, mtime)
	writeTestFile(t, keyPath, pairA.keyPEM, mtime)

	c, err := NewReloadableCertificate("", certPath, keyPath)
	if err != nil {
		t.Fatalf("NewReloadableCertificate: %v", err)
	}
	if c.Loaded() || c.Hashes() != nil {
		t.Fatal("certificate loaded before Load")
	}
	if _, err := c.GetCertificate(nil); err == nil {
		t.Fatal("GetCertificate served before Load")
	}
	if gotCert, gotKey := c.Files(); gotCert != certPath || gotKey != keyPath {
		t.Fatalf("Files = %q, %q", gotCert, gotKey)
	}
	if err := c.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !c.Loaded() || !bytes.Equal(servedLeafHash(t, c), pairA.hash) || !bytes.Equal(c.Hashes()[0].Value, pairA.hash) {
		t.Fatal("Load did not serve A")
	}
	if reloaded, err := c.ReloadIfChanged(); err != nil || reloaded {
		t.Fatalf("ReloadIfChanged on unchanged files = %v, %v", reloaded, err)
	}

	// A mismatched pair (B's certificate with A's key) keeps A.
	mtime = mtime.Add(time.Minute)
	writeTestFile(t, certPath, pairB.certPEM, mtime)
	if err := c.Reload(); err == nil {
		t.Fatal("Reload of a mismatched pair succeeded")
	}
	if reloaded, err := c.ReloadIfChanged(); err == nil || reloaded {
		t.Fatalf("ReloadIfChanged of a mismatched pair = %v, %v", reloaded, err)
	}
	if !bytes.Equal(servedLeafHash(t, c), pairA.hash) {
		t.Fatal("failed reload replaced A")
	}

	writeTestFile(t, keyPath, pairB.keyPEM, mtime)
	if reloaded, err := c.ReloadIfChanged(); err != nil || !reloaded {
		t.Fatalf("ReloadIfChanged after completing B = %v, %v", reloaded, err)
	}
	if !bytes.Equal(servedLeafHash(t, c), pairB.hash) || !bytes.Equal(c.Hashes()[0].Value, pairB.hash) {
		t.Fatal("ReloadIfChanged did not serve B")
	}

	// Explicit Reload works regardless of file stamps.
	writeTestFile(t, certPath, pairA.certPEM, mtime)
	writeTestFile(t, keyPath, pairA.keyPEM, mtime)
	if err := c.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if !bytes.Equal(servedLeafHash(t, c), pairA.hash) {
		t.Fatal("Reload did not serve A")
	}
}

func TestReloadableCertificateWatch(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	pairA, pairB := newTestCertificatePair(t), newTestCertificatePair(t)
	mtime := time.Now().Add(-time.Hour).Truncate(time.Second)
	writeTestFile(t, certPath, pairA.certPEM, mtime)
	writeTestFile(t, keyPath, pairA.keyPEM, mtime)
	c, err := NewReloadableCertificate("HTTP", certPath, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Load(); err != nil {
		t.Fatal(err)
	}

	// A non-positive interval returns at once.
	c.Watch(context.Background(), 0)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		c.Watch(ctx, 5*time.Millisecond)
	}()
	mtime = mtime.Add(time.Minute)
	writeTestFile(t, certPath, pairB.certPEM, mtime)
	writeTestFile(t, keyPath, pairB.keyPEM, mtime)
	deadline := time.Now().Add(5 * time.Second)
	for !bytes.Equal(servedLeafHash(t, c), pairB.hash) {
		if time.Now().After(deadline) {
			t.Fatal("Watch did not reload B")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Watch did not stop after cancellation")
	}
}
