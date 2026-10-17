package main

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode/utf16"
)

func utf16LE(s string) []byte {
	var out []byte
	for _, u := range utf16.Encode([]rune(s)) {
		out = binary.LittleEndian.AppendUint16(out, u)
	}
	return out
}

// setOutput returns what the temp batch file prints: noise from VsDevCmd.bat,
// the sentinel line, then env in UTF-16 as cmd /u prints it.
func setOutput(noise, env string) []byte {
	return append([]byte(noise+envSentinel+"\r\n"), utf16LE(env)...)
}

func TestParseSetOutput_Basic(t *testing.T) {
	in := setOutput("noise before\r\n", "FOO=bar\r\nBAZ=qux\r\n")
	env, err := parseSetOutput(in)
	if err != nil {
		t.Fatal(err)
	}
	if env["FOO"] != "bar" || env["BAZ"] != "qux" {
		t.Errorf("unexpected env: %v", env)
	}
}

func TestParseSetOutput_NonASCII(t *testing.T) {
	want := `C:\Users\José\Олег 日本 😀`
	env, err := parseSetOutput(setOutput("Jos\x82\r\n", "HOME_DIR="+want+"\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	if env["HOME_DIR"] != want {
		t.Errorf("HOME_DIR = %q, want %q", env["HOME_DIR"], want)
	}
}

func TestParseSetOutput_MissingSentinel(t *testing.T) {
	if _, err := parseSetOutput(utf16LE("FOO=bar\r\n")); err == nil {
		t.Error("expected error when sentinel is missing")
	}
}

func TestParseSetOutput_NotUTF16(t *testing.T) {
	if _, err := parseSetOutput([]byte(envSentinel + "\r\nFOO=bar\r\n")); err == nil {
		t.Error("expected error for output of odd length")
	}
}

func TestParseSetOutput_ValueContainsEquals(t *testing.T) {
	in := setOutput("", "KEY=a=b=c\n")
	env, err := parseSetOutput(in)
	if err != nil {
		t.Fatal(err)
	}
	if env["KEY"] != "a=b=c" {
		t.Errorf("expected 'a=b=c', got %q", env["KEY"])
	}
}

func TestParseSetOutput_EmptyValue(t *testing.T) {
	in := setOutput("", "FOO=\nBAR=baz\n")
	env, err := parseSetOutput(in)
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := env["FOO"]; !ok || v != "" {
		t.Errorf("expected empty FOO, got %q (ok=%v)", v, ok)
	}
	if env["BAR"] != "baz" {
		t.Errorf("BAR=%q want %q", env["BAR"], "baz")
	}
}

func TestParseSetOutput_SkipsMalformedLines(t *testing.T) {
	in := setOutput("", "GOOD=ok\nthis-has-no-equals\n=leading-equals\nALSO=fine\n")
	env, err := parseSetOutput(in)
	if err != nil {
		t.Fatal(err)
	}
	if env["GOOD"] != "ok" || env["ALSO"] != "fine" {
		t.Errorf("expected GOOD and ALSO to be parsed: %v", env)
	}
	if _, ok := env[""]; ok {
		t.Error("should skip leading-equals line")
	}
	if len(env) != 2 {
		t.Errorf("expected 2 entries, got %d: %v", len(env), env)
	}
}

func TestParseSetOutput_HandlesCRLF(t *testing.T) {
	in := setOutput("", "FOO=bar\r\nBAZ=qux\r\n")
	env, _ := parseSetOutput(in)
	if env["FOO"] != "bar" {
		t.Errorf("CRLF not stripped: %q", env["FOO"])
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
	tools := t.TempDir()
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

func TestDevEnvPath_EmptyCurrent(t *testing.T) {
	dev := devEnv{PathBefore: []string{`C:\vs`}, PathAfter: []string{`C:\cmake`}}
	if got, want := dev.path(""), `C:\vs;C:\cmake`; got != want {
		t.Errorf("path() = %q, want %q", got, want)
	}
}

func TestWriteTempBat(t *testing.T) {
	bat, err := writeTempBat(`C:\fake path\VsDevCmd.bat`, []string{"-no_logo", "-arch=x64"})
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
		`call "C:\fake path\VsDevCmd.bat" -no_logo -arch=x64`,
		envSentinel,
		`"%SystemRoot%\System32\cmd.exe" /d /u /c set`,
	}
	for _, w := range wants {
		if !strings.Contains(body, w) {
			t.Errorf("temp .bat missing %q\nbody:\n%s", w, body)
		}
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
