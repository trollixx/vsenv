package main

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

func formatEnv(shell string, env map[string]string, w io.Writer) error {
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	switch strings.ToLower(shell) {
	case "powershell", "pwsh":
		for _, k := range keys {
			fmt.Fprintf(w, "$env:%s = %s\n", k, psQuote(env[k]))
		}
	case "cmd", "bat":
		for _, k := range keys {
			fmt.Fprintf(w, "set \"%s=%s\"\n", k, env[k])
		}
	case "bash", "sh", "zsh":
		for _, k := range keys {
			fmt.Fprintf(w, "export %s=%s\n", k, shQuote(env[k]))
		}
	case "fish":
		for _, k := range keys {
			fmt.Fprintf(w, "set -gx %s %s\n", k, fishQuote(env[k]))
		}
	case "nu", "nushell":
		return json.NewEncoder(w).Encode(env)
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
