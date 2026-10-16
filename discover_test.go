package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func sample() []Install {
	return []Install{
		{InstanceID: "abc", InstallationPath: `C:\VS\A`, InstallationVersion: "18.0", DisplayName: "VS A"},
		{InstanceID: "def", InstallationPath: `C:\VS\B`, InstallationVersion: "17.0", DisplayName: "VS B"},
	}
}

// seedInstalls writes installs to a fresh installs cache.
func seedInstalls(t *testing.T, installs []Install) {
	t.Helper()
	path, err := installsCachePath()
	if err != nil {
		t.Fatal(err)
	}
	c := installsCache{Schema: cacheSchemaVersion, Installer: currentInstallerState(), Installs: installs}
	if writeErr := writeInstallsCache(path, c); writeErr != nil {
		t.Fatal(writeErr)
	}
}

func TestSelectInstall_ByID(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	seedInstalls(t, sample())

	got, err := SelectInstall(context.Background(), "def")
	if err != nil {
		t.Fatal(err)
	}
	if got.InstanceID != "def" {
		t.Errorf("expected def, got %q", got.InstanceID)
	}
}

func TestSelectInstall_DefaultPicksFirstWithMSVC(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	root := t.TempDir()
	installs := []Install{
		{InstanceID: "ssms", InstallationPath: filepath.Join(root, "ssms")},
		{InstanceID: "vs", InstallationPath: filepath.Join(root, "vs")},
		{InstanceID: "older", InstallationPath: filepath.Join(root, "older")},
	}
	for _, id := range []string{"vs", "older"} {
		dir := filepath.Join(root, id, "VC", "Auxiliary", "Build")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := writeFile(filepath.Join(dir, "Microsoft.VCToolsVersion.default.txt"), "14.50\n"); err != nil {
			t.Fatal(err)
		}
	}
	seedInstalls(t, installs)

	got, err := SelectInstall(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if got.InstanceID != "vs" {
		t.Errorf("expected the first install with MSVC (vs), got %q", got.InstanceID)
	}
}

func TestSelectInstall_DefaultPrefersComplete(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	root := t.TempDir()
	installs := []Install{
		{InstanceID: "failed", InstallationPath: filepath.Join(root, "failed")},
		{InstanceID: "vs", InstallationPath: filepath.Join(root, "vs"), IsComplete: true},
	}
	for _, i := range installs {
		dir := filepath.Join(i.InstallationPath, "VC", "Auxiliary", "Build")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := writeFile(filepath.Join(dir, "Microsoft.VCToolsVersion.default.txt"), "14.50\n"); err != nil {
			t.Fatal(err)
		}
	}
	seedInstalls(t, installs)

	got, err := SelectInstall(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if got.InstanceID != "vs" {
		t.Errorf("expected the complete install (vs), got %q", got.InstanceID)
	}
}

func TestSelectInstall_DefaultWithoutMSVC_PicksFirst(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	seedInstalls(t, sample())
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
	seedInstalls(t, sample())
	if _, err := SelectInstall(context.Background(), "nope"); err == nil {
		t.Error("expected error for unknown instance ID")
	}
}

func TestSelectInstall_NoInstallsReturnsError(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	seedInstalls(t, []Install{})
	if _, err := SelectInstall(context.Background(), ""); err == nil {
		t.Error("expected error when no installs exist")
	}
}

func TestInstallsCache_Roundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "installs.json")
	original := installsCache{
		Schema:    cacheSchemaVersion,
		Installer: installerState{Vswhere: time.Unix(1, 0), Instances: time.Unix(2, 0)},
		Installs:  sample(),
	}
	if err := writeInstallsCache(path, original); err != nil {
		t.Fatal(err)
	}
	got, ok := readInstallsCache(path)
	if !ok {
		t.Fatal("readInstallsCache reported failure")
	}
	if !got.Installer.equal(original.Installer) {
		t.Errorf("installer state mismatch: %+v vs %+v", got.Installer, original.Installer)
	}
	if len(got.Installs) != len(original.Installs) {
		t.Fatalf("len mismatch: got %d, want %d", len(got.Installs), len(original.Installs))
	}
	for i := range got.Installs {
		if got.Installs[i] != original.Installs[i] {
			t.Errorf("install %d mismatch: %+v vs %+v", i, got.Installs[i], original.Installs[i])
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

func TestReadInstallsCache_OldFormat(t *testing.T) {
	// Older versions wrote a bare list, in vswhere's unsorted order.
	path := filepath.Join(t.TempDir(), "installs.json")
	data, _ := json.Marshal(sample())
	if err := writeFile(path, string(data)); err != nil {
		t.Fatal(err)
	}
	if _, ok := readInstallsCache(path); ok {
		t.Error("expected ok=false for the old cache format")
	}
}

func TestInstallerState_ChangesWithInstances(t *testing.T) {
	t.Setenv("ProgramData", t.TempDir())
	before := currentInstallerState()
	writeInstanceState(t, "abc", time.Now().Add(-time.Hour))
	after := currentInstallerState()
	if before.equal(after) {
		t.Error("installer state should change when an instance does")
	}
	if !after.equal(currentInstallerState()) {
		t.Error("installer state should be stable while nothing changes")
	}
}

// writeInstanceState creates a fake VS Installer state.json for instanceID
// under a temporary %ProgramData%, modified at mtime.
func writeInstanceState(t *testing.T, instanceID string, mtime time.Time) {
	t.Helper()
	dir := filepath.Join(os.Getenv("ProgramData"), "Microsoft", "VisualStudio", "Packages", "_Instances", instanceID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(dir, "state.json")
	if err := writeFile(state, "{}"); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(state, mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

func TestInstanceStateTime(t *testing.T) {
	t.Setenv("ProgramData", t.TempDir())
	future := time.Now().Add(time.Hour).Truncate(time.Second)
	writeInstanceState(t, "abc", future)

	if got := instanceStateTime("abc"); !got.Equal(future) {
		t.Errorf("instanceStateTime(abc) = %v, want %v", got, future)
	}
	if got := instanceStateTime("*"); !got.Equal(future) {
		t.Errorf("instanceStateTime(*) = %v, want %v", got, future)
	}
	if got := instanceStateTime("missing"); !got.IsZero() {
		t.Errorf("instanceStateTime(missing) = %v, want zero", got)
	}
}

func TestInstanceStateTime_NoProgramData(t *testing.T) {
	t.Setenv("ProgramData", "")
	if got := instanceStateTime("*"); !got.IsZero() {
		t.Errorf("expected zero time, got %v", got)
	}
}

// writeFile is a thin helper that delegates to writeInstallsCacheRaw to keep
// imports tidy in this test file.
func writeFile(path, content string) error {
	return writeInstallsCacheRaw(path, []byte(content))
}
