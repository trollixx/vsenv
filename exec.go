package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

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
	return cmd.Run()
}
