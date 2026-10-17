package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const cacheSchemaVersion = 3

type envCache struct {
	Schema              int    `json:"schema"`
	InstanceID          string `json:"instanceId"`
	InstallationVersion string `json:"installationVersion"`
	Arch                string `json:"arch"`
	HostArch            string `json:"hostArch"`
	DevCmdArgs          string `json:"devCmdArgs"`
	// StateTime is instanceStateTime of the instance before the capture.
	StateTime time.Time `json:"stateTime"`
	Env       devEnv    `json:"env"`
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
	// The schema is part of the key so vsenv versions with different cache
	// formats don't keep replacing each other's files.
	key := fmt.Sprintf("%d|%s|%s|%s|%s|%s",
		cacheSchemaVersion, install.InstanceID, install.InstallationVersion, arch, hostArch, devArgs)
	h := sha256.Sum256([]byte(key))
	return filepath.Join(dir, "env", hex.EncodeToString(h[:])+".json"), nil
}

func GetEnv(ctx context.Context, install *Install, arch, hostArch, devArgs string) (devEnv, error) {
	cachePath, err := envCachePath(install, arch, hostArch, devArgs)
	if err != nil {
		return devEnv{}, err
	}

	// Taken before the capture, so a change the VS Installer makes during it
	// still invalidates the result.
	stateTime := instanceStateTime(install.InstanceID)
	if data, readErr := os.ReadFile(cachePath); readErr == nil {
		var c envCache
		if json.Unmarshal(data, &c) == nil && c.Schema == cacheSchemaVersion && c.StateTime.Equal(stateTime) {
			return c.Env, nil
		}
	}

	base := baseEnv()
	full, err := captureDevShellEnv(ctx, install, arch, hostArch, devArgs, base)
	if err != nil {
		return devEnv{}, err
	}
	diff := diffEnv(envMap(base), full)

	c := envCache{
		Schema:              cacheSchemaVersion,
		InstanceID:          install.InstanceID,
		InstallationVersion: install.InstallationVersion,
		Arch:                arch,
		HostArch:            hostArch,
		DevCmdArgs:          devArgs,
		StateTime:           stateTime,
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
