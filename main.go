package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime/debug"

	"github.com/urfave/cli/v3"
)

const shellFlagName = "shell"

var version = "dev" // injected at build time via -ldflags "-X main.version=..."

// resolveVersion returns the effective version string: an explicit ldflags
// override wins; otherwise the module version stamped by the Go toolchain
// (set for 'go install module@version' and, since Go 1.24, for VCS builds);
// otherwise ldflagsVersion as-is.
func resolveVersion(ldflagsVersion string) string {
	if ldflagsVersion != "dev" {
		return ldflagsVersion
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return ldflagsVersion
}

func main() {
	cmd := &cli.Command{
		Name:    "vsenv",
		Usage:   "Visual Studio developer environment for any shell",
		Version: resolveVersion(version),
		Commands: []*cli.Command{
			listCmd(),
			refreshCmd(),
			execCmd(),
			shellCmd(),
			envCmd(),
		},
	}

	if err := cmd.Run(context.Background(), os.Args); err != nil {
		fmt.Fprintln(os.Stderr, "vsenv:", err)
		os.Exit(1)
	}
}

func archFlags() []cli.Flag {
	native := nativeArch()
	return []cli.Flag{
		&cli.StringFlag{
			Name:  "arch",
			Usage: "target architecture (x64|x86|arm64|arm)",
			Value: native,
		},
		&cli.StringFlag{
			Name:  "host-arch",
			Usage: "host architecture (x64|x86|arm64)",
			Value: native,
		},
		&cli.StringFlag{
			Name:  "instance",
			Usage: "VS instance ID (default: newest install with MSVC)",
		},
		&cli.StringFlag{
			Name:  "dev-cmd-args",
			Usage: "raw arguments forwarded to VsDevCmd.bat",
		},
	}
}

func listCmd() *cli.Command {
	return &cli.Command{
		Name:  "list",
		Usage: "List discovered Visual Studio installations",
		Action: func(ctx context.Context, _ *cli.Command) error {
			installs, err := DiscoverInstalls(ctx, false)
			if err != nil {
				return err
			}
			if len(installs) == 0 {
				fmt.Fprintln(os.Stdout, "No Visual Studio installations found.")
				return nil
			}
			for _, i := range installs {
				notes := ""
				if i.IsPrerelease {
					notes += " (prerelease)"
				}
				if !i.IsComplete {
					notes += " (incomplete)"
				}
				fmt.Fprintf(os.Stdout, "%s  %s  %s%s\n  %s\n",
					i.InstanceID, i.InstallationVersion, i.DisplayName, notes, i.InstallationPath)
			}
			return nil
		},
	}
}

func refreshCmd() *cli.Command {
	return &cli.Command{
		Name:  "refresh",
		Usage: "Force-rebuild the install and env caches",
		Action: func(ctx context.Context, _ *cli.Command) error {
			if _, err := DiscoverInstalls(ctx, true); err != nil {
				return err
			}
			if err := ClearEnvCache(); err != nil {
				return err
			}
			fmt.Fprintln(os.Stdout, "Caches cleared.")
			return nil
		},
	}
}

func execCmd() *cli.Command {
	return &cli.Command{
		Name:      "exec",
		Usage:     "Run a command with VS environment loaded",
		ArgsUsage: "-- command [args...]",
		Flags:     archFlags(),
		Action: func(ctx context.Context, c *cli.Command) error {
			args := c.Args().Slice()
			if len(args) == 0 {
				return errors.New("no command given (use: vsenv exec [flags] -- cmd args)")
			}
			env, err := loadEnv(ctx, c)
			if err != nil {
				return err
			}
			return execChild(ctx, args, env)
		},
	}
}

func shellCmd() *cli.Command {
	return &cli.Command{
		Name:  "shell",
		Usage: "Spawn an interactive child shell with VS environment loaded",
		Flags: append(archFlags(),
			&cli.StringFlag{
				Name:  shellFlagName,
				Usage: "shell to launch (cmd|powershell|pwsh|nu|bash|fish) or a path to its executable; auto-detect if empty",
			},
		),
		Action: func(ctx context.Context, c *cli.Command) error {
			env, err := loadEnv(ctx, c)
			if err != nil {
				return err
			}
			return spawnShell(ctx, c.String(shellFlagName), env)
		},
	}
}

func envCmd() *cli.Command {
	return &cli.Command{
		Name:  "env",
		Usage: "Print VS environment in a shell-specific format",
		Flags: append(archFlags(),
			&cli.StringFlag{
				Name:     shellFlagName,
				Usage:    "output format: powershell|cmd|nu|bash|fish",
				Required: true,
			},
		),
		Action: func(ctx context.Context, c *cli.Command) error {
			env, err := loadEnv(ctx, c)
			if err != nil {
				return err
			}
			return formatEnv(c.String(shellFlagName), env, os.Getenv, os.Stdout)
		},
	}
}

func loadEnv(ctx context.Context, c *cli.Command) (devEnv, error) {
	install, err := SelectInstall(ctx, c.String("instance"))
	if err != nil {
		return devEnv{}, err
	}
	return GetEnv(ctx, install, c.String("arch"), c.String("host-arch"), c.String("dev-cmd-args"))
}
