package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func formatTo(t *testing.T, shell string, env map[string]string) string {
	t.Helper()
	var b bytes.Buffer
	if err := formatEnv(shell, env, &b); err != nil {
		t.Fatalf("formatEnv(%q): %v", shell, err)
	}
	return b.String()
}

func TestFormatEnv_PowerShell(t *testing.T) {
	out := formatTo(t, "powershell", map[string]string{"PATH": `C:\foo;C:\bar`, "FOO": "bar"})
	want := "$env:FOO = 'bar'\n$env:PATH = 'C:\\foo;C:\\bar'\n"
	if out != want {
		t.Errorf("got:\n%q\nwant:\n%q", out, want)
	}
}

func TestFormatEnv_Pwsh_AliasesPowerShell(t *testing.T) {
	a := formatTo(t, "pwsh", map[string]string{"X": "1"})
	b := formatTo(t, "powershell", map[string]string{"X": "1"})
	if a != b {
		t.Errorf("pwsh and powershell should produce identical output, got\n%q\nvs\n%q", a, b)
	}
}

func TestFormatEnv_Cmd(t *testing.T) {
	out := formatTo(t, "cmd", map[string]string{"FOO": "bar baz"})
	if out != "set FOO=bar baz\n" {
		t.Errorf("unexpected cmd output: %q", out)
	}
}

func TestFormatEnv_Bash(t *testing.T) {
	out := formatTo(t, "bash", map[string]string{"FOO": "bar"})
	if out != "export FOO='bar'\n" {
		t.Errorf("unexpected bash output: %q", out)
	}
}

func TestFormatEnv_Fish(t *testing.T) {
	out := formatTo(t, "fish", map[string]string{"FOO": "bar"})
	if out != "set -gx FOO 'bar'\n" {
		t.Errorf("unexpected fish output: %q", out)
	}
}

func TestFormatEnv_Nu_IsJSON(t *testing.T) {
	out := formatTo(t, "nu", map[string]string{"PATH": `C:\foo`, "BAR": "baz"})
	var got map[string]string
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("nu output is not valid JSON: %v\n%s", err, out)
	}
	if got["PATH"] != `C:\foo` || got["BAR"] != "baz" {
		t.Errorf("nu output values mismatch: %v", got)
	}
}

func TestFormatEnv_UnknownShell_ReturnsError(t *testing.T) {
	var b bytes.Buffer
	if err := formatEnv("xonsh", map[string]string{"X": "1"}, &b); err == nil {
		t.Error("expected error for unknown shell")
	}
}

func TestFormatEnv_KeysSorted(t *testing.T) {
	in := map[string]string{"Z": "1", "A": "2", "M": "3"}
	out := formatTo(t, "bash", in)
	expectedOrder := []string{"A", "M", "Z"}
	for i, name := range expectedOrder {
		idx := strings.Index(out, "export "+name+"=")
		if idx < 0 {
			t.Fatalf("missing %s", name)
		}
		for _, prev := range expectedOrder[:i] {
			pi := strings.Index(out, "export "+prev+"=")
			if pi >= idx {
				t.Errorf("%s should appear before %s", prev, name)
			}
		}
	}
}

func TestPsQuote(t *testing.T) {
	cases := map[string]string{
		"":             "''",
		"foo":          "'foo'",
		"with space":   "'with space'",
		"it's":         "'it''s'",
		"a'b'c":        "'a''b''c'",
		`C:\path\with`: `'C:\path\with'`,
	}
	for in, want := range cases {
		if got := psQuote(in); got != want {
			t.Errorf("psQuote(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestShQuote(t *testing.T) {
	cases := map[string]string{
		"":      "''",
		"foo":   "'foo'",
		"a b":   "'a b'",
		"it's":  `'it'\''s'`,
		"$HOME": "'$HOME'",
	}
	for in, want := range cases {
		if got := shQuote(in); got != want {
			t.Errorf("shQuote(%q) = %q, want %q", in, got, want)
		}
	}
}
