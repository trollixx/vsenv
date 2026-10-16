package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
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
