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
	"strings"
)

const envSentinel = "==VSENV_BEGIN=="

func captureDevShellEnv(
	ctx context.Context,
	install *Install,
	arch, hostArch, devArgs string,
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
	var out bytes.Buffer
	c.Stdout = &out
	c.Stderr = &out
	if runErr := c.Run(); runErr != nil {
		return nil, fmt.Errorf("VsDevCmd.bat failed: %w\noutput:\n%s", runErr, out.String())
	}

	return parseSetOutput(out.String())
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

func diffEnv(parent, full map[string]string) map[string]string {
	diff := map[string]string{}
	for k, v := range full {
		if pv, ok := parent[k]; !ok || pv != v {
			diff[k] = v
		}
	}
	return diff
}

func parentEnvMap() map[string]string {
	m := map[string]string{}
	for _, e := range os.Environ() {
		k, v, hasEq := strings.Cut(e, "=")
		if !hasEq || k == "" {
			continue
		}
		m[k] = v
	}
	return m
}
