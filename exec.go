package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
)

// ignoreCtrlC toggles the calling process's CTRL+C disposition via
// SetConsoleCtrlHandler(NULL, ignore). When set, the OS does not deliver
// CTRL_C_EVENT to this process, so Go's runtime handler never fires for
// Ctrl+C. We use this while a child shell is running — without it, Go's
// CTRL_C_EVENT dispatch (even with [signal.Ignore]) disturbs interactive
// shells like nushell when they Ctrl+C a grandchild process, leaving
// stdin corrupted afterward.
func ignoreCtrlC(ignore bool) {
	var add uintptr
	if ignore {
		add = 1
	}
	proc := syscall.NewLazyDLL("kernel32.dll").NewProc("SetConsoleCtrlHandler")
	_, _, _ = proc.Call(0, add)
}

func mergedEnv(diff map[string]string) []string {
	parent := os.Environ()
	overrides := make(map[string]struct{}, len(diff))
	for k := range diff {
		overrides[strings.ToUpper(k)] = struct{}{}
	}
	keep := make([]string, 0, len(parent)+len(diff))
	for _, e := range parent {
		eq := strings.IndexByte(e, '=')
		if eq <= 0 {
			continue
		}
		if _, ok := overrides[strings.ToUpper(e[:eq])]; ok {
			continue
		}
		keep = append(keep, e)
	}
	for k, v := range diff {
		keep = append(keep, k+"="+v)
	}
	return keep
}

func envValue(env []string, name string) string {
	upper := strings.ToUpper(name) + "="
	for _, e := range env {
		if strings.HasPrefix(strings.ToUpper(e), upper) {
			return e[len(upper):]
		}
	}
	return ""
}

func lookInEnvPath(file, pathEnv, pathExt string) (string, error) {
	if strings.ContainsAny(file, `\/`) || filepath.IsAbs(file) {
		return file, nil
	}
	exts := []string{""}
	for x := range strings.SplitSeq(pathExt, ";") {
		x = strings.TrimSpace(x)
		if x != "" {
			exts = append(exts, x)
		}
	}
	for _, dir := range filepath.SplitList(pathEnv) {
		if dir == "" {
			continue
		}
		for _, ext := range exts {
			full := filepath.Join(dir, file+ext)
			if info, err := os.Stat(full); err == nil && !info.IsDir() {
				return full, nil
			}
		}
	}
	return "", fmt.Errorf("%q not found in PATH", file)
}

func execChild(ctx context.Context, args []string, diff map[string]string) error {
	env := mergedEnv(diff)
	pathExt := envValue(env, "PATHEXT")
	if pathExt == "" {
		pathExt = ".COM;.EXE;.BAT;.CMD"
	}
	resolved, err := lookInEnvPath(args[0], envValue(env, "PATH"), pathExt)
	if err != nil {
		return err
	}
	c := exec.CommandContext(ctx, resolved, args[1:]...)
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	c.Env = env
	if runErr := c.Run(); runErr != nil {
		if ee, ok := errors.AsType[*exec.ExitError](runErr); ok {
			os.Exit(ee.ExitCode())
		}
		return runErr
	}
	return nil
}

func spawnShell(ctx context.Context, shell string, diff map[string]string) error {
	autoDetected := shell == ""
	if autoDetected {
		shell = detectParentShell()
	}
	var cmd *exec.Cmd
	switch strings.ToLower(shell) {
	case shellIDPowerShell:
		cmd = exec.CommandContext(ctx, "powershell.exe", "-NoLogo")
	case shellIDPwsh:
		cmd = exec.CommandContext(ctx, "pwsh.exe", "-NoLogo")
	case shellIDCmd:
		cmd = exec.CommandContext(ctx, "cmd.exe", "/k", "prompt [vsenv] $P$G")
	case shellIDNu, "nushell":
		cmd = exec.CommandContext(ctx, "nu.exe")
	case shellIDBash:
		cmd = exec.CommandContext(ctx, "bash.exe")
	default:
		return fmt.Errorf("unknown shell %q", shell)
	}
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = mergedEnv(diff)
	if autoDetected {
		fmt.Fprintf(os.Stderr, "[vsenv] launching %s (auto-detected; override with --shell)\n", shell)
	}
	// Belt: ignore SIGINT at the Go level so Ctrl+Break (mapped to SIGINT by
	// the Go runtime) doesn't terminate us, and to cover the microsecond
	// window before SetConsoleCtrlHandler takes effect below.
	signal.Ignore(os.Interrupt)
	defer signal.Reset(os.Interrupt)
	if err := cmd.Start(); err != nil {
		return err
	}
	// Suspenders: after the child is spawned (so it doesn't inherit the
	// disposition), tell Windows to suppress CTRL_C_EVENT delivery to us
	// entirely. Without this, Go's CTRL_C_EVENT handler running in vsenv
	// corrupts interactive shells (e.g. nushell) when they Ctrl+C a child.
	ignoreCtrlC(true)
	defer ignoreCtrlC(false)
	return cmd.Wait()
}
