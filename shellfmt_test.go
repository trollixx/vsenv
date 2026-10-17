package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"unicode/utf16"
)

// testDev adds C:\vs before and C:\cmake after the caller's PATH.
func testDev() devEnv {
	return devEnv{
		Vars:       map[string]string{"FOO": "bar"},
		PathBefore: []string{`C:\vs`},
		PathAfter:  []string{`C:\cmake`},
	}
}

func formatTo(t *testing.T, shell string, dev devEnv, curPath string) string {
	t.Helper()
	var b bytes.Buffer
	if err := formatEnv(shell, dev, curPath, &b); err != nil {
		t.Fatalf("formatEnv(%q): %v", shell, err)
	}
	return b.String()
}

func TestFormatEnv_PowerShell(t *testing.T) {
	out := formatTo(t, "powershell", testDev(), `C:\a`)
	want := "$env:FOO = 'bar'\n" +
		"$env:Path = 'C:\\vs;C:\\a;C:\\cmake'\n"
	if out != want {
		t.Errorf("got:\n%q\nwant:\n%q", out, want)
	}
}

func TestFormatEnv_PowerShell_LeavesAppliedPathAlone(t *testing.T) {
	out := formatTo(t, "powershell", testDev(), `C:\vs;C:\a;C:\cmake`)
	if strings.Contains(out, "Path") {
		t.Errorf("expected no PATH changes, got:\n%s", out)
	}
}

func TestFormatEnv_Pwsh_AliasesPowerShell(t *testing.T) {
	a := formatTo(t, "pwsh", testDev(), "")
	b := formatTo(t, "powershell", testDev(), "")
	if a != b {
		t.Errorf("pwsh and powershell should produce identical output, got\n%q\nvs\n%q", a, b)
	}
}

func TestFormatEnv_Cmd(t *testing.T) {
	dev := devEnv{Vars: map[string]string{"FOO": "bar baz"}, PathBefore: []string{`C:\vs`}}
	out := formatTo(t, "cmd", dev, `C:\a`)
	want := "set \"FOO=bar baz\"\nset \"Path=C:\\vs;C:\\a\"\n"
	if out != want {
		t.Errorf("got:\n%q\nwant:\n%q", out, want)
	}
}

func TestFormatEnv_Bash(t *testing.T) {
	out := formatTo(t, "bash", testDev(), `C:\a`)
	want := "export FOO='bar'\n" +
		"export PATH=\"$(cygpath -u -p 'C:\\vs;C:\\a;C:\\cmake' || echo \"$PATH\")\"\n"
	if out != want {
		t.Errorf("got:\n%q\nwant:\n%q", out, want)
	}
}

func TestFormatEnv_Bash_LeavesAppliedPathAlone(t *testing.T) {
	out := formatTo(t, "bash", testDev(), `C:\vs;C:\a;C:\cmake`)
	if strings.Contains(out, "PATH") {
		t.Errorf("expected no PATH changes, got:\n%s", out)
	}
}

func TestFormatEnv_Fish(t *testing.T) {
	dev := devEnv{
		Vars:       map[string]string{"VSINSTALLDIR": `C:\VS\`},
		PathBefore: []string{`C:\vs`},
		PathAfter:  []string{`C:\cmake`},
	}
	out := formatTo(t, "fish", dev, `C:\a`)
	want := "set -gx VSINSTALLDIR 'C:\\\\VS\\\\'\n" +
		"if set -l p (cygpath -u -p 'C:\\\\vs;C:\\\\a;C:\\\\cmake'); set -gx PATH (string split : -- $p); end\n"
	if out != want {
		t.Errorf("got:\n%q\nwant:\n%q", out, want)
	}
}

func TestFormatEnv_Nu_IsJSON(t *testing.T) {
	out := formatTo(t, "nu", testDev(), `C:\a`)
	var got map[string]string
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("nu output is not valid JSON: %v\n%s", err, out)
	}
	if got["Path"] != `C:\vs;C:\a;C:\cmake` || got["FOO"] != "bar" {
		t.Errorf("nu output values mismatch: %v", got)
	}
}

func TestFormatEnv_UnknownShell_ReturnsError(t *testing.T) {
	var b bytes.Buffer
	if err := formatEnv("xonsh", testDev(), "", &b); err == nil {
		t.Error("expected error for unknown shell")
	}
}

func TestFormatEnv_KeysSorted(t *testing.T) {
	dev := devEnv{Vars: map[string]string{"Z": "1", "A": "2", "M": "3"}}
	out := formatTo(t, "bash", dev, "")
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
		"":                "''",
		"foo":             "'foo'",
		"with space":      "'with space'",
		"it's":            "'it''s'",
		"a'b'c":           "'a''b''c'",
		`C:\path\with`:    `'C:\path\with'`,
		`C:\Users\O’Neil`: `'C:\Users\O' + [char]0x2019 + 'Neil'`,
		"‘a‚":             `'' + [char]0x2018 + 'a' + [char]0x201A`,
		"日本":              `'' + (-join [char[]](0x65E5,0x672C))`,
		"😀'":              `'' + (-join [char[]](0xD83D,0xDE00)) + ''''`,
	}
	for in, want := range cases {
		if got := psQuote(in); got != want {
			t.Errorf("psQuote(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPsQuote_RoundTripsInPowerShell(t *testing.T) {
	values := []string{
		"", "it's", `C:\Users\O’Neil\`, "‘a‚b‛ José Олег 日本 😀", "é", "$x `t \"q\"",
		strings.Repeat(`C:\Users\Владимир\Программы\bin;`, 100),
	}
	// The script, and what it prints (each value's UTF-16 code units), are
	// ASCII, so they get through in any console code page.
	var script strings.Builder
	for _, v := range values {
		fmt.Fprintf(&script, "'[' + (([int[]][char[]](%s)) -join ',') + ']'\n", psQuote(v))
	}
	for _, shell := range []string{"powershell", "pwsh"} {
		if _, err := exec.LookPath(shell); err != nil {
			continue
		}
		out, err := exec.Command(shell, "-NoProfile", "-NonInteractive", "-Command", script.String()).Output()
		if err != nil {
			t.Fatalf("%s: %v", shell, err)
		}
		lines := strings.Fields(string(out))
		if len(lines) != len(values) {
			t.Fatalf("%s printed %d values, want %d:\n%s", shell, len(lines), len(values), out)
		}
		for i, v := range values {
			var units []uint16
			if jsonErr := json.Unmarshal([]byte(lines[i]), &units); jsonErr != nil {
				t.Fatalf("%s: %v", shell, jsonErr)
			}
			if got := string(utf16.Decode(units)); got != v {
				t.Errorf("%s: got %q, want %q", shell, got, v)
			}
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

func TestFishQuote(t *testing.T) {
	cases := map[string]string{
		"":        "''",
		"foo":     "'foo'",
		"it's":    `'it\'s'`,
		`C:\dir\`: `'C:\\dir\\'`,
		"$HOME":   "'$HOME'",
	}
	for in, want := range cases {
		if got := fishQuote(in); got != want {
			t.Errorf("fishQuote(%q) = %q, want %q", in, got, want)
		}
	}
}
