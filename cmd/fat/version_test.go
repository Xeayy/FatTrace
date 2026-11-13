//go:build windows

package main

import "testing"

func TestResolveVersionPrefersBuildInjected(t *testing.T) {
	old := version
	t.Cleanup(func() { version = old })

	version = "1.3.1"
	if got := resolveVersion(); got != "1.3.1" {
		t.Fatalf("resolveVersion() = %q, want %q", got, "1.3.1")
	}
}

func TestResolveVersionDevFallback(t *testing.T) {
	old := version
	t.Cleanup(func() { version = old })

	// Test binaries are built with "(devel)" module info, so the placeholder
	// must survive unchanged.
	version = devVersion
	if got := resolveVersion(); got != devVersion {
		t.Fatalf("resolveVersion() = %q, want %q", got, devVersion)
	}
}
