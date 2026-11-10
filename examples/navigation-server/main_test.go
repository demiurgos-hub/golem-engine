package main

import "testing"

func TestServerNavigationLifecycle(t *testing.T) {
	if err := run(t.TempDir()); err != nil {
		t.Fatal(err)
	}
}
