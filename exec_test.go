package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnvValue_CaseInsensitive(t *testing.T) {
	env := []string{"PATH=C:\\foo", "Other=val"}
	if v := envValue(env, "path"); v != "C:\\foo" {
		t.Errorf("expected PATH lookup to be case-insensitive, got %q", v)
	}
	if v := envValue(env, "OTHER"); v != "val" {
		t.Errorf("expected OTHER=val, got %q", v)
	}
	if v := envValue(env, "missing"); v != "" {
		t.Errorf("expected empty for missing key, got %q", v)
	}
}

func TestMergedEnv_OverridesParent(t *testing.T) {
	t.Setenv("VSENV_TEST_KEY_OVERRIDE", "parent")
	merged := mergedEnv(map[string]string{"VSENV_TEST_KEY_OVERRIDE": "child"})
	if v := envValue(merged, "VSENV_TEST_KEY_OVERRIDE"); v != "child" {
		t.Errorf("expected child override, got %q", v)
	}
	count := 0
	for _, e := range merged {
		if strings.HasPrefix(strings.ToUpper(e), "VSENV_TEST_KEY_OVERRIDE=") {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected single entry for overridden key, got %d", count)
	}
}

func TestMergedEnv_AddsNewKey(t *testing.T) {
	merged := mergedEnv(map[string]string{"VSENV_TEST_NEW_KEY": "value"})
	if v := envValue(merged, "VSENV_TEST_NEW_KEY"); v != "value" {
		t.Errorf("expected new key in merged env, got %q", v)
	}
}

func TestMergedEnv_PreservesParentKeys(t *testing.T) {
	t.Setenv("VSENV_TEST_KEEP", "preserved")
	merged := mergedEnv(map[string]string{"OTHER": "x"})
	if v := envValue(merged, "VSENV_TEST_KEEP"); v != "preserved" {
		t.Errorf("expected parent key preserved, got %q", v)
	}
}

func TestLookInEnvPath_AbsolutePath_PassesThrough(t *testing.T) {
	got, err := lookInEnvPath(`C:\foo\bar.exe`, "", ".EXE")
	if err != nil {
		t.Fatal(err)
	}
	if got != `C:\foo\bar.exe` {
		t.Errorf("absolute path should pass through, got %q", got)
	}
}

func TestLookInEnvPath_RelativeWithSeparator_PassesThrough(t *testing.T) {
	got, err := lookInEnvPath(`.\foo.exe`, "", ".EXE")
	if err != nil {
		t.Fatal(err)
	}
	if got != `.\foo.exe` {
		t.Errorf("path with separator should pass through, got %q", got)
	}
}

func TestLookInEnvPath_FindsInPath(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "tool.exe"), []byte("MZ"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := lookInEnvPath("tool", dir, ".EXE;.CMD")
	if err != nil {
		t.Fatal(err)
	}
	// Windows is case-insensitive; the function returns the queried casing.
	if !strings.EqualFold(got, filepath.Join(dir, "tool.exe")) {
		t.Errorf("expected tool.exe in %q, got %q", dir, got)
	}
}

func TestLookInEnvPath_NotFound(t *testing.T) {
	if _, err := lookInEnvPath("definitely-not-a-real-binary-xyz", t.TempDir(), ".EXE"); err == nil {
		t.Error("expected error for missing binary")
	}
}

func TestLookInEnvPath_TriesEachExtension(t *testing.T) {
	dir := t.TempDir()
	cmdPath := filepath.Join(dir, "thing.CMD")
	if err := os.WriteFile(cmdPath, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := lookInEnvPath("thing", dir, ".EXE;.CMD")
	if err != nil {
		t.Fatal(err)
	}
	if got != cmdPath {
		t.Errorf("expected to find %q, got %q", cmdPath, got)
	}
}

func TestLookInEnvPath_SkipsDirectories(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "thing.exe"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := lookInEnvPath("thing", dir, ".EXE"); err == nil {
		t.Error("should not match a directory")
	}
}
