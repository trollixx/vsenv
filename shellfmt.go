package main

import (
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
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

// psQuote returns s as a PowerShell expression in ASCII, since PowerShell
// reads a program's output in the console code page, which can't hold every
// character. Other characters are written as [char] codes, which also covers
// the typographic quotes (as in a name like O’Neil) that would end a
// single-quoted string.
func psQuote(s string) string {
	var b strings.Builder
	var codes []string // the non-ASCII run being collected
	quoted := false    // inside a '...' string
	flush := func() {
		if len(codes) == 0 {
			return
		}
		if b.Len() == 0 {
			b.WriteString("''") // so + joins strings rather than adding numbers
		}
		// One term per run: PowerShell takes a long time over a long chain of
		// + terms, as a PATH with many non-ASCII names would make.
		if len(codes) == 1 {
			fmt.Fprintf(&b, " + [char]%s", codes[0])
		} else {
			fmt.Fprintf(&b, " + (-join [char[]](%s))", strings.Join(codes, ","))
		}
		codes = codes[:0]
	}
	for _, u := range utf16.Encode([]rune(s)) {
		if u >= utf8.RuneSelf {
			if quoted {
				b.WriteByte('\'')
				quoted = false
			}
			codes = append(codes, fmt.Sprintf("0x%04X", u))
			continue
		}
		flush()
		if !quoted {
			if b.Len() > 0 {
				b.WriteString(" + ")
			}
			b.WriteByte('\'')
			quoted = true
		}
		if u == '\'' {
			b.WriteByte('\'')
		}
		b.WriteRune(rune(u))
	}
	flush()
	switch {
	case quoted:
		b.WriteByte('\'')
	case b.Len() == 0:
		b.WriteString("''")
	}
	return b.String()
}

func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// fishQuote quotes for fish, where \\ and \' are escapes even inside single
// quotes, so a value ending in a backslash would otherwise eat the quote.
func fishQuote(s string) string {
	return "'" + strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(s) + "'"
}
