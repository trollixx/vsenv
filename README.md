# vsenv

[![CI](https://img.shields.io/github/actions/workflow/status/trollixx/vsenv/ci.yml?branch=main&label=ci&style=flat-square)](https://github.com/trollixx/vsenv/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/trollixx/vsenv?style=flat-square)](https://github.com/trollixx/vsenv/releases)
[![License](https://img.shields.io/github/license/trollixx/vsenv?style=flat-square)](LICENSE)

Use the MSVC toolchain from any shell on Windows: PowerShell, cmd, nushell, Git Bash, MSYS2, or fish. No Developer Command Prompt required.

```sh
vsenv exec -- cmake --build build
```

## Why

`cl.exe`, `link.exe`, and the Windows SDK only work after `VsDevCmd.bat` (or `vcvarsall.bat`) sets about 40 environment variables. Since that's a cmd script, its changes can't reach other shells:

- **Developer Command Prompt / `vcvarsall.bat`**: cmd only.
- **`Enter-VsDevShell`**: PowerShell only, and runs `VsDevCmd.bat` (about a second) in every new shell.
- **`ilammy/msvc-dev-cmd`**: GitHub Actions only.

vsenv runs `VsDevCmd.bat` once, caches the result, and applies it to any shell in milliseconds.

## Install

Download `vsenv-amd64.exe` (or `vsenv-arm64.exe`) from the [latest release](https://github.com/trollixx/vsenv/releases/latest), rename it to `vsenv.exe`, and put it on your `PATH`. With the `gh` CLI:

```sh
gh release download --repo trollixx/vsenv --pattern vsenv-amd64.exe --output vsenv.exe
```

Or build it with Go:

```sh
go install github.com/trollixx/vsenv@latest
```

## Usage

Run one command with the VS environment:

```sh
vsenv exec -- cl -nologo hello.c
vsenv exec --arch arm64 -- cmake --build build-arm64
```

Start a child shell with the VS environment (`exit` to leave):

```sh
vsenv shell                      # same shell you're in
vsenv shell --shell pwsh         # or pick one: cmd, powershell, pwsh, nu, bash, fish
```

Either way, your current shell stays clean. To load the environment into your shell instead, see [Shell profile](#shell-profile).

| Command | What it does |
| --- | --- |
| `vsenv exec [flags] -- <cmd> [args...]` | Run one command in the VS environment |
| `vsenv shell [flags] [--shell <name or path>]` | Start an interactive child shell |
| `vsenv env [flags] --shell <name>` | Print the environment as commands for your shell |
| `vsenv list` | List Visual Studio installations and their instance IDs |
| `vsenv refresh` | Clear caches and rediscover installations |

`exec`, `shell`, and `env` accept:

| Flag | Default | |
| --- | --- | --- |
| `--arch` | `x64` | Target architecture: `x64`, `x86`, `arm64`, `arm` |
| `--host-arch` | `x64` | Architecture of the compiler binaries: `x64`, `x86`, `arm64` |
| `--instance` | newest install with MSVC | Instance ID from `vsenv list`; previews included |
| `--dev-cmd-args` | | Extra `VsDevCmd.bat` arguments, e.g. `"-vcvars_ver=14.39 -winsdk=10.0.22621.0"` |

Each architecture needs its MSVC build tools component installed (x64/x86 are installed by default; ARM64 isn't). vsenv reports an error if it's missing.

## Shell profile

To have the VS environment in every new shell, add the line for your shell to its profile. Once cached, this takes about 15 ms in PowerShell and about 70 ms in Git Bash, MSYS2, and Cygwin, where starting processes is slower.

PowerShell (`$PROFILE`):

```powershell
vsenv env --shell powershell | Out-String | Invoke-Expression
```

Nushell (`config.nu`):

```nu
load-env (vsenv env --shell nu | from json)
```

Git Bash, MSYS2, or Cygwin (`~/.bashrc`):

```bash
eval "$(vsenv env --shell bash)"
```

Fish (`config.fish`):

```fish
vsenv env --shell fish | source
```

cmd has no profile; run this from a batch file (at the prompt, use `%i` instead of `%%i`):

```bat
for /f "delims=" %%i in ('vsenv env --shell cmd') do @%%i
```

Running it again, or in a nested shell, doesn't add duplicate `PATH` entries, and running it with another `--arch` puts that architecture's tools first. For bash and fish, `PATH` is converted to POSIX paths with `cygpath`, which ships with Git for Windows, MSYS2, and Cygwin.

## How it works

vsenv finds Visual Studio with `vswhere.exe`. For each combination of install, architecture, and `--dev-cmd-args`, it runs `VsDevCmd.bat` once and records what changed: the variables it set, plus the entries it added before and after `PATH`, `INCLUDE`, `LIB`, and `LIBPATH`. The result is cached in `%LOCALAPPDATA%\vsenv\`.

`VsDevCmd.bat` runs in your default Windows environment (what programs started from the Start menu get), not the environment of the shell that ran vsenv. That way the cache comes out the same even if that shell already has the VS environment loaded.

Each later run applies those changes on top of your current values, so it keeps your own entries and picks up any changes made since the capture. The cache rebuilds automatically after Visual Studio is installed, updated, or modified in the Visual Studio Installer. `vsenv refresh` forces a rebuild.

`VsDevCmd.bat` normally launches PowerShell to send usage telemetry. vsenv sets `VSCMD_SKIP_SENDTELEMETRY` while it runs `VsDevCmd.bat` to skip that, and leaves the variable out of the environment it applies.

## Requirements

- Windows
- Visual Studio 2017 or later, or the Visual Studio Build Tools, with the C++ build tools (the "Desktop development with C++" workload)
