package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestParentExePath(t *testing.T) {
	path, err := parentExePath()
	if err != nil {
		t.Fatalf("parentExePath: %v", err)
	}
	if !filepath.IsAbs(path) {
		t.Errorf("expected an absolute path, got %q", path)
	}
	if !strings.HasSuffix(strings.ToLower(path), ".exe") {
		t.Errorf("expected .exe suffix, got %q", path)
	}
	t.Logf("parent exe: %s", path)
}

func TestDetectParentShell(t *testing.T) {
	id, exe := detectParentShell()
	if id == "" {
		t.Fatal("detectParentShell returned empty ID")
	}
	t.Logf("detected parent shell: %s (%s)", id, exe)
}

func TestShellIDFromExe(t *testing.T) {
	cases := map[string]string{
		"nu.exe":                                 shellIDNu,
		`C:\Program Files\PowerShell\7\pwsh.exe`: shellIDPwsh,
		"PowerShell.exe":                         shellIDPowerShell,
		"cmd.exe":                                shellIDCmd,
		`C:\Program Files\Git\usr\bin\bash.exe`:  shellIDBash,
		"sh.exe":                                 shellIDBash,
		`C:\msys64\usr\bin\fish.exe`:             shellIDFish,
		"go.exe":                                 "",
	}
	for in, want := range cases {
		if got := shellIDFromExe(in); got != want {
			t.Errorf("shellIDFromExe(%q) = %q, want %q", in, got, want)
		}
	}
}
