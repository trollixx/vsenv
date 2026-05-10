package main

import (
	"strings"
	"testing"
)

func TestParentExeName(t *testing.T) {
	name, err := parentExeName()
	if err != nil {
		t.Fatalf("parentExeName: %v", err)
	}
	if name == "" {
		t.Fatal("parentExeName returned empty string")
	}
	if !strings.HasSuffix(strings.ToLower(name), ".exe") {
		t.Errorf("expected .exe suffix, got %q", name)
	}
	t.Logf("parent exe: %s", name)
}

func TestDetectParentShell(t *testing.T) {
	shell := detectParentShell()
	if shell == "" {
		t.Fatal("detectParentShell returned empty string")
	}
	t.Logf("detected parent shell: %s", shell)
}
