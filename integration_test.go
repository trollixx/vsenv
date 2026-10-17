package main

import (
	"context"
	"os"
	"slices"
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

	env, err := captureDevShellEnv(context.Background(), install, "x64", "x64", "", baseEnv())
	if err != nil {
		t.Fatal(err)
	}

	requiredKeys := []string{"VSINSTALLDIR", "VCINSTALLDIR", "INCLUDE", "LIB", "PATH", "VSCMD_VER"}
	for _, k := range requiredKeys {
		if lookupFold(env, k) == "" {
			t.Errorf("missing expected env var %s in VsDevCmd output", k)
		}
	}

	if v := env["VSCMD_ARG_TGT_ARCH"]; v != "x64" {
		t.Errorf("VSCMD_ARG_TGT_ARCH = %q, want x64", v)
	}

	if _, leaked := env[telemetryOptOut]; leaked {
		t.Errorf("%s leaked into the captured env", telemetryOptOut)
	}
}

// Only catches a regression where the console code page isn't UTF-8, which
// is the default.
func TestIntegration_CaptureDevShellEnv_NonASCII(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping VsDevCmd.bat run in short mode")
	}
	requireVS(t)
	t.Setenv("LOCALAPPDATA", t.TempDir())
	install, err := SelectInstall(context.Background(), "")
	if err != nil {
		t.Skipf("no install: %v", err)
	}

	const value, entry = "José Олег 日本", `C:\Users\José\bin`
	base := append(baseEnv(), "VSENV_TEST_NON_ASCII="+value)
	for i, kv := range base {
		if k, v, _ := strings.Cut(kv, "="); strings.EqualFold(k, "PATH") {
			base[i] = k + "=" + entry + ";" + v
		}
	}
	full, err := captureDevShellEnv(context.Background(), install, "x64", "x64", "", base)
	if err != nil {
		t.Fatal(err)
	}
	if got := full["VSENV_TEST_NON_ASCII"]; got != value {
		t.Errorf("VSENV_TEST_NON_ASCII = %q, want %q", got, value)
	}
	diff := diffEnv(envMap(base), full)
	if _, ok := diff.Vars["VSENV_TEST_NON_ASCII"]; ok {
		t.Error("an unchanged non-ASCII variable is in the diff")
	}
	// Garbled, the original PATH isn't found in VsDevCmd's, so its entries
	// end up among the VS ones.
	if slices.ContainsFunc(diff.PathBefore, func(p string) bool { return strings.Contains(p, `\Users\Jos`) }) {
		t.Errorf("an original PATH entry is among the VS ones: %q", diff.PathBefore)
	}
}

func TestIntegration_GetEnv_FromLoadedShell(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping (slow: runs VsDevCmd twice)")
	}
	requireVS(t)
	t.Setenv("LOCALAPPDATA", t.TempDir())

	install, err := SelectInstall(context.Background(), "")
	if err != nil {
		t.Skipf("no install: %v", err)
	}
	clean, err := GetEnv(context.Background(), install, "x64", "x64", "")
	if err != nil {
		t.Fatal(err)
	}

	// Rebuild the cache from a process that already has the VS environment,
	// as after 'vsenv refresh' in a shell whose profile loaded it.
	for _, kv := range mergedEnv(clean) {
		if k, v, ok := strings.Cut(kv, "="); ok && k != "" {
			t.Setenv(k, v)
		}
	}
	if clearErr := ClearEnvCache(); clearErr != nil {
		t.Fatal(clearErr)
	}
	loaded, err := GetEnv(context.Background(), install, "x64", "x64", "")
	if err != nil {
		t.Fatal(err)
	}

	if len(loaded.Vars) != len(clean.Vars) || loaded.Vars["VSINSTALLDIR"] != clean.Vars["VSINSTALLDIR"] {
		t.Errorf("capture depends on the calling shell: %d vars vs %d clean", len(loaded.Vars), len(clean.Vars))
	}
	if !slices.Equal(loaded.PathBefore, clean.PathBefore) || !slices.Equal(loaded.PathAfter, clean.PathAfter) {
		t.Errorf("PATH entries differ:\nloaded %q\nclean  %q", loaded.PathBefore, clean.PathBefore)
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

	if v := env1.Vars["VSINSTALLDIR"]; !strings.Contains(strings.ToLower(v), "visual studio") {
		t.Errorf("VSINSTALLDIR doesn't look right: %q", v)
	}
	if !slices.ContainsFunc(env1.PathBefore, func(p string) bool {
		return strings.Contains(strings.ToLower(p), `\vc\tools\msvc\`)
	}) {
		t.Errorf("PathBefore lacks the MSVC bin dir: %q", env1.PathBefore)
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
	if len(env1.Vars) != len(env2.Vars) {
		t.Errorf("cold/warm env size mismatch: %d vs %d", len(env1.Vars), len(env2.Vars))
	}
	for k, v := range env1.Vars {
		if env2.Vars[k] != v {
			t.Errorf("warm cache differs for %s: %q vs %q", k, env2.Vars[k], v)
			break
		}
	}
	if !slices.Equal(env1.PathBefore, env2.PathBefore) || !slices.Equal(env1.PathAfter, env2.PathAfter) {
		t.Error("warm cache PATH entries differ from cold")
	}

	t.Logf("cold: %v, warm: %v, env vars: %d", coldDur, warmDur, len(env1.Vars))
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

	if envX64.Vars["VSCMD_ARG_TGT_ARCH"] != "x64" {
		t.Errorf("x64 cache has wrong target arch: %q", envX64.Vars["VSCMD_ARG_TGT_ARCH"])
	}
	if envX86.Vars["VSCMD_ARG_TGT_ARCH"] != "x86" {
		t.Errorf("x86 cache has wrong target arch: %q", envX86.Vars["VSCMD_ARG_TGT_ARCH"])
	}

	// LIB paths should differ between archs
	if envX64.Vars["LIB"] == envX86.Vars["LIB"] {
		t.Errorf("LIB should differ between x64 and x86, both = %q", envX64.Vars["LIB"])
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
