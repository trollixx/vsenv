package main

import (
	"os"
	"slices"
	"strings"
	"testing"
)

func TestParseSetOutput_Basic(t *testing.T) {
	in := "noise before\r\n" + envSentinel + "\r\nFOO=bar\r\nBAZ=qux\r\n"
	env, err := parseSetOutput(in)
	if err != nil {
		t.Fatal(err)
	}
	if env["FOO"] != "bar" || env["BAZ"] != "qux" {
		t.Errorf("unexpected env: %v", env)
	}
}

func TestParseSetOutput_MissingSentinel(t *testing.T) {
	if _, err := parseSetOutput("FOO=bar\n"); err == nil {
		t.Error("expected error when sentinel is missing")
	}
}

func TestParseSetOutput_ValueContainsEquals(t *testing.T) {
	in := envSentinel + "\nKEY=a=b=c\n"
	env, err := parseSetOutput(in)
	if err != nil {
		t.Fatal(err)
	}
	if env["KEY"] != "a=b=c" {
		t.Errorf("expected 'a=b=c', got %q", env["KEY"])
	}
}

func TestParseSetOutput_EmptyValue(t *testing.T) {
	in := envSentinel + "\nFOO=\nBAR=baz\n"
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
	in := envSentinel + "\nGOOD=ok\nthis-has-no-equals\n=leading-equals\nALSO=fine\n"
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
	in := envSentinel + "\r\nFOO=bar\r\nBAZ=qux\r\n"
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
		"set",
	}
	for _, w := range wants {
		if !strings.Contains(body, w) {
			t.Errorf("temp .bat missing %q\nbody:\n%s", w, body)
		}
	}
}

func TestParentEnvMap_RoundTrip(t *testing.T) {
	m := parentEnvMap()
	if len(m) == 0 {
		t.Fatal("parent env should not be empty")
	}
	for _, e := range []string{"PATH", "Path"} {
		if v, ok := m[e]; ok {
			if !strings.Contains(strings.ToLower(v), `\`) {
				t.Logf("PATH does not contain backslash; on Windows that is unusual: %q", v)
			}
			return
		}
	}
}
