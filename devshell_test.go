package main

import (
	"bytes"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode/utf16"
	"unicode/utf8"
)

// envDump returns what the temp batch file prints: noise from VsDevCmd.bat,
// the sentinel line, then env as dumpEnv writes it.
func envDump(t *testing.T, noise string, env ...string) []byte {
	t.Helper()
	data, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	return append([]byte(noise+envSentinel+"\r\n"), data...)
}

func TestParseEnvDump(t *testing.T) {
	env, err := parseEnvDump(envDump(t, "Jos\x82\r\n",
		"FOO=bar", "KEY=a=b=c", "EMPTY=", "=C:=C:\\", "junk", `HOME_DIR=C:\Users\José\Олег 日本 😀`))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"FOO": "bar", "KEY": "a=b=c", "EMPTY": "", "HOME_DIR": `C:\Users\José\Олег 日本 😀`}
	if !maps.Equal(env, want) {
		t.Errorf("env = %v, want %v", env, want)
	}
}

func TestParseEnvDump_OutputAfter(t *testing.T) {
	in := append(envDump(t, "", "FOO=bar"), "warning: GOCOVERDIR not set, no coverage data emitted\r\n"...)
	env, err := parseEnvDump(in)
	if err != nil || env["FOO"] != "bar" {
		t.Errorf("env = %v, err = %v", env, err)
	}
}

func TestParseEnvDump_MissingSentinel(t *testing.T) {
	if _, err := parseEnvDump([]byte(`["FOO=bar"]`)); err == nil {
		t.Error("expected error when sentinel is missing")
	}
}

func TestParseEnvDump_NotJSON(t *testing.T) {
	if _, err := parseEnvDump([]byte(envSentinel + "\r\nFOO=bar\r\n")); err == nil {
		t.Error("expected error for output that isn't JSON")
	}
}

// Values come back whole, unlike from cmd's set, which cuts lines at 8191
// characters.
func TestDumpEnv_RoundTrips(t *testing.T) {
	long := strings.Repeat("José Олег 日本 😀;", 3000)
	t.Setenv("VSENV_TEST_LONG", long)
	var b bytes.Buffer
	if err := dumpEnv(&b); err != nil {
		t.Fatal(err)
	}
	env, err := parseEnvDump(append([]byte(envSentinel+"\r\n"), b.Bytes()...))
	if err != nil {
		t.Fatal(err)
	}
	if env["VSENV_TEST_LONG"] != long {
		t.Errorf("VSENV_TEST_LONG came back %d characters long, want %d", len(env["VSENV_TEST_LONG"]), len(long))
	}
	if !maps.Equal(env, envMap(os.Environ())) {
		t.Error("the dump doesn't match the environment")
	}
}

func TestDiffEnv_Adds(t *testing.T) {
	parent := map[string]string{"A": "1"}
	full := map[string]string{"A": "1", "B": "2"}
	diff := diffEnv(parent, full)
	if len(diff.Vars) != 1 || diff.Vars["B"] != "2" {
		t.Errorf("expected only B=2 in diff, got %v", diff.Vars)
	}
}

func TestDiffEnv_Overrides(t *testing.T) {
	parent := map[string]string{"FOO": "old"}
	full := map[string]string{"FOO": "new"}
	diff := diffEnv(parent, full)
	if diff.Vars["FOO"] != "new" {
		t.Errorf("expected FOO=new in diff, got %v", diff.Vars)
	}
}

func TestDiffEnv_Identical(t *testing.T) {
	m := map[string]string{"A": "1", "Path": `C:\a`}
	diff := diffEnv(m, m)
	if len(diff.Vars) != 0 || len(diff.PathBefore) != 0 || len(diff.PathAfter) != 0 {
		t.Errorf("expected empty diff, got %+v", diff)
	}
}

func TestDiffEnv_DoesNotCarryRemovedKeys(t *testing.T) {
	parent := map[string]string{"A": "1", "B": "2"}
	full := map[string]string{"A": "1"}
	diff := diffEnv(parent, full)
	if len(diff.Vars) != 0 {
		t.Errorf("removed keys should not appear in diff, got %v", diff.Vars)
	}
}

func TestDiffEnv_SplitsPathAroundOriginal(t *testing.T) {
	base := map[string]string{"Path": `C:\a;C:\b;`}
	full := map[string]string{"PATH": `C:\vs1;C:\vs2;C:\a;C:\b;;C:\cmake`}
	diff := diffEnv(base, full)
	if _, ok := diff.Vars["PATH"]; ok {
		t.Error("PATH should not be stored as a plain var")
	}
	if !slices.Equal(diff.PathBefore, []string{`C:\vs1`, `C:\vs2`}) {
		t.Errorf("PathBefore = %q", diff.PathBefore)
	}
	if !slices.Equal(diff.PathAfter, []string{`C:\cmake`}) {
		t.Errorf("PathAfter = %q", diff.PathAfter)
	}
}

func TestDiffEnv_KeepsAddedEntriesAlsoInOriginal(t *testing.T) {
	base := map[string]string{"Path": `C:\a;C:\vs`}
	full := map[string]string{"Path": `C:\vs;C:\a;C:\vs;C:\cmake`}
	diff := diffEnv(base, full)
	if !slices.Equal(diff.PathBefore, []string{`C:\vs`}) || !slices.Equal(diff.PathAfter, []string{`C:\cmake`}) {
		t.Errorf("got before=%q after=%q", diff.PathBefore, diff.PathAfter)
	}
}

func TestDiffEnv_MatchesOriginalPathAtEntryBoundaries(t *testing.T) {
	base := map[string]string{"Path": `C:\a`}
	full := map[string]string{"Path": `C:\ab;C:\a;C:\c`}
	diff := diffEnv(base, full)
	if !slices.Equal(diff.PathBefore, []string{`C:\ab`}) || !slices.Equal(diff.PathAfter, []string{`C:\c`}) {
		t.Errorf("got before=%q after=%q", diff.PathBefore, diff.PathAfter)
	}
}

func TestDiffEnv_OriginalPathNotIntact_NewEntriesPrepended(t *testing.T) {
	base := map[string]string{"Path": `C:\a;C:\b`}
	full := map[string]string{"Path": `C:\vs;C:\b;C:\a;C:\cmake`}
	diff := diffEnv(base, full)
	if !slices.Equal(diff.PathBefore, []string{`C:\vs`, `C:\cmake`}) || len(diff.PathAfter) != 0 {
		t.Errorf("got before=%q after=%q", diff.PathBefore, diff.PathAfter)
	}
}

func TestDiffEnv_NoOriginalPath_AllPrepended(t *testing.T) {
	diff := diffEnv(map[string]string{}, map[string]string{"Path": `C:\vs1;C:\vs2`})
	if !slices.Equal(diff.PathBefore, []string{`C:\vs1`, `C:\vs2`}) || len(diff.PathAfter) != 0 {
		t.Errorf("got before=%q after=%q", diff.PathBefore, diff.PathAfter)
	}
}

func TestDevEnvPath_WrapsCurrent(t *testing.T) {
	dev := devEnv{PathBefore: []string{`C:\vs`}, PathAfter: []string{`C:\cmake`}}
	if got, want := dev.path(`C:\a;C:\b`), `C:\vs;C:\a;C:\b;C:\cmake`; got != want {
		t.Errorf("path() = %q, want %q", got, want)
	}
}

func TestDevEnvPath_MovesEntriesAlreadyPresent(t *testing.T) {
	dev := devEnv{PathBefore: []string{`C:\VS\bin`}, PathAfter: []string{`C:\cmake`}}
	if got, want := dev.path(`C:\a;c:\vs\bin\;C:\cmake;C:\b`), `C:\VS\bin;C:\a;C:\b;C:\cmake`; got != want {
		t.Errorf("path() = %q, want %q", got, want)
	}
}

func TestDevEnvPath_MatchesUncleanEntries(t *testing.T) {
	dev := devEnv{PathBefore: []string{`C:\Kits\bin\\x64`}}
	if got, want := dev.path(`C:\Kits\bin\x64;C:\a`), `C:\Kits\bin\\x64;C:\a`; got != want {
		t.Errorf("path() = %q, want %q", got, want)
	}
}

func TestDevEnvPath_Idempotent(t *testing.T) {
	dev := devEnv{PathBefore: []string{`C:\vs`, `C:\kits`}, PathAfter: []string{`C:\cmake`}}
	once := dev.path(`C:\a;C:\b`)
	if twice := dev.path(once); twice != once {
		t.Errorf("applying twice changed PATH:\n%q\n%q", once, twice)
	}
}

func TestDevEnvPath_SwitchingArchPutsItsToolsFirst(t *testing.T) {
	x64 := devEnv{PathBefore: []string{`C:\msvc\Hostx64\x64`, `C:\kits`}}
	x86 := devEnv{PathBefore: []string{`C:\msvc\Hostx64\x86`, `C:\msvc\Hostx64\x64`, `C:\kits`}}
	p := x64.path(`C:\a`)
	p = x86.path(p)
	if !strings.HasPrefix(p, `C:\msvc\Hostx64\x86;`) {
		t.Errorf("after x86: %q", p)
	}
	p = x64.path(p)
	if want := `C:\msvc\Hostx64\x64;C:\kits;C:\msvc\Hostx64\x86;C:\a`; p != want {
		t.Errorf("after x64 again: %q, want %q", p, want)
	}
}

func TestDiffEnv_ListsKeepOriginalEntries(t *testing.T) {
	base := map[string]string{"INCLUDE": `C:\mine`}
	full := map[string]string{"INCLUDE": `C:\vs\include;C:\mine`, "LIB": `C:\vs\lib`, "FOO": "1"}
	diff := diffEnv(base, full)
	if len(diff.Vars) != 1 || diff.Vars["FOO"] != "1" {
		t.Errorf("expected only FOO in Vars, got %v", diff.Vars)
	}
	want := map[string]listAdded{
		"INCLUDE": {Before: []string{`C:\vs\include`}},
		"LIB":     {Before: []string{`C:\vs\lib`}},
	}
	if len(diff.Lists) != len(want) {
		t.Fatalf("Lists = %v, want %v", diff.Lists, want)
	}
	for k, w := range want {
		if got := diff.Lists[k]; !slices.Equal(got.Before, w.Before) || !slices.Equal(got.After, w.After) {
			t.Errorf("Lists[%s] = %+v, want %+v", k, got, w)
		}
	}
}

func TestDevEnvVars_PutsListEntriesAroundCallers(t *testing.T) {
	dev := devEnv{
		Vars:  map[string]string{"FOO": "1"},
		Lists: map[string]listAdded{"LIB": {Before: []string{`C:\vs\lib\x64`}, After: []string{`C:\after`}}},
	}
	getenv := func(k string) string {
		if k == "LIB" {
			return `C:\mine;C:\vs\lib\x64`
		}
		return ""
	}
	vars := dev.vars(getenv)
	if vars["FOO"] != "1" || vars["LIB"] != `C:\vs\lib\x64;C:\mine;C:\after` {
		t.Errorf("vars = %v", vars)
	}
	if again := dev.vars(func(string) string { return vars["LIB"] }); again["LIB"] != vars["LIB"] {
		t.Errorf("applying twice changed LIB: %q", again["LIB"])
	}
}

func TestDiffEnv_DropsCmdPrompt(t *testing.T) {
	diff := diffEnv(map[string]string{}, map[string]string{"PROMPT": "$P$G", "X": "1"})
	if len(diff.Vars) != 1 || diff.Vars["X"] != "1" {
		t.Errorf("expected only X in diff, got %v", diff.Vars)
	}
}

func TestDiffEnv_DropsPreinitVars(t *testing.T) {
	full := map[string]string{"__VSCMD_PREINIT_PATH": `C:\a`, "__VSCMD_PREINIT_INCLUDE": `C:\inc`, "X": "1"}
	diff := diffEnv(map[string]string{}, full)
	if len(diff.Vars) != 1 || diff.Vars["X"] != "1" {
		t.Errorf("expected only X in diff, got %v", diff.Vars)
	}
}

func TestCheckCompiler(t *testing.T) {
	tools := filepath.Join(t.TempDir(), "VS[2026]")
	cl := filepath.Join(tools, "bin", "HostX64", "x64", "cl.exe")
	if err := os.MkdirAll(filepath.Dir(cl), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cl, []byte("MZ"), 0o644); err != nil {
		t.Fatal(err)
	}
	env := func(tgt string) map[string]string {
		return map[string]string{
			"VCToolsInstallDir":   tools + `\`,
			"VSCMD_ARG_HOST_ARCH": "x64",
			"VSCMD_ARG_TGT_ARCH":  tgt,
		}
	}
	if err := checkCompiler(env("x64")); err != nil {
		t.Errorf("x64: %v", err)
	}
	if err := checkCompiler(env("arm64")); err == nil {
		t.Error("arm64: expected an error for the missing cross compiler")
	}
	// A toolset without compilers for this host, like v142 on arm64.
	arm64Host := env("arm64")
	arm64Host["VSCMD_ARG_HOST_ARCH"] = "arm64"
	if err := checkCompiler(arm64Host); err == nil {
		t.Error("arm64 host: expected an error for the missing HostARM64")
	}
	if err := checkCompiler(map[string]string{"VSCMD_ARG_TGT_ARCH": "arm64"}); err != nil {
		t.Errorf("no C++ toolset: %v", err)
	}
	// The VS 2015 toolset (-vcvars_ver=14.0) keeps its compilers in VC\bin.
	v140 := env("x64")
	v140["VCToolsInstallDir"] = t.TempDir() + `\`
	if err := checkCompiler(v140); err != nil {
		t.Errorf("v140 layout: %v", err)
	}
}

// fakeInstall returns an install whose VsDevCmd.bat runs script.
func fakeInstall(t *testing.T, script ...string) *Install {
	t.Helper()
	dir := t.TempDir()
	tools := filepath.Join(dir, "Common7", "Tools")
	if err := os.MkdirAll(tools, 0o755); err != nil {
		t.Fatal(err)
	}
	bat := "@echo off\r\n" + strings.Join(script, "\r\n") + "\r\n"
	if err := os.WriteFile(filepath.Join(tools, "VsDevCmd.bat"), []byte(bat), 0o644); err != nil {
		t.Fatal(err)
	}
	return &Install{InstallationPath: dir}
}

func TestCaptureDevEnv_LongPath(t *testing.T) {
	install := fakeInstall(t, `set "PATH_START=%PATH:~0,3%"`)
	minimal := minimalPath(os.Environ())
	for _, tc := range []struct {
		path, start string
	}{
		{strings.Repeat("ж", maxCmdPath), "жжж"},
		{strings.Repeat("ж", maxCmdPath) + ";", minimal[:3]},
	} {
		diff, err := captureDevEnv(t.Context(), install, "x64", "x64", "", withPath(os.Environ(), tc.path))
		if err != nil {
			t.Fatal(err)
		}
		if got := diff.Vars["PATH_START"]; got != tc.start {
			t.Errorf("a PATH of %d characters: VsDevCmd.bat ran with one starting %q, want %q",
				len(utf16.Encode([]rune(tc.path))), got, tc.start)
		}
	}
}

func TestCaptureDevEnv_RetriesWithMinimalPath(t *testing.T) {
	install := fakeInstall(t, `if not "%PATH:FAILS=%"=="%PATH%" exit /b 1`, `set "PATH=C:\vs;%PATH%"`)
	diff, err := captureDevEnv(t.Context(), install, "x64", "x64", "", withPath(os.Environ(), `C:\FAILS`))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(diff.PathBefore, []string{`C:\vs`}) || diff.PathAfter != nil {
		t.Errorf("PATH entries: %q, %q", diff.PathBefore, diff.PathAfter)
	}
}

func TestCaptureDevEnv_RetryFails(t *testing.T) {
	install := fakeInstall(t, "echo PATH is %PATH%", "exit /b 1")
	_, err := captureDevEnv(t.Context(), install, "x64", "x64", "", withPath(os.Environ(), `C:\first`))
	if err == nil || !strings.Contains(err.Error(), `PATH is C:\first`) {
		t.Errorf("want the error with the original PATH, got %v", err)
	}
}

func TestDevEnvPath_EmptyCurrent(t *testing.T) {
	dev := devEnv{PathBefore: []string{`C:\vs`}, PathAfter: []string{`C:\cmake`}}
	if got, want := dev.path(""), `C:\vs;C:\cmake`; got != want {
		t.Errorf("path() = %q, want %q", got, want)
	}
}

func TestWriteTempBat(t *testing.T) {
	bat, err := writeTempBat()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(bat)

	if !strings.HasSuffix(bat, ".bat") {
		t.Errorf("expected .bat suffix, got %q", bat)
	}

	data, err := os.ReadFile(bat)
	if err != nil {
		t.Fatal(err)
	}
	body := string(data)
	wants := []string{
		"@echo off",
		`call "%VSENV_VSDEVCMD%" %VSENV_VSDEVCMD_ARGS%`,
		envSentinel,
		`"%VSENV_SELF%" __dump-env`,
	}
	for _, w := range wants {
		if !strings.Contains(body, w) {
			t.Errorf("temp .bat missing %q\nbody:\n%s", w, body)
		}
	}
	// cmd reads it in the console code page, so it can't hold a path itself.
	if strings.ContainsFunc(body, func(r rune) bool { return r >= utf8.RuneSelf }) {
		t.Errorf("temp .bat isn't ASCII:\n%s", body)
	}
}

func TestEnvMap(t *testing.T) {
	m := envMap([]string{"A=1", "B=x=y", "=C:=C:\\", "junk"})
	if len(m) != 2 || m["A"] != "1" || m["B"] != "x=y" {
		t.Errorf("envMap = %v", m)
	}
}

func TestBaseEnv_IsTheUsersDefault(t *testing.T) {
	t.Setenv("VSENV_TEST_PROCESS_ONLY", "1")
	m := envMap(baseEnv())
	if lookupFold(m, "SystemRoot") == "" || lookupFold(m, "PATH") == "" {
		t.Errorf("base env lacks SystemRoot or PATH: %v", m)
	}
	if _, ok := m["VSENV_TEST_PROCESS_ONLY"]; ok {
		t.Error("base env should not inherit this process's variables")
	}
}
