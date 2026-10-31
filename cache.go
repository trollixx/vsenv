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

const cacheSchemaVersion = 4

type envCache struct {
	Schema              int    `json:"schema"`
	InstanceID          string `json:"instanceId"`
	InstallationVersion string `json:"installationVersion"`
	Arch                string `json:"arch"`
	HostArch            string `json:"hostArch"`
	DevCmdArgs          string `json:"devCmdArgs"`
	// Instances and SDKs are when any VS instance and the Windows SDKs changed
	// before the capture: VsDevCmd.bat uses the newest SDK, which another
	// instance or an SDK installer may have added or removed.
	Instances time.Time `json:"instances"`
	SDKs      time.Time `json:"sdks"`
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
	instances, sdks := instanceStateTime("*"), sdkStateTime()
	if data, readErr := os.ReadFile(cachePath); readErr == nil {
		var c envCache
		if json.Unmarshal(data, &c) == nil && c.Schema == cacheSchemaVersion &&
			c.Instances.Equal(instances) && c.SDKs.Equal(sdks) {
			return c.Env, nil
		}
	}

	diff, err := captureDevEnv(ctx, install, arch, hostArch, devArgs, baseEnv())
	if err != nil {
		return devEnv{}, err
	}

	c := envCache{
		Schema:              cacheSchemaVersion,
		InstanceID:          install.InstanceID,
		InstallationVersion: install.InstallationVersion,
		Arch:                arch,
		HostArch:            hostArch,
		DevCmdArgs:          devArgs,
		Instances:           instances,
		SDKs:                sdks,
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
