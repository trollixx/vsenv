package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

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
// be reapplied on top of whatever PATH the caller has at the time.
type devEnv struct {
	Vars       map[string]string `json:"vars"`
	PathBefore []string          `json:"pathBefore"`
	PathAfter  []string          `json:"pathAfter"`
}

// path returns cur with the VS entries put before and after it. Entries cur
// already has are moved rather than skipped, so applying the env again
// changes nothing, and applying another architecture's env puts its tools
// ahead of the ones applied before.
func (e devEnv) path(cur string) string {
	vs := map[string]bool{}
	for _, p := range slices.Concat(e.PathBefore, e.PathAfter) {
		vs[pathEntryKey(p)] = true
	}
	parts := slices.Clone(e.PathBefore)
	for _, p := range splitPath(cur) {
		if !vs[pathEntryKey(p)] {
			parts = append(parts, p)
		}
	}
	parts = append(parts, e.PathAfter...)
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

	env, err := parseSetOutput(out.String())
	if err != nil {
		return nil, err
	}
	delete(env, telemetryOptOut)
	return env, nil
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
		"set\r\n",
	}
	for _, line := range writes {
		if _, writeErr := io.WriteString(f, line); writeErr != nil {
			return "", writeErr
		}
	}
	return f.Name(), nil
}

func parseSetOutput(out string) (map[string]string, error) {
	_, tail, ok := strings.Cut(out, envSentinel)
	if !ok {
		return nil, errors.New("env sentinel not found in VsDevCmd output")
	}
	env := map[string]string{}
	for line := range strings.SplitSeq(tail, "\n") {
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
	e := devEnv{Vars: map[string]string{}}
	for k, v := range full {
		// The __VSCMD_PREINIT_ variables hold the environment VsDevCmd started
		// from; replayed later they'd be stale. cmd.exe sets PROMPT itself when
		// it's missing, as it is from the default environment, and applying it
		// would reset a custom cmd prompt.
		if strings.EqualFold(k, "PATH") || strings.EqualFold(k, "PROMPT") ||
			strings.HasPrefix(strings.ToUpper(k), preinitPrefix) {
			continue
		}
		if bv, ok := base[k]; !ok || bv != v {
			e.Vars[k] = v
		}
	}
	e.PathBefore, e.PathAfter = addedPathEntries(lookupFold(base, "PATH"), lookupFold(full, "PATH"))
	return e
}

// addedPathEntries returns the entries VsDevCmd.bat added before and after
// the original PATH. It prepends most of its directories and appends a few
// (CMake, Ninja, vcpkg), leaving the original value intact in between.
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
