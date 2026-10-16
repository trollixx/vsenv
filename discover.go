package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

const vswhereDefault = `C:\Program Files (x86)\Microsoft Visual Studio\Installer\vswhere.exe`

type Install struct {
	InstanceID          string `json:"instanceId"`
	InstallationPath    string `json:"installationPath"`
	InstallationVersion string `json:"installationVersion"`
	DisplayName         string `json:"displayName"`
	ProductID           string `json:"productId"`
	IsPrerelease        bool   `json:"isPrerelease"`
	// IsComplete is false for an install that failed or is still in progress.
	IsComplete bool `json:"isComplete"`
}

type installsCache struct {
	Schema int `json:"schema"`
	// Installer is the installer's state before vswhere ran.
	Installer installerState `json:"installer"`
	Installs  []Install      `json:"installs"`
}

// installerState identifies the state of the VS Installer: when vswhere.exe
// was updated and when any instance last changed.
type installerState struct {
	Vswhere   time.Time `json:"vswhere"`
	Instances time.Time `json:"instances"`
}

func currentInstallerState() installerState {
	var s installerState
	if info, err := os.Stat(vswhereDefault); err == nil {
		s.Vswhere = info.ModTime()
	}
	s.Instances = instanceStateTime("*")
	return s
}

func (s installerState) equal(o installerState) bool {
	return s.Vswhere.Equal(o.Vswhere) && s.Instances.Equal(o.Instances)
}

func DiscoverInstalls(ctx context.Context, force bool) ([]Install, error) {
	cachePath, err := installsCachePath()
	if err != nil {
		return nil, err
	}

	state := currentInstallerState()
	if !force {
		if c, ok := readInstallsCache(cachePath); ok && c.Installer.equal(state) {
			return c.Installs, nil
		}
	}

	installs, err := runVsWhere(ctx)
	if err != nil {
		return nil, err
	}
	_ = writeInstallsCache(cachePath, installsCache{Schema: cacheSchemaVersion, Installer: state, Installs: installs})
	return installs, nil
}

func runVsWhere(ctx context.Context) ([]Install, error) {
	if _, err := os.Stat(vswhereDefault); err != nil {
		return nil, fmt.Errorf("vswhere.exe not found at %s — is the Visual Studio Installer present?", vswhereDefault)
	}

	cmd := exec.CommandContext(ctx, vswhereDefault,
		"-prerelease", "-all", "-products", "*", "-sort", "-format", "json", "-utf8")
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
		// vswhere lists the newest first, but that can be a product without
		// MSVC, like SQL Server Management Studio, which also uses the VS
		// Installer, or an install that didn't finish.
		for _, complete := range []bool{true, false} {
			for i := range installs {
				if hasMSVC(&installs[i]) && (installs[i].IsComplete || !complete) {
					return &installs[i], nil
				}
			}
		}
		return &installs[0], nil
	}
	for i := range installs {
		if installs[i].InstanceID == instanceID {
			return &installs[i], nil
		}
	}
	return nil, fmt.Errorf("VS instance %q not found", instanceID)
}

// hasMSVC reports whether install has a C++ toolset, by the file vcvarsall.bat
// reads the default toolset version from.
func hasMSVC(install *Install) bool {
	_, err := os.Stat(filepath.Join(install.InstallationPath,
		"VC", "Auxiliary", "Build", "Microsoft.VCToolsVersion.default.txt"))
	return err == nil
}

func installsCachePath() (string, error) {
	dir, err := cacheDir()
	if err != nil {
		return "", err
	}
	// Named for the schema, as the env cache files are, so vsenv versions with
	// different formats don't keep replacing each other's file.
	return filepath.Join(dir, fmt.Sprintf("installs-%d.json", cacheSchemaVersion)), nil
}

// readInstallsCache reads the cache at path. A cache in another format, such
// as the bare list older versions wrote, doesn't count.
func readInstallsCache(path string) (installsCache, bool) {
	var c installsCache
	data, err := os.ReadFile(path)
	if err != nil {
		return c, false
	}
	if jsonErr := json.Unmarshal(data, &c); jsonErr != nil || c.Schema != cacheSchemaVersion {
		return c, false
	}
	return c, true
}

func writeInstallsCache(path string, c installsCache) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return writeInstallsCacheRaw(path, data)
}

func writeInstallsCacheRaw(path string, data []byte) error {
	//nolint:gosec // G703: path is under %LOCALAPPDATA%, the user's own dir by design
	return os.WriteFile(path, data, 0o600)
}

// instanceStateTime returns when the VS Installer last installed, updated,
// modified or removed the instance with the given ID ("*" for any), or the
// zero time if that can't be determined.
func instanceStateTime(instanceID string) time.Time {
	programData := os.Getenv("ProgramData")
	if programData == "" {
		return time.Time{}
	}
	// vswhere reads the same per-instance state the installer keeps here.
	dir := filepath.Join(programData, "Microsoft", "VisualStudio", "Packages", "_Instances")
	var latest time.Time
	paths, _ := filepath.Glob(filepath.Join(dir, instanceID, "state.json"))
	if instanceID == "*" {
		paths = append(paths, dir) // catches removed instances
	}
	for _, p := range paths {
		//nolint:gosec // G703: read-only stat under %ProgramData%, where the VS Installer keeps state
		if info, err := os.Stat(p); err == nil && info.ModTime().After(latest) {
			latest = info.ModTime()
		}
	}
	return latest
}
