package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

const vswhereDefault = `C:\Program Files (x86)\Microsoft Visual Studio\Installer\vswhere.exe`

type Install struct {
	InstanceID          string `json:"instanceId"`
	InstallationPath    string `json:"installationPath"`
	InstallationVersion string `json:"installationVersion"`
	DisplayName         string `json:"displayName"`
	ProductID           string `json:"productId"`
	IsPrerelease        bool   `json:"isPrerelease"`
}

func DiscoverInstalls(ctx context.Context, force bool) ([]Install, error) {
	cachePath, err := installsCachePath()
	if err != nil {
		return nil, err
	}

	if !force {
		if installs, ok := readInstallsCache(cachePath); ok && installsCacheFresh(cachePath) {
			return installs, nil
		}
	}

	installs, err := runVsWhere(ctx)
	if err != nil {
		return nil, err
	}
	_ = writeInstallsCache(cachePath, installs)
	return installs, nil
}

func runVsWhere(ctx context.Context) ([]Install, error) {
	if _, err := os.Stat(vswhereDefault); err != nil {
		return nil, fmt.Errorf("vswhere.exe not found at %s — is the Visual Studio Installer present?", vswhereDefault)
	}

	cmd := exec.CommandContext(ctx, vswhereDefault,
		"-prerelease", "-all", "-products", "*", "-format", "json", "-utf8")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("vswhere failed: %w", err)
	}

	var installs []Install
	if jsonErr := json.Unmarshal(out, &installs); jsonErr != nil {
		return nil, fmt.Errorf("parsing vswhere output: %w", jsonErr)
	}
	return installs, nil
}

func SelectInstall(ctx context.Context, instanceID string) (*Install, error) {
	installs, err := DiscoverInstalls(ctx, false)
	if err != nil {
		return nil, err
	}
	if len(installs) == 0 {
		return nil, errors.New("no Visual Studio installations found")
	}
	if instanceID == "" {
		return &installs[0], nil
	}
	for i := range installs {
		if installs[i].InstanceID == instanceID {
			return &installs[i], nil
		}
	}
	return nil, fmt.Errorf("VS instance %q not found", instanceID)
}

func installsCachePath() (string, error) {
	dir, err := cacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "installs.json"), nil
}

func readInstallsCache(path string) ([]Install, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	var installs []Install
	if jsonErr := json.Unmarshal(data, &installs); jsonErr != nil {
		return nil, false
	}
	return installs, true
}

func writeInstallsCache(path string, installs []Install) error {
	data, err := json.MarshalIndent(installs, "", "  ")
	if err != nil {
		return err
	}
	return writeInstallsCacheRaw(path, data)
}

func writeInstallsCacheRaw(path string, data []byte) error {
	return os.WriteFile(path, data, 0o600)
}

func installsCacheFresh(path string) bool {
	cs, err := os.Stat(path)
	if err != nil {
		return false
	}
	vs, err := os.Stat(vswhereDefault)
	if err != nil {
		return false
	}
	return !vs.ModTime().After(cs.ModTime())
}
