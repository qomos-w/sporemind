//go:build devrelease

package main

import "testing"

func TestAppNameDevRelease(t *testing.T) {
	if got := appName(); got != "sporemind-devrelease" {
		t.Fatalf("appName() on devrelease build = %q, want %q", got, "sporemind-devrelease")
	}
}
