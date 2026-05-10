package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

const cacheSchemaVersion = 1

type envCache struct {
	Schema              int               `json:"schema"`
	InstanceID          string            `json:"instanceId"`
	InstallationVersion string            `json:"installationVersion"`
	Arch                string            `json:"arch"`
	HostArch            string            `json:"hostArch"`
	DevCmdArgs          string            `json:"devCmdArgs"`
	Env                 map[string]string `json:"env"`
}

func cacheDir() (string, error) {
	la := os.Getenv("LOCALAPPDATA")
	if la == "" {
		return "", errors.New("LOCALAPPDATA not set")
	}
	dir := filepath.Join(la, "vsenv")
	envDir := filepath.Join(dir, "env")
	//nolint:gosec // G703: %LOCALAPPDATA% is the user's own dir by design
	if err := os.MkdirAll(envDir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

func envCachePath(install *Install, arch, hostArch, devArgs string) (string, error) {
	dir, err := cacheDir()
	if err != nil {
		return "", err
	}
	key := install.InstanceID + "|" + install.InstallationVersion + "|" + arch + "|" + hostArch + "|" + devArgs
	h := sha256.Sum256([]byte(key))
	return filepath.Join(dir, "env", hex.EncodeToString(h[:])+".json"), nil
}

func GetEnv(ctx context.Context, install *Install, arch, hostArch, devArgs string) (map[string]string, error) {
	cachePath, err := envCachePath(install, arch, hostArch, devArgs)
	if err != nil {
		return nil, err
	}

	if data, readErr := os.ReadFile(cachePath); readErr == nil {
		var c envCache
		if json.Unmarshal(data, &c) == nil && c.Schema == cacheSchemaVersion {
			return c.Env, nil
		}
	}

	parent := parentEnvMap()
	full, err := captureDevShellEnv(ctx, install, arch, hostArch, devArgs)
	if err != nil {
		return nil, err
	}
	diff := diffEnv(parent, full)

	c := envCache{
		Schema:              cacheSchemaVersion,
		InstanceID:          install.InstanceID,
		InstallationVersion: install.InstallationVersion,
		Arch:                arch,
		HostArch:            hostArch,
		DevCmdArgs:          devArgs,
		Env:                 diff,
	}
	if data, marshalErr := json.MarshalIndent(c, "", "  "); marshalErr == nil {
		_ = os.WriteFile(cachePath, data, 0o600)
	}
	return diff, nil
}

func ClearEnvCache() error {
	dir, err := cacheDir()
	if err != nil {
		return err
	}
	return os.RemoveAll(filepath.Join(dir, "env"))
}
