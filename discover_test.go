package main

import (
	"context"
	"path/filepath"
	"testing"
)

func sample() []Install {
	return []Install{
		{InstanceID: "abc", InstallationPath: `C:\VS\A`, InstallationVersion: "18.0", DisplayName: "VS A"},
		{InstanceID: "def", InstallationPath: `C:\VS\B`, InstallationVersion: "17.0", DisplayName: "VS B"},
	}
}

func TestSelectInstall_ByID(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	path, err := installsCachePath()
	if err != nil {
		t.Fatal(err)
	}
	if writeErr := writeInstallsCache(path, sample()); writeErr != nil {
		t.Fatal(writeErr)
	}

	got, err := SelectInstall(context.Background(), "def")
	if err != nil {
		t.Fatal(err)
	}
	if got.InstanceID != "def" {
		t.Errorf("expected def, got %q", got.InstanceID)
	}
}

func TestSelectInstall_DefaultPicksFirst(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	path, _ := installsCachePath()
	if err := writeInstallsCache(path, sample()); err != nil {
		t.Fatal(err)
	}
	got, err := SelectInstall(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if got.InstanceID != "abc" {
		t.Errorf("expected first install (abc), got %q", got.InstanceID)
	}
}

func TestSelectInstall_NotFound(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	path, _ := installsCachePath()
	if err := writeInstallsCache(path, sample()); err != nil {
		t.Fatal(err)
	}
	if _, err := SelectInstall(context.Background(), "nope"); err == nil {
		t.Error("expected error for unknown instance ID")
	}
}

func TestSelectInstall_NoInstallsReturnsError(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	path, _ := installsCachePath()
	if err := writeInstallsCache(path, []Install{}); err != nil {
		t.Fatal(err)
	}
	if _, err := SelectInstall(context.Background(), ""); err == nil {
		t.Error("expected error when no installs exist")
	}
}

func TestInstallsCache_Roundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "installs.json")
	original := sample()
	if err := writeInstallsCache(path, original); err != nil {
		t.Fatal(err)
	}
	got, ok := readInstallsCache(path)
	if !ok {
		t.Fatal("readInstallsCache reported failure")
	}
	if len(got) != len(original) {
		t.Fatalf("len mismatch: got %d, want %d", len(got), len(original))
	}
	for i := range got {
		if got[i] != original[i] {
			t.Errorf("install %d mismatch: %+v vs %+v", i, got[i], original[i])
		}
	}
}

func TestReadInstallsCache_MissingFile(t *testing.T) {
	if _, ok := readInstallsCache(filepath.Join(t.TempDir(), "does-not-exist.json")); ok {
		t.Error("expected ok=false for missing cache file")
	}
}

func TestReadInstallsCache_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	if err := writeFile(path, "not json"); err != nil {
		t.Fatal(err)
	}
	if _, ok := readInstallsCache(path); ok {
		t.Error("expected ok=false for invalid JSON")
	}
}

// writeFile is a thin helper that delegates to writeInstallsCacheRaw to keep
// imports tidy in this test file.
func writeFile(path, content string) error {
	return writeInstallsCacheRaw(path, []byte(content))
}
