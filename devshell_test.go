package main

import (
	"os"
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
	if len(diff) != 1 || diff["B"] != "2" {
		t.Errorf("expected only B=2 in diff, got %v", diff)
	}
}

func TestDiffEnv_Overrides(t *testing.T) {
	parent := map[string]string{"PATH": "old"}
	full := map[string]string{"PATH": "new"}
	diff := diffEnv(parent, full)
	if diff["PATH"] != "new" {
		t.Errorf("expected PATH=new in diff, got %v", diff)
	}
}

func TestDiffEnv_Identical(t *testing.T) {
	m := map[string]string{"A": "1", "B": "2"}
	diff := diffEnv(m, m)
	if len(diff) != 0 {
		t.Errorf("expected empty diff, got %v", diff)
	}
}

func TestDiffEnv_DoesNotCarryRemovedKeys(t *testing.T) {
	parent := map[string]string{"A": "1", "B": "2"}
	full := map[string]string{"A": "1"}
	diff := diffEnv(parent, full)
	if len(diff) != 0 {
		t.Errorf("removed keys should not appear in diff, got %v", diff)
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
