package main

// End-to-end tests: each shell loads vsenv's output the way the README says
// to, then builds with cl, or starts a program that dumps the environment it
// gets. A shell that isn't installed is skipped, unless VSENV_TEST_REQUIRE_ALL
// is set, as in CI.

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
	"unicode/utf16"

	"golang.org/x/sys/windows"
)

const (
	// dumpEnvVar makes the test binary write its environment to the file it
	// names, as JSON, and exit.
	dumpEnvVar = "VSENV_TEST_DUMP_ENV"
	// dumperVar holds the test binary's path, for the shells to run.
	dumperVar = "VSENV_TEST_DUMPER"
)

func TestMain(m *testing.M) {
	if out := os.Getenv(dumpEnvVar); out != "" {
		data, err := json.Marshal(envMap(os.Environ()))
		if err == nil {
			err = os.WriteFile(out, data, 0o600)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

type testShell struct {
	name string
	exe  string
	args []string // before the script's name
	ext  string
	// path goes before PATH, for shells that need their own tools there.
	path string
	// env is added to the shell's environment.
	env []string
	// profile loads the environment (twice, then for x86), builds arch.c after
	// each architecture, and, if child is set, starts vsenv shell for x64
	// with child as its input, which builds arch.c too. A profile or AutoRun
	// of the user's that loads vsenv can make that pass regardless; CI's has
	// none.
	profile string
	child   string
	// quoting loads the environment and runs the dumper.
	quoting string
	// codePage, if set, runs the shell in a console of its own with that code
	// page. GitHub Actions runs everything at 65001 (UTF-8).
	codePage uint32
	// lossy is set for cmd, which reads vsenv's output in the console code
	// page, so values can only have the characters that has.
	lossy bool
	// msys is set for shells on the MSYS2 runtime, which cleans PATH entries.
	msys bool
}

const archC = `#include <stdio.h>

int main(void) {
#if defined(_M_X64)
    puts("x64");
#elif defined(_M_IX86)
    puts("x86");
#elif defined(_M_ARM64)
    puts("arm64");
#endif
    return 0;
}
`

const psProfile = `$ErrorActionPreference = 'Stop'
function Build {
    cl -nologo arch.c | Out-Null
    if ($LASTEXITCODE) { throw 'cl failed' }
    'arch=' + (.\arch.exe)
}
vsenv env --shell powershell | Out-String | Invoke-Expression
Build
$first = $env:Path
vsenv env --shell powershell | Out-String | Invoke-Expression
'same=' + [int]($env:Path -ceq $first)
vsenv env --shell powershell --arch x86 | Out-String | Invoke-Expression
Build
Get-Content child.ps1 | vsenv shell --arch x64
`

const psQuoting = `vsenv env --shell powershell | Out-String | Invoke-Expression
& $env:VSENV_TEST_DUMPER
`

const cmdProfile = `@echo off
for /f "delims=" %%i in ('vsenv env --shell cmd') do @%%i
call :build || exit /b 1
set "first=%Path%"
for /f "delims=" %%i in ('vsenv env --shell cmd') do @%%i
rem Expanded later, so the line stays under cmd's 8191 characters.
setlocal EnableDelayedExpansion
if "!Path!"=="!first!" (echo same=1) else (echo same=0)
endlocal
for /f "delims=" %%i in ('vsenv env --shell cmd --arch x86') do @%%i
call :build || exit /b 1
vsenv shell --arch x64 < child.bat
exit /b
:build
cl -nologo arch.c >nul || exit /b 1
for /f %%a in ('.\arch.exe') do echo arch=%%a
exit /b
`

const cmdQuoting = `@echo off
for /f "delims=" %%i in ('vsenv env --shell cmd') do @%%i
"%VSENV_TEST_DUMPER%"
`

const nuProfile = `def build [] {
    let r = (^cl -nologo arch.c | complete)
    if $r.exit_code != 0 { error make {msg: $r.stdout} }
    print $"arch=(^./arch.exe | str trim)"
}
load-env (vsenv env --shell nu | from json)
build
let first = $env.Path
load-env (vsenv env --shell nu | from json)
print $"same=(if $env.Path == $first { 1 } else { 0 })"
load-env (vsenv env --shell nu --arch x86 | from json)
build
`

const nuQuoting = `load-env (vsenv env --shell nu | from json)
run-external $env.VSENV_TEST_DUMPER
`

const bashProfile = `set -e
build() {
    cl -nologo arch.c >/dev/null
    echo "arch=$(./arch.exe)"
}
eval "$(vsenv env --shell bash)"
build
first=$PATH
eval "$(vsenv env --shell bash)"
if [ "$PATH" = "$first" ]; then echo same=1; else echo same=0; fi
eval "$(vsenv env --shell bash --arch x86)"
build
vsenv shell --arch x64 < child.sh
`

const bashQuoting = `eval "$(vsenv env --shell bash)"
"$(cygpath -u "$VSENV_TEST_DUMPER")"
`

const fishProfile = `function build
    cl -nologo arch.c >/dev/null; or exit 1
    echo arch=(./arch.exe)
end
vsenv env --shell fish | source
build
set -l first (string join : $PATH)
vsenv env --shell fish | source
if test (string join : $PATH) = "$first"; echo same=1; else; echo same=0; end
vsenv env --shell fish --arch x86 | source
build
vsenv shell --arch x64 < child.fish
`

const fishQuoting = `vsenv env --shell fish | source
set -l dump (cygpath -u $VSENV_TEST_DUMPER)
$dump
`

// testShells returns the shells to test, failing for one that's missing if
// VSENV_TEST_REQUIRE_ALL is set.
func testShells(t *testing.T) []testShell {
	t.Helper()
	system32 := filepath.Join(os.Getenv("SystemRoot"), "System32")
	ps := func(name, exe string) testShell {
		return testShell{
			name: name, exe: exe, ext: ".ps1",
			args:    []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File"},
			profile: psProfile, child: "cl -nologo arch.c | Out-Null\n'child=' + (.\\arch.exe)\n", quoting: psQuoting,
		}
	}
	cmd := testShell{
		name: "cmd", exe: filepath.Join(system32, "cmd.exe"), args: []string{"/d", "/c"}, ext: ".bat",
		profile: cmdProfile, quoting: cmdQuoting, lossy: true,
		child: "cl -nologo arch.c >nul\nfor /f %a in ('.\\arch.exe') do @echo child=%a\nexit\n",
	}
	bash := func(name, exe, path string) testShell {
		return testShell{
			name: name, exe: exe, ext: ".sh", path: path, msys: true,
			profile: bashProfile, quoting: bashQuoting,
			child: "cl -nologo arch.c >/dev/null && echo \"child=$(./arch.exe)\"\n",
		}
	}
	// A legacy console code page, as most Windows installs have.
	cp437 := func(s testShell) testShell {
		s.name += "-cp437"
		s.codePage = 437
		return s
	}
	var msysBin, msysBash, msysFish string
	if msys2 := findMSYS2(); msys2 != "" {
		msysBin = filepath.Join(msys2, "usr", "bin")
		msysBash, msysFish = filepath.Join(msysBin, "bash.exe"), filepath.Join(msysBin, "fish.exe")
	}
	powershell := ps("powershell", filepath.Join(system32, "WindowsPowerShell", "v1.0", "powershell.exe"))
	pwsh := ps("pwsh", lookPathOrEmpty("pwsh"))
	all := []testShell{
		powershell, cp437(powershell), pwsh, cp437(pwsh), cmd, cp437(cmd),
		{
			name: "nu", exe: lookPathOrEmpty("nu"), args: []string{"-n"}, ext: ".nu",
			profile: nuProfile, quoting: nuQuoting,
		},
		bash("git-bash", findGitBash(), ""),
		bash("msys2-bash", msysBash, msysBin),
		{
			// Without the user's config, in the script and in vsenv shell's child.
			name: "msys2-fish", exe: msysFish, args: []string{"--no-config"}, ext: ".fish", path: msysBin, msys: true,
			env:     []string{"XDG_CONFIG_HOME=" + t.TempDir()},
			profile: fishProfile, quoting: fishQuoting,
			child: "cl -nologo arch.c >/dev/null; and echo child=(./arch.exe)\n",
		},
	}
	var found []testShell
	for _, s := range all {
		if s.exe != "" {
			if _, err := os.Stat(s.exe); err == nil {
				found = append(found, s)
				continue
			}
		}
		if os.Getenv(requireAllVar) != "" {
			t.Errorf("%s not found", s.name)
		} else {
			t.Logf("%s not found; skipping it", s.name)
		}
	}
	return found
}

func lookPathOrEmpty(name string) string {
	p, _ := exec.LookPath(name)
	return p
}

// findGitBash returns Git for Windows' bash, which sets up Git Bash's PATH.
func findGitBash() string {
	out, err := exec.Command("git", "--exec-path").Output()
	if err != nil {
		return ""
	}
	return filepath.Join(strings.TrimSpace(string(out)), "..", "..", "..", "bin", "bash.exe")
}

func findMSYS2() string {
	for _, root := range []string{
		os.Getenv("VSENV_TEST_MSYS2"),
		`C:\msys64`,
		filepath.Join(os.Getenv("USERPROFILE"), "scoop", "apps", "msys2", "current"),
	} {
		if root == "" {
			continue
		}
		if _, err := os.Stat(filepath.Join(root, "usr", "bin", "cygpath.exe")); err == nil {
			return root
		}
	}
	return ""
}

func buildVsenv(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	out, err := exec.Command("go", "build", "-o", filepath.Join(dir, "vsenv.exe"), ".").CombinedOutput()
	if err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	return dir
}

// runShell runs script in shell s, in a new directory holding the given
// files, and returns its output.
func runShell(t *testing.T, s testShell, script string, files map[string]string, env []string) string {
	t.Helper()
	dir := t.TempDir()
	files["script"+s.ext] = script
	for name, content := range files {
		if s.ext == ".bat" {
			content = strings.ReplaceAll(content, "\n", "\r\n")
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	args := append(slices.Clone(s.args), filepath.Join(dir, "script"+s.ext))
	cmd := exec.CommandContext(ctx, s.exe, args...)
	if s.codePage != 0 {
		// cmd sets the code page of a new, hidden console, then starts the shell.
		line := fmt.Sprintf(`chcp %d >nul && "%s" "%s"`, s.codePage, s.exe, strings.Join(args, `" "`))
		cmd = exec.CommandContext(ctx, os.Getenv("ComSpec"))
		cmd.SysProcAttr = &syscall.SysProcAttr{
			CmdLine:       `cmd.exe /d /s /c "` + line + `"`,
			CreationFlags: windows.CREATE_NEW_CONSOLE,
			HideWindow:    true,
		}
	}
	cmd.Dir = dir
	cmd.Env = env
	// Not to wait on a child left holding the output, if the shell is killed.
	cmd.WaitDelay = 10 * time.Second
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("%s: %v\nstdout:\n%s\nstderr:\n%s", s.name, err, stdout.String(), stderr.String())
	}
	t.Logf("stderr:\n%s", stderr.String())
	return stdout.String()
}

// shellEnv returns the test's environment for s, with binDir and s.path put
// before PATH, and LOCALAPPDATA replaced.
func shellEnv(s testShell, localAppData, binDir string) []string {
	env := slices.DeleteFunc(os.Environ(), func(kv string) bool {
		k, _, _ := strings.Cut(kv, "=")
		return strings.EqualFold(k, "PATH") || strings.EqualFold(k, "LOCALAPPDATA")
	})
	path := slices.DeleteFunc([]string{binDir, s.path, os.Getenv("PATH")}, func(p string) bool { return p == "" })
	// Spelled as Windows does: nushell started with PATH has both PATH and
	// Path, and passes either on.
	env = append(env, "Path="+strings.Join(path, ";"), "LOCALAPPDATA="+localAppData)
	return append(env, s.env...)
}

func TestShells(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping shell tests in short mode")
	}
	shells := testShells(t)
	binDir := buildVsenv(t)

	t.Run("profile", func(t *testing.T) {
		requireVS(t)
		// One cache for all shells, so only the first one runs VsDevCmd.bat.
		localAppData := t.TempDir()
		for _, s := range shells {
			t.Run(s.name, func(t *testing.T) {
				files := map[string]string{"arch.c": archC}
				// The first load is for this machine's architecture, by default.
				want := []string{"arch=" + nativeArch(), "same=1", "arch=x86"}
				if s.child != "" {
					files["child"+s.ext] = s.child
					want = append(want, "child=x64")
				}
				out := runShell(t, s, s.profile, files, shellEnv(s, localAppData, binDir))
				if got := resultLines(out); !slices.Equal(got, want) {
					t.Errorf("got %q, want %q\noutput:\n%s", got, want, out)
				}
			})
		}
	})

	t.Run("quoting", func(t *testing.T) {
		localAppData := t.TempDir()
		dev := seedQuotingCache(t, localAppData)
		// The shells have an INCLUDE of their own, for the VS entries to go around.
		const include = `C:\mine\include`
		applied := devEnv{
			Vars: dev.vars(func(k string) string {
				if k == "INCLUDE" {
					return include
				}
				return ""
			}),
			PathBefore: dev.PathBefore,
			PathAfter:  dev.PathAfter,
		}
		dumper, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range shells {
			t.Run(s.name, func(t *testing.T) {
				dump := filepath.Join(t.TempDir(), "env.json")
				env := append(shellEnv(s, localAppData, binDir),
					dumperVar+"="+dumper, dumpEnvVar+"="+dump, "INCLUDE="+include)
				runShell(t, s, s.quoting, map[string]string{}, env)
				want := applied
				if s.lossy {
					want = throughCodePage(t, applied, s.codePage)
				}
				checkDump(t, dump, want, !s.msys)
			})
		}
	})
}

// resultLines returns the arch=, same= and child= lines of a profile script's
// output.
func resultLines(out string) []string {
	var got []string
	for line := range strings.SplitSeq(out, "\n") {
		line = strings.TrimSpace(line)
		if k, _, ok := strings.Cut(line, "="); ok && slices.Contains([]string{"arch", "same", "child"}, k) {
			got = append(got, line)
		}
	}
	return got
}

// seedQuotingCache makes vsenv, with its default flags, find a fake install
// whose environment has values that are hard to quote.
func seedQuotingCache(t *testing.T, localAppData string) devEnv {
	t.Helper()
	t.Setenv("LOCALAPPDATA", localAppData)
	install := Install{InstanceID: "vsenv-test-quoting", InstallationPath: t.TempDir(), InstallationVersion: "1.0"}
	build := filepath.Join(install.InstallationPath, "VC", "Auxiliary", "Build")
	if err := os.MkdirAll(build, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeFile(filepath.Join(build, "Microsoft.VCToolsVersion.default.txt"), "1"); err != nil {
		t.Fatal(err)
	}
	seedInstalls(t, []Install{install})

	dev := devEnv{
		Vars: map[string]string{
			"VSENV_Q_BACKSLASH":        `C:\VS\`,
			"VSENV_Q_DOUBLE_BACKSLASH": `C:\VS\\`,
			"VSENV_Q_UNC":              `\\server\share\dir\`,
			"VSENV_Q_QUOTES":           `it's "quoted" O’Neil ‘a‚b‛`,
			"VSENV_Q_BACKSLASH_QUOTES": `a\'b\\'c\"d`,
			"VSENV_Q_DOLLAR":           `$HOME $env:PATH ${x} $(x) (x)`,
			"VSENV_Q_PERCENT":          `%PATH% !PATH! ^ %%`,
			"VSENV_Q_META":             `a;b|c&d<e>f#g*?[a]~{a,b}`,
			"VSENV_Q_BACKTICK":         "a`b`$c",
			"VSENV_Q_SPACES":           "  lead  and trail  ",
			"VSENV_Q_TAB":              "a\tb",
			"VSENV_Q_NON_ASCII":        "José Олег 日本 😀",
			"VSENV_Q_ESCAPES":          `\n\t\e\x41\u0041`,
			// fish treats variables named *PATH as lists split at colons.
			"VSENV_Q_LIBPATH": `C:\a;C:\b\;D:\c (x86)`,
		},
		PathBefore: []string{
			`C:\VS\bin\Host x64\`, `C:\it's\here`, `C:\O’Neil (x86)\bin`, `C:\Users\José\bin`, `C:\a\\b`, `C:\100%\x`,
		},
		PathAfter: []string{`C:\cmake (x86)\bin\`, `C:\$HOME\bin`},
		Lists: map[string]listAdded{
			"INCLUDE": {Before: []string{`C:\VS\include`, `C:\José's (x86)\inc`}, After: []string{`C:\after\`}},
		},
	}
	path, err := envCachePath(&install, nativeArch(), nativeArch(), "")
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(envCache{
		Schema:     cacheSchemaVersion,
		InstanceID: install.InstanceID,
		StateTime:  instanceStateTime(install.InstanceID),
		Env:        dev,
	})
	if err != nil {
		t.Fatal(err)
	}
	if writeErr := os.WriteFile(path, data, 0o600); writeErr != nil {
		t.Fatal(writeErr)
	}
	return dev
}

// throughCodePage returns dev with the characters code page cp lacks replaced,
// as they are when cmd reads them. cp 0 means this console's.
func throughCodePage(t *testing.T, dev devEnv, cp uint32) devEnv {
	t.Helper()
	if cp == 0 {
		var err error
		if cp, err = windows.GetConsoleOutputCP(); err != nil {
			return dev
		}
	}
	if cp == cpUTF8 {
		return dev
	}
	conv := func(s string) string {
		b := encodeCodePage(s, cp)
		if len(b) == 0 {
			return s
		}
		n, decodeErr := windows.MultiByteToWideChar(cp, 0, &b[0], int32(len(b)), nil, 0)
		if decodeErr != nil {
			t.Fatal(decodeErr)
		}
		u := make([]uint16, n)
		if _, decodeErr = windows.MultiByteToWideChar(cp, 0, &b[0], int32(len(b)), &u[0], n); decodeErr != nil {
			t.Fatal(decodeErr)
		}
		return string(utf16.Decode(u))
	}
	out := devEnv{Vars: map[string]string{}}
	for k, v := range dev.Vars {
		out.Vars[k] = conv(v)
	}
	for _, p := range dev.PathBefore {
		out.PathBefore = append(out.PathBefore, conv(p))
	}
	for _, p := range dev.PathAfter {
		out.PathAfter = append(out.PathAfter, conv(p))
	}
	return out
}

// checkDump checks that the environment the dumper wrote has dev applied,
// with the PATH entries exactly as they are or, if not exact, cleaned.
func checkDump(t *testing.T, dump string, dev devEnv, exact bool) {
	t.Helper()
	data, err := os.ReadFile(dump)
	if err != nil {
		t.Fatalf("the dumper didn't run: %v", err)
	}
	var got map[string]string
	if jsonErr := json.Unmarshal(data, &got); jsonErr != nil {
		t.Fatal(jsonErr)
	}
	for _, k := range slices.Sorted(maps.Keys(dev.Vars)) {
		if v, ok := got[k]; !ok || v != dev.Vars[k] {
			t.Errorf("%s = %q (set: %v), want %q", k, v, ok, dev.Vars[k])
		}
	}
	path := splitPath(lookupFold(got, "PATH"))
	keys := func(entries []string) []string {
		if exact {
			return entries
		}
		out := make([]string, len(entries))
		for i, p := range entries {
			out[i] = filepath.Clean(p)
		}
		return out
	}
	n, m := len(dev.PathBefore), len(dev.PathAfter)
	if len(path) < n+m ||
		!slices.Equal(keys(path[:n]), keys(dev.PathBefore)) ||
		!slices.Equal(keys(path[len(path)-m:]), keys(dev.PathAfter)) {
		t.Errorf("PATH doesn't have the VS entries around it:\n%q", path)
	}
}
