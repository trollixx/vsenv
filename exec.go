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

func mergedEnv(dev devEnv) []string {
	parent := os.Environ()
	vars := dev.vars(func(k string) string { return envValue(parent, k) })
	vars["Path"] = dev.path(envValue(parent, "PATH"))

	overrides := make(map[string]struct{}, len(vars))
	for k := range vars {
		overrides[strings.ToUpper(k)] = struct{}{}
	}
	keep := make([]string, 0, len(parent)+len(vars))
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
	for k, v := range vars {
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

func execChild(ctx context.Context, args []string, dev devEnv) error {
	env := mergedEnv(dev)
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

// shellArgs returns the arguments vsenv starts an interactive shell with.
func shellArgs(id string) ([]string, error) {
	switch id {
	case shellIDPowerShell, shellIDPwsh:
		return []string{"-NoLogo"}, nil
	case shellIDCmd:
		return []string{"/k", "prompt [vsenv] $P$G"}, nil
	case shellIDNu, shellIDBash, shellIDFish:
		return nil, nil
	}
	return nil, fmt.Errorf("unknown shell %q", id)
}

// resolveShell turns the --shell value into a shell ID and executable path.
// Empty means the parent shell, relaunched from the same binary; a value with
// a path separator is the shell's executable; anything else is a shell ID.
func resolveShell(ctx context.Context, shell string, env []string) (string, string, error) {
	var id, exe string
	switch {
	case shell == "":
		id, exe = detectParentShell()
	case strings.ContainsAny(shell, `\/`):
		id, exe = shellIDFromExe(shell), shell
		if id == "" {
			return "", "", fmt.Errorf("can't tell which shell %q is", shell)
		}
	default:
		id = strings.TrimSuffix(strings.ToLower(shell), ".exe")
		if id == "nushell" {
			id = shellIDNu
		}
	}
	if strings.ContainsAny(exe, `\/`) {
		return id, exe, nil
	}
	if _, err := shellArgs(id); err != nil {
		return "", "", err
	}
	exe, err := findShellExe(ctx, id, envValue(env, "PATH"))
	return id, exe, err
}

func findShellExe(ctx context.Context, id, pathEnv string) (string, error) {
	if id == shellIDBash {
		return findBash(ctx, pathEnv)
	}
	return lookInEnvPath(id+".exe", pathEnv, "")
}

// findBash locates a Windows-native bash (Git Bash, MSYS2). It skips
// the WSL launchers in System32 and WindowsApps, which start Linux bash where
// the VS environment is useless.
func findBash(ctx context.Context, pathEnv string) (string, error) {
	for _, dir := range splitPath(pathEnv) {
		d := strings.ToLower(filepath.Clean(dir))
		if strings.HasSuffix(d, `\system32`) || strings.HasSuffix(d, `\windowsapps`) {
			continue
		}
		p := filepath.Join(dir, "bash.exe")
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p, nil
		}
	}
	// Git for Windows often puts only git.exe on PATH; its bash sits under the
	// install root, three levels above the exec path (mingw64/libexec/git-core).
	if out, err := exec.CommandContext(ctx, "git", "--exec-path").Output(); err == nil {
		root := filepath.Join(strings.TrimSpace(string(out)), "..", "..", "..")
		for _, p := range []string{
			filepath.Join(root, "bin", "bash.exe"),
			filepath.Join(root, "usr", "bin", "bash.exe"),
		} {
			if _, statErr := os.Stat(p); statErr == nil {
				return p, nil
			}
		}
	}
	return "", errors.New("no Windows bash found; install Git for Windows or pass --shell <path to bash.exe>")
}

func spawnShell(ctx context.Context, shell string, dev devEnv) error {
	env := mergedEnv(dev)
	id, exe, err := resolveShell(ctx, shell, env)
	if err != nil {
		return err
	}
	args, err := shellArgs(id)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, exe, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = env
	if shell == "" {
		fmt.Fprintf(os.Stderr, "[vsenv] launching %s (auto-detected; override with --shell)\n", id)
	}
	// Belt: ignore SIGINT at the Go level so Ctrl+Break (mapped to SIGINT by
	// the Go runtime) doesn't terminate us, and to cover the microsecond
	// window before SetConsoleCtrlHandler takes effect below.
	signal.Ignore(os.Interrupt)
	defer signal.Reset(os.Interrupt)
	if startErr := cmd.Start(); startErr != nil {
		return startErr
	}
	// Suspenders: after the child is spawned (so it doesn't inherit the
	// disposition), tell Windows to suppress CTRL_C_EVENT delivery to us
	// entirely. Without this, Go's CTRL_C_EVENT handler running in vsenv
	// corrupts interactive shells (e.g. nushell) when they Ctrl+C a child.
	ignoreCtrlC(true)
	defer ignoreCtrlC(false)
	return cmd.Wait()
}
