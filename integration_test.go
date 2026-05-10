package main

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func requireVS(t *testing.T) {
	t.Helper()
	if _, err := os.Stat(vswhereDefault); err != nil {
		t.Skipf("vswhere not installed at %s", vswhereDefault)
	}
}

func firstInstall(t *testing.T) *Install {
	t.Helper()
	installs, err := runVsWhere(context.Background())
	if err != nil {
		t.Fatalf("vswhere: %v", err)
	}
	if len(installs) == 0 {
		t.Skip("no Visual Studio installations on this machine")
	}
	return &installs[0]
}

func TestIntegration_RunVsWhere(t *testing.T) {
	requireVS(t)
	installs, err := runVsWhere(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(installs) == 0 {
		t.Skip("no VS installations")
	}
	for _, i := range installs {
		if i.InstanceID == "" {
			t.Errorf("entry missing instanceId: %+v", i)
		}
		if i.InstallationPath == "" {
			t.Errorf("entry missing installationPath: %+v", i)
		}
		if i.InstallationVersion == "" {
			t.Errorf("entry missing installationVersion: %+v", i)
		}
		if _, statErr := os.Stat(i.InstallationPath); statErr != nil {
			t.Errorf("installationPath does not exist: %s", i.InstallationPath)
		}
	}
}

func TestIntegration_DiscoverInstalls_WritesCache(t *testing.T) {
	requireVS(t)
	t.Setenv("LOCALAPPDATA", t.TempDir())

	a, err := DiscoverInstalls(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(a) == 0 {
		t.Skip("no VS installs")
	}

	cachePath, err := installsCachePath()
	if err != nil {
		t.Fatal(err)
	}
	if _, statErr := os.Stat(cachePath); statErr != nil {
		t.Errorf("expected cache file written: %v", statErr)
	}

	// Second call should hit cache. Check by removing vswhere from PATH
	// — actually we can't easily; just verify equivalence.
	b, err := DiscoverInstalls(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(a) != len(b) {
		t.Errorf("cached result differs: %d vs %d", len(a), len(b))
	}
}

func TestIntegration_DiscoverInstalls_ForceRefresh(t *testing.T) {
	requireVS(t)
	t.Setenv("LOCALAPPDATA", t.TempDir())

	if _, err := DiscoverInstalls(context.Background(), false); err != nil {
		t.Fatal(err)
	}

	// Force refresh should re-run vswhere even if cache exists
	if _, err := DiscoverInstalls(context.Background(), true); err != nil {
		t.Fatal(err)
	}
}

func TestIntegration_CaptureDevShellEnv(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping VsDevCmd.bat run in short mode")
	}
	requireVS(t)
	install := firstInstall(t)

	env, err := captureDevShellEnv(context.Background(), install, "x64", "x64", "")
	if err != nil {
		t.Fatal(err)
	}

	requiredKeys := []string{"VSINSTALLDIR", "VCINSTALLDIR", "INCLUDE", "LIB", "PATH", "VSCMD_VER"}
	for _, k := range requiredKeys {
		if _, ok := env[k]; !ok {
			t.Errorf("missing expected env var %s in VsDevCmd output", k)
		}
	}

	if v := env["VSCMD_ARG_TGT_ARCH"]; v != "x64" {
		t.Errorf("VSCMD_ARG_TGT_ARCH = %q, want x64", v)
	}
}

func TestIntegration_GetEnv_FullPipeline(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping full pipeline in short mode")
	}
	requireVS(t)
	t.Setenv("LOCALAPPDATA", t.TempDir())

	install, err := SelectInstall(context.Background(), "")
	if err != nil {
		t.Skipf("no install: %v", err)
	}

	// Cold call: should run VsDevCmd.bat
	cold := time.Now()
	env1, err := GetEnv(context.Background(), install, "x64", "x64", "")
	if err != nil {
		t.Fatal(err)
	}
	coldDur := time.Since(cold)

	if v := env1["VSINSTALLDIR"]; !strings.Contains(strings.ToLower(v), "visual studio") {
		t.Errorf("VSINSTALLDIR doesn't look right: %q", v)
	}

	// Warm call: cache hit, must be fast
	warm := time.Now()
	env2, err := GetEnv(context.Background(), install, "x64", "x64", "")
	if err != nil {
		t.Fatal(err)
	}
	warmDur := time.Since(warm)

	if warmDur > 500*time.Millisecond {
		t.Errorf("warm cache took %v (>500ms); cold took %v", warmDur, coldDur)
	}
	if len(env1) != len(env2) {
		t.Errorf("cold/warm env size mismatch: %d vs %d", len(env1), len(env2))
	}
	for k, v := range env1 {
		if env2[k] != v {
			t.Errorf("warm cache differs for %s: %q vs %q", k, env2[k], v)
			break
		}
	}

	t.Logf("cold: %v, warm: %v, env vars: %d", coldDur, warmDur, len(env1))
}

func TestIntegration_GetEnv_DifferentArchsCacheSeparately(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping (slow: runs VsDevCmd twice)")
	}
	requireVS(t)
	t.Setenv("LOCALAPPDATA", t.TempDir())

	install, err := SelectInstall(context.Background(), "")
	if err != nil {
		t.Skipf("no install: %v", err)
	}

	envX64, err := GetEnv(context.Background(), install, "x64", "x64", "")
	if err != nil {
		t.Fatal(err)
	}
	envX86, err := GetEnv(context.Background(), install, "x86", "x64", "")
	if err != nil {
		t.Fatalf("x86 capture: %v", err)
	}

	if envX64["VSCMD_ARG_TGT_ARCH"] != "x64" {
		t.Errorf("x64 cache has wrong target arch: %q", envX64["VSCMD_ARG_TGT_ARCH"])
	}
	if envX86["VSCMD_ARG_TGT_ARCH"] != "x86" {
		t.Errorf("x86 cache has wrong target arch: %q", envX86["VSCMD_ARG_TGT_ARCH"])
	}

	// LIB paths should differ between archs
	if envX64["LIB"] == envX86["LIB"] {
		t.Errorf("LIB should differ between x64 and x86, both = %q", envX64["LIB"])
	}
}

func TestIntegration_VswhereOutput_HasExpectedFields(t *testing.T) {
	requireVS(t)
	installs, err := runVsWhere(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(installs) == 0 {
		t.Skip("no installs")
	}
	i := installs[0]
	// These four are what we use in cache keys / display.
	if i.InstanceID == "" || i.InstallationPath == "" ||
		i.InstallationVersion == "" || i.DisplayName == "" {
		t.Errorf("vswhere returned an install missing required fields: %+v", i)
	}
}
