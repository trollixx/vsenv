package main

import (
	"debug/pe"
	"runtime"

	"golang.org/x/sys/windows"
)

// Architectures, as VsDevCmd names them.
const (
	archX64   = "x64"
	archX86   = "x86"
	archARM64 = "arm64"
)

// nativeArch returns this machine's architecture. It can differ from vsenv's
// own: Windows on ARM runs the x64 build under emulation, where the process
// sees x64 everywhere but in IsWow64Process2.
func nativeArch() string {
	// IsWow64Process2 is new in Windows 10 1709.
	if windows.NewLazySystemDLL("kernel32.dll").NewProc("IsWow64Process2").Find() == nil {
		var process, native uint16
		if windows.IsWow64Process2(windows.CurrentProcess(), &process, &native) == nil {
			if arch := machineArch(native); arch != "" {
				return arch
			}
		}
	}
	switch runtime.GOARCH {
	case "arm64":
		return archARM64
	case "386":
		return archX86
	}
	return archX64
}

// machineArch returns the architecture of a PE machine type, or "" for one
// VsDevCmd doesn't host.
func machineArch(machine uint16) string {
	switch machine {
	case pe.IMAGE_FILE_MACHINE_AMD64:
		return archX64
	case pe.IMAGE_FILE_MACHINE_ARM64:
		return archARM64
	case pe.IMAGE_FILE_MACHINE_I386:
		return archX86
	}
	return ""
}
