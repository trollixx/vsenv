package main

import (
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
)

// formatEnv writes dev as commands for shell. curPath is the caller's PATH,
// which the output replaces with dev.path(curPath), so sourcing it twice (or
// in a nested shell) doesn't duplicate entries.
func formatEnv(shell string, dev devEnv, curPath string, w io.Writer) error {
	keys := slices.Sorted(maps.Keys(dev.Vars))
	path := dev.path(curPath)
	// Left alone when unchanged, which spares bash and fish a cygpath run.
	setPath := path != curPath

	switch strings.ToLower(shell) {
	case "powershell", "pwsh":
		for _, k := range keys {
			fmt.Fprintf(w, "$env:%s = %s\n", k, psQuote(dev.Vars[k]))
		}
		if setPath {
			fmt.Fprintf(w, "$env:Path = %s\n", psQuote(path))
		}
	case "cmd", "bat":
		for _, k := range keys {
			fmt.Fprintf(w, "set \"%s=%s\"\n", k, dev.Vars[k])
		}
		fmt.Fprintf(w, "set \"Path=%s\"\n", path)
	case "bash", "sh", "zsh":
		// PATH in MSYS2, Git Bash and Cygwin holds POSIX paths. vsenv gets it
		// in Windows form, and cygpath converts the result back using the
		// shell's own mount table; if that fails, PATH stays as it was.
		for _, k := range keys {
			fmt.Fprintf(w, "export %s=%s\n", k, shQuote(dev.Vars[k]))
		}
		if setPath {
			fmt.Fprintf(w, "export PATH=\"$(cygpath -u -p %s || echo \"$PATH\")\"\n", shQuote(path))
		}
	case "fish":
		for _, k := range keys {
			fmt.Fprintf(w, "set -gx %s %s\n", k, fishQuote(dev.Vars[k]))
		}
		if setPath {
			fmt.Fprintf(w, "if set -l p (cygpath -u -p %s); set -gx PATH (string split : -- $p); end\n",
				fishQuote(path))
		}
	case "nu", "nushell":
		vars := make(map[string]string, len(dev.Vars)+1)
		maps.Copy(vars, dev.Vars)
		vars["Path"] = path
		return json.NewEncoder(w).Encode(vars)
	default:
		return fmt.Errorf("unknown shell %q (supported: powershell, pwsh, cmd, bash, fish, nu)", shell)
	}
	return nil
}

func psQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// fishQuote quotes for fish, where \\ and \' are escapes even inside single
// quotes, so a value ending in a backslash would otherwise eat the quote.
func fishQuote(s string) string {
	return "'" + strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(s) + "'"
}
