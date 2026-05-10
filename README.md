# vsenv

Visual Studio environment activator that works in cmd, PowerShell, nushell, bash, and fish.

`vcvars*.bat` and `Enter-VsDevShell` only work in cmd and PowerShell. `vsenv` provides the same environment to any shell by capturing it once and reading from cache thereafter.

## Build

```
go build -o vsenv.exe
```

Single static binary, no CGO.

## Usage

```
vsenv list                                       # show discovered VS installs
vsenv refresh                                    # rebuild caches
vsenv exec [--arch x64] -- <cmd> [args...]       # run one command in VS env
vsenv shell [--arch x64] [--shell nu]            # interactive child shell
vsenv env --shell powershell|cmd|nu|bash|fish    # print env for profile
```

`exec` and `shell` set the env in a child process; the parent shell stays clean. `env` prints shell-flavored commands you can source from your profile.

## Profile integration

PowerShell (`$PROFILE`):
```powershell
vsenv env --shell powershell | Out-String | Invoke-Expression
```

Nushell (`config.nu`):
```nu
load-env (vsenv env --shell nu | from json)
```

Bash (`.bashrc`):
```bash
eval "$(vsenv env --shell bash)"
```

Fish (`config.fish`):
```fish
vsenv env --shell fish | source
```

## How it works

`vsenv` runs `vswhere.exe` to find Visual Studio installations and caches the result. For each `(install, arch, host-arch, args)` tuple it invokes `VsDevCmd.bat` once, captures the env diff, and writes it to `%LOCALAPPDATA%\vsenv\`. Subsequent invocations read the cache (~20 ms). The cache invalidates when the VS install version changes; `vsenv refresh` forces a rebuild.

`VsDevCmd.bat` is the same script Microsoft's `Enter-VsDevShell` cmdlet wraps internally — `vsenv` is that, minus the PowerShell-only constraint and the telemetry.

## Requirements

- Windows
- Visual Studio 2017+ or VS Build Tools (anything `vswhere.exe` can find)
