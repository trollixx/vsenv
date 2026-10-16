package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	shellIDNu         = "nu"
	shellIDPwsh       = "pwsh"
	shellIDPowerShell = "powershell"
	shellIDCmd        = "cmd"
	shellIDBash       = "bash"
	shellIDFish       = "fish"
)

// shellIDFromExe maps a shell executable name or path to a shell ID, or ""
// if it isn't a shell vsenv knows.
func shellIDFromExe(exe string) string {
	switch strings.ToLower(filepath.Base(exe)) {
	case "nu.exe":
		return shellIDNu
	case "pwsh.exe":
		return shellIDPwsh
	case "powershell.exe":
		return shellIDPowerShell
	case "cmd.exe":
		return shellIDCmd
	case "bash.exe", "sh.exe":
		return shellIDBash
	case "fish.exe":
		return shellIDFish
	}
	return ""
}

// detectParentShell identifies the shell that launched vsenv, returning its ID
// and executable. The executable is a full path when it can be determined, so
// the same binary can be relaunched; otherwise it's a bare file name or empty.
func detectParentShell() (string, string) {
	if path, err := parentExePath(); err == nil {
		if id := shellIDFromExe(path); id != "" {
			return id, path
		}
	}
	if os.Getenv("NU_VERSION") != "" {
		return shellIDNu, ""
	}
	return shellIDPowerShell, ""
}

// parentExePath returns the full path of the parent process's executable,
// or just its file name if the process can't be opened.
func parentExePath() (string, error) {
	pid := uint32(os.Getpid()) //nolint:gosec // Windows PIDs fit in uint32

	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return "", err
	}
	defer func() { _ = windows.CloseHandle(snap) }()

	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))

	var ppid uint32
	if firstErr := windows.Process32First(snap, &entry); firstErr != nil {
		return "", firstErr
	}
	for {
		if entry.ProcessID == pid {
			ppid = entry.ParentProcessID
			break
		}
		if nextErr := windows.Process32Next(snap, &entry); nextErr != nil {
			return "", nextErr
		}
	}
	if ppid == 0 {
		return "", errors.New("parent process not found")
	}

	snap2, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return "", err
	}
	defer func() { _ = windows.CloseHandle(snap2) }()

	if firstErr := windows.Process32First(snap2, &entry); firstErr != nil {
		return "", firstErr
	}
	for {
		if entry.ProcessID == ppid {
			if path, pathErr := processImagePath(ppid); pathErr == nil {
				return path, nil
			}
			return windows.UTF16ToString(entry.ExeFile[:]), nil
		}
		if nextErr := windows.Process32Next(snap2, &entry); nextErr != nil {
			return "", nextErr
		}
	}
}

func processImagePath(pid uint32) (string, error) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return "", err
	}
	defer func() { _ = windows.CloseHandle(h) }()
	size := uint32(windows.MAX_LONG_PATH)
	buf := make([]uint16, size)
	if queryErr := windows.QueryFullProcessImageName(h, 0, &buf[0], &size); queryErr != nil {
		return "", queryErr
	}
	return windows.UTF16ToString(buf[:size]), nil
}
