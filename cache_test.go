package main

import (
	"context"
	"encoding/json"
	"os"
	"testing"
)

func TestEnvCachePath_DeterministicForSameInput(t *testing.T) {
	install := &Install{InstanceID: "abc", InstallationVersion: "18.5.0"}
	a, err := envCachePath(install, "x64", "x64", "")
	if err != nil {
		t.Fatal(err)
	}
	b, err := envCachePath(install, "x64", "x64", "")
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Errorf("same input should yield same path: %q vs %q", a, b)
	}
}

func TestEnvCachePath_DiffersByInstance(t *testing.T) {
	a, _ := envCachePath(&Install{InstanceID: "one", InstallationVersion: "1"}, "x64", "x64", "")
	b, _ := envCachePath(&Install{InstanceID: "two", InstallationVersion: "1"}, "x64", "x64", "")
	if a == b {
		t.Error("different instances should yield different paths")
	}
}

func TestEnvCachePath_DiffersByVersion(t *testing.T) {
	a, _ := envCachePath(&Install{InstanceID: "i", InstallationVersion: "18.5.0"}, "x64", "x64", "")
	b, _ := envCachePath(&Install{InstanceID: "i", InstallationVersion: "18.5.1"}, "x64", "x64", "")
	if a == b {
		t.Error("different versions should yield different paths")
	}
}

func TestEnvCachePath_DiffersByArch(t *testing.T) {
	install := &Install{InstanceID: "i", InstallationVersion: "1"}
	a, _ := envCachePath(install, "x64", "x64", "")
	b, _ := envCachePath(install, "arm64", "x64", "")
	if a == b {
		t.Error("different target arch should yield different paths")
	}
}

func TestEnvCachePath_DiffersByHostArch(t *testing.T) {
	install := &Install{InstanceID: "i", InstallationVersion: "1"}
	a, _ := envCachePath(install, "x64", "x64", "")
	b, _ := envCachePath(install, "x64", "x86", "")
	if a == b {
		t.Error("different host arch should yield different paths")
	}
}

func TestEnvCachePath_DiffersByDevCmdArgs(t *testing.T) {
	install := &Install{InstanceID: "i", InstallationVersion: "1"}
	a, _ := envCachePath(install, "x64", "x64", "")
	b, _ := envCachePath(install, "x64", "x64", "-vcvars_ver=14.39")
	if a == b {
		t.Error("different dev-cmd-args should yield different paths")
	}
}

func TestGetEnv_CacheHit_SkipsDevCmd(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	install := &Install{InstanceID: "x", InstallationVersion: "1.0"}

	path, err := envCachePath(install, "x64", "x64", "")
	if err != nil {
		t.Fatal(err)
	}
	preseeded := envCache{
		Schema:              cacheSchemaVersion,
		InstanceID:          install.InstanceID,
		InstallationVersion: install.InstallationVersion,
		Arch:                "x64",
		HostArch:            "x64",
		Env:                 map[string]string{"FOO": "bar", "PATH": `C:\fake`},
	}
	data, _ := json.MarshalIndent(preseeded, "", "  ")
	if writeErr := os.WriteFile(path, data, 0o600); writeErr != nil {
		t.Fatal(writeErr)
	}

	got, err := GetEnv(context.Background(), install, "x64", "x64", "")
	if err != nil {
		t.Fatalf("GetEnv: %v", err)
	}
	if got["FOO"] != "bar" || got["PATH"] != `C:\fake` {
		t.Errorf("expected cache-hit env, got %v", got)
	}
}

func TestGetEnv_StaleSchema_ForcesRebuild(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	install := &Install{InstanceID: "x", InstallationVersion: "1.0"}

	path, err := envCachePath(install, "x64", "x64", "")
	if err != nil {
		t.Fatal(err)
	}
	stale := map[string]any{"schema": 999, "env": map[string]string{"FOO": "stale"}}
	data, _ := json.Marshal(stale)
	if writeErr := os.WriteFile(path, data, 0o600); writeErr != nil {
		t.Fatal(writeErr)
	}

	// GetEnv should not return the stale FOO; it'll try to spawn cmd.exe and fail
	// because the install path is fake. We just verify it didn't blindly use the cache.
	got, err := GetEnv(context.Background(), install, "x64", "x64", "")
	if err == nil && got["FOO"] == "stale" {
		t.Error("stale-schema cache should have been rejected")
	}
}

func TestClearEnvCache(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	install := &Install{InstanceID: "x", InstallationVersion: "1.0"}
	path, _ := envCachePath(install, "x64", "x64", "")
	if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ClearEnvCache(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("expected cache file removed, stat err=%v", err)
	}
}
