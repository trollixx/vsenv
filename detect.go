package main

import (
	"errors"
	"os"
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

func detectParentShell() string {
	if exe, err := parentExeName(); err == nil {
		switch strings.ToLower(exe) {
		case "nu.exe":
			return shellIDNu
		case "pwsh.exe":
			return shellIDPwsh
		case "powershell.exe":
			return shellIDPowerShell
		case "cmd.exe":
			return shellIDCmd
		case "bash.exe", "sh.exe", "git-bash.exe":
			return shellIDBash
		case "fish.exe":
			return shellIDFish
		}
	}
	if os.Getenv("NU_VERSION") != "" {
		return shellIDNu
	}
	if os.Getenv("PSModulePath") != "" {
		return shellIDPowerShell
	}
	return shellIDPowerShell
}

func parentExeName() (string, error) {
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
			return windows.UTF16ToString(entry.ExeFile[:]), nil
		}
		if nextErr := windows.Process32Next(snap2, &entry); nextErr != nil {
			return "", nextErr
		}
	}
}
