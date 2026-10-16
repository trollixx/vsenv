package main

import "testing"

func TestResolveVersion_LdflagsWins(t *testing.T) {
	if got := resolveVersion("v1.2.3"); got != "v1.2.3" {
		t.Errorf("resolveVersion(%q) = %q, want %q", "v1.2.3", got, "v1.2.3")
	}
}

func TestResolveVersion_DevFallsBackToBuildInfo(t *testing.T) {
	// Test binaries carry no usable module version ("(devel)" or empty),
	// so "dev" must pass through unchanged rather than become "(devel)".
	if got := resolveVersion("dev"); got != "dev" {
		t.Errorf("resolveVersion(%q) = %q, want %q", "dev", got, "dev")
	}
}
