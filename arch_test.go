package main

import (
	"debug/pe"
	"runtime"
	"testing"
)

func TestMachineArch(t *testing.T) {
	cases := map[uint16]string{
		pe.IMAGE_FILE_MACHINE_AMD64: "x64",
		pe.IMAGE_FILE_MACHINE_ARM64: "arm64",
		pe.IMAGE_FILE_MACHINE_I386:  "x86",
		pe.IMAGE_FILE_MACHINE_ARMNT: "",
	}
	for machine, want := range cases {
		if got := machineArch(machine); got != want {
			t.Errorf("machineArch(%#x) = %q, want %q", machine, got, want)
		}
	}
}

func TestNativeArch(t *testing.T) {
	got := nativeArch()
	t.Logf("native arch: %s (vsenv is %s)", got, runtime.GOARCH)
	switch runtime.GOARCH {
	case "amd64":
		// Native x64, or x64 under emulation on ARM.
		if got != "x64" && got != "arm64" {
			t.Errorf("nativeArch() = %q for an amd64 build", got)
		}
	case "arm64":
		if got != "arm64" {
			t.Errorf("nativeArch() = %q for an arm64 build", got)
		}
	}
}
