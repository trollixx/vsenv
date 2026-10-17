package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf16"

	"golang.org/x/sys/windows"
)

const (
	envSentinel = "==VSENV_BEGIN=="
	// telemetryOptOut stops VsDevCmd.bat from launching PowerShell to send
	// usage telemetry.
	telemetryOptOut = "VSCMD_SKIP_SENDTELEMETRY"
	// preinitPrefix starts the names of VsDevCmd's copies of the variables it
	// started from, which VsDevCmd -clean_env restores.
	preinitPrefix = "__VSCMD_PREINIT_"
)

// devEnv is what VsDevCmd.bat adds to the environment it started from. PATH
// is kept as the entries added before and after the original value, so it can
// be reapplied on top of whatever PATH the caller has at the time, and so are
// the other lists VsDevCmd.bat adds to, like INCLUDE and LIB.
type devEnv struct {
	Vars       map[string]string    `json:"vars"`
	PathBefore []string             `json:"pathBefore"`
	PathAfter  []string             `json:"pathAfter"`
	Lists      map[string]listAdded `json:"lists"`
}

// listAdded holds the entries VsDevCmd.bat added before and after a list's
// original value.
type listAdded struct {
	Before []string `json:"before"`
	After  []string `json:"after"`
}

// isListVar reports whether VsDevCmd.bat adds entries to the variable named k
// while keeping its value, other than PATH.
func isListVar(k string) bool {
	switch strings.ToUpper(k) {
	case "INCLUDE", "EXTERNAL_INCLUDE", "LIB", "LIBPATH":
		return true
	}
	return false
}

// path returns cur with the VS entries put before and after it.
func (e devEnv) path(cur string) string {
	return addEntries(e.PathBefore, e.PathAfter, cur)
}

// vars returns the variables to set, with each list's VS entries put around
// the caller's value of it, which getenv returns.
func (e devEnv) vars(getenv func(string) string) map[string]string {
	vars := make(map[string]string, len(e.Vars)+len(e.Lists))
	maps.Copy(vars, e.Vars)
	for k, l := range e.Lists {
		vars[k] = addEntries(l.Before, l.After, getenv(k))
	}
	return vars
}

// addEntries returns cur with before and after put around it. Entries cur
// already has are moved rather than skipped, so applying the env again
// changes nothing, and applying another architecture's env puts its entries
// ahead of the ones applied before.
func addEntries(before, after []string, cur string) string {
	vs := map[string]bool{}
	for _, p := range slices.Concat(before, after) {
		vs[pathEntryKey(p)] = true
	}
	parts := slices.Clone(before)
	for _, p := range splitPath(cur) {
		if !vs[pathEntryKey(p)] {
			parts = append(parts, p)
		}
	}
	parts = append(parts, after...)
	return strings.Join(parts, string(os.PathListSeparator))
}

// baseEnv returns the environment a new process of the current user starts
// with, built from the registry rather than inherited. Capturing against it
// keeps the cache independent of the shell that happens to build it, which
// may already have the VS environment loaded.
func baseEnv() []string {
	var tok windows.Token
	// CreateEnvironmentBlock needs query and duplicate access to a primary token.
	if err := windows.OpenProcessToken(windows.CurrentProcess(),
		windows.TOKEN_QUERY|windows.TOKEN_DUPLICATE, &tok); err == nil {
		defer func() { _ = tok.Close() }()
		if env, envErr := tok.Environ(false); envErr == nil {
			return env
		}
	}
	return os.Environ()
}

func captureDevShellEnv(
	ctx context.Context,
	install *Install,
	arch, hostArch, devArgs string,
	base []string,
) (map[string]string, error) {
	vsdevcmd := filepath.Join(install.InstallationPath, "Common7", "Tools", "VsDevCmd.bat")
	if _, err := os.Stat(vsdevcmd); err != nil {
		return nil, fmt.Errorf("VsDevCmd.bat not found at %s", vsdevcmd)
	}

	args := []string{"-no_logo", "-arch=" + arch}
	if hostArch != "" {
		args = append(args, "-host_arch="+hostArch)
	}
	if devArgs != "" {
		args = append(args, strings.Fields(devArgs)...)
	}

	bat, err := writeTempBat(vsdevcmd, args)
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.Remove(bat) }()

	c := exec.CommandContext(ctx, "cmd.exe", "/d", "/c", bat)
	// The opt-out is set even over a user's own value, which could be empty
	// (VsDevCmd only skips telemetry for a non-empty one), and is dropped from
	// the result, so it doesn't override that value later.
	c.Env = append(slices.Clone(base), telemetryOptOut+"=1")
	var out bytes.Buffer
	c.Stdout = &out
	c.Stderr = &out
	if runErr := c.Run(); runErr != nil {
		return nil, fmt.Errorf("VsDevCmd.bat failed: %w\noutput:\n%s", runErr, out.String())
	}

	env, err := parseSetOutput(out.Bytes())
	if err != nil {
		return nil, err
	}
	delete(env, telemetryOptOut)
	return env, checkCompiler(env)
}

// checkCompiler fails if VsDevCmd.bat set up a C++ toolset without a compiler
// for the requested architectures. VsDevCmd.bat doesn't report that; the
// compiler for the host architecture just ends up first on PATH instead.
func checkCompiler(env map[string]string) error {
	tools := lookupFold(env, "VCToolsInstallDir")
	host := lookupFold(env, "VSCMD_ARG_HOST_ARCH")
	tgt := lookupFold(env, "VSCMD_ARG_TGT_ARCH")
	if tools == "" || host == "" || tgt == "" {
		return nil // no C++ toolset to check
	}
	hostDir := filepath.Join(tools, "bin", "Host"+host)
	if !exists(hostDir) {
		return nil // a layout without Host<arch> dirs, like the VS 2015 (v140) toolset's
	}
	if cl := filepath.Join(hostDir, tgt, "cl.exe"); !exists(cl) {
		return fmt.Errorf("this install has no MSVC compiler targeting %s on %s (%s not found); "+
			"add the C++ build tools for %s in the Visual Studio Installer", tgt, host, cl, tgt)
	}
	return nil
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func writeTempBat(vsdevcmd string, args []string) (string, error) {
	f, err := os.CreateTemp("", "vsenv-*.bat")
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	writes := []string{
		"@echo off\r\n",
		fmt.Sprintf("call \"%s\" %s\r\n", vsdevcmd, strings.Join(args, " ")),
		"if errorlevel 1 exit /b %errorlevel%\r\n",
		"echo " + envSentinel + "\r\n",
		// cmd writes to a pipe in the console code page, which can't hold every
		// non-ASCII value (such as a user profile path); cmd /u writes UTF-16.
		// It's named in full, as ComSpec can name another shell.
		"\"%SystemRoot%\\System32\\cmd.exe\" /d /u /c set\r\n",
	}
	for _, line := range writes {
		if _, writeErr := io.WriteString(f, line); writeErr != nil {
			return "", writeErr
		}
	}
	return f.Name(), nil
}

// parseSetOutput parses the temp batch file's output: whatever VsDevCmd.bat
// printed, the sentinel line, then the environment in UTF-16 from cmd /u.
func parseSetOutput(out []byte) (map[string]string, error) {
	_, tail, ok := bytes.Cut(out, []byte(envSentinel+"\r\n"))
	if !ok {
		return nil, errors.New("env sentinel not found in VsDevCmd output")
	}
	if len(tail)%2 != 0 {
		return nil, errors.New("environment printed by cmd /u isn't UTF-16")
	}
	units := make([]uint16, len(tail)/2)
	for i := range units {
		units[i] = binary.LittleEndian.Uint16(tail[2*i:])
	}
	env := map[string]string{}
	for line := range strings.SplitSeq(string(utf16.Decode(units)), "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		k, v, hasEq := strings.Cut(line, "=")
		if !hasEq || k == "" {
			continue
		}
		env[k] = v
	}
	return env, nil
}

func diffEnv(base, full map[string]string) devEnv {
	e := devEnv{Vars: map[string]string{}, Lists: map[string]listAdded{}}
	for k, v := range full {
		// The __VSCMD_PREINIT_ variables hold the environment VsDevCmd started
		// from; replayed later they'd be stale. cmd.exe sets PROMPT itself when
		// it's missing, as it is from the default environment, and applying it
		// would reset a custom cmd prompt.
		if strings.EqualFold(k, "PATH") || strings.EqualFold(k, "PROMPT") ||
			strings.HasPrefix(strings.ToUpper(k), preinitPrefix) {
			continue
		}
		bv, ok := base[k]
		if ok && bv == v {
			continue
		}
		if isListVar(k) {
			var l listAdded
			l.Before, l.After = addedPathEntries(bv, v)
			e.Lists[k] = l
		} else {
			e.Vars[k] = v
		}
	}
	e.PathBefore, e.PathAfter = addedPathEntries(lookupFold(base, "PATH"), lookupFold(full, "PATH"))
	return e
}

// addedPathEntries returns the entries VsDevCmd.bat added before and after
// the original value of PATH, or another list. It prepends most of its
// directories and appends a few (to PATH: CMake, Ninja, vcpkg), leaving the
// original value intact in between.
func addedPathEntries(before, after string) ([]string, []string) {
	if before != "" {
		if i := strings.Index(";"+after+";", ";"+before+";"); i >= 0 {
			return splitPath(after[:i]), splitPath(after[i+len(before):])
		}
	}
	// No original PATH, or VsDevCmd.bat didn't keep it intact: put whatever
	// is new first.
	return missingEntries(splitPath(after), pathEntrySet(before)), nil
}

func splitPath(path string) []string {
	var out []string
	for _, p := range filepath.SplitList(path) {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func missingEntries(entries []string, have map[string]bool) []string {
	var out []string
	for _, p := range entries {
		if !have[pathEntryKey(p)] {
			out = append(out, p)
		}
	}
	return out
}

func pathEntrySet(path string) map[string]bool {
	set := map[string]bool{}
	for _, p := range splitPath(path) {
		set[pathEntryKey(p)] = true
	}
	return set
}

// pathEntryKey normalizes a PATH entry for comparison. Cleaning matters for
// entries like VsDevCmd's "bin\\x64", which come back as "bin\x64" after a
// round trip through an MSYS2 shell's PATH.
func pathEntryKey(p string) string {
	return strings.ToLower(filepath.Clean(p))
}

// samePath reports whether two PATH values have the same entries, as
// pathEntryKey compares them.
func samePath(a, b string) bool {
	return slices.EqualFunc(splitPath(a), splitPath(b), func(x, y string) bool {
		return pathEntryKey(x) == pathEntryKey(y)
	})
}

func lookupFold(m map[string]string, key string) string {
	for k, v := range m {
		if strings.EqualFold(k, key) {
			return v
		}
	}
	return ""
}

func envMap(env []string) map[string]string {
	m := map[string]string{}
	for _, e := range env {
		k, v, hasEq := strings.Cut(e, "=")
		if !hasEq || k == "" {
			continue
		}
		m[k] = v
	}
	return m
}
