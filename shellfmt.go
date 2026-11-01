package main

import (
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
	"unsafe"

	"golang.org/x/sys/windows"
)

// formatEnv writes dev as commands for shell. getenv returns the caller's
// variables: the output puts the VS entries around its PATH and other lists,
// so sourcing it twice (or in a nested shell) doesn't duplicate entries.
func formatEnv(shell string, dev devEnv, getenv func(string) string, w io.Writer) error {
	vars := dev.vars(getenv)
	keys := slices.Sorted(maps.Keys(vars))
	curPath := getenv("PATH")
	path := dev.path(curPath)
	// Left alone when unchanged, which spares bash and fish a cygpath run. The
	// entries are compared cleaned, since MSYS2 shells clean them.
	setPath := !samePath(path, curPath)

	switch strings.ToLower(shell) {
	case "powershell", "pwsh":
		for _, k := range keys {
			fmt.Fprintf(w, "$env:%s = %s\n", k, psQuote(vars[k]))
		}
		if setPath {
			fmt.Fprintf(w, "$env:Path = %s\n", psQuote(path))
		}
	case "cmd":
		var out []byte
		var lost []string
		set := func(k, v string) {
			line, lossy := consoleBytes(w, fmt.Sprintf("set \"%s=%s\"\n", k, v))
			out = append(out, line...)
			if lossy {
				lost = append(lost, k)
			}
		}
		// Values the caller has already are left alone, as cmd would read them
		// back with the characters its code page lacks replaced.
		for _, k := range keys {
			if getenv(k) != vars[k] {
				set(k, vars[k])
			}
		}
		if setPath {
			set("Path", path)
		}
		// On stderr, so for /f doesn't run it as a command.
		if len(lost) > 0 {
			fmt.Fprintf(os.Stderr, "vsenv: warning: the console's code page lacks some characters in %s, "+
				"so cmd gets ? for them; run \"chcp 65001\" first to keep them\n", strings.Join(lost, ", "))
		}
		_, err := w.Write(out)
		return err
	case "bash", "sh", "zsh":
		// PATH in MSYS2 and Git Bash holds POSIX paths. vsenv gets it
		// in Windows form, and cygpath converts the result back using the
		// shell's own mount table; if that fails, PATH stays as it was.
		for _, k := range keys {
			fmt.Fprintf(w, "export %s=%s\n", k, shQuote(vars[k]))
		}
		if setPath {
			fmt.Fprintf(w, "export PATH=\"$(cygpath -u -p %s || echo \"$PATH\")\"\n", shQuote(path))
		}
	case "fish":
		for _, k := range keys {
			fmt.Fprintf(w, "set -gx %s %s\n", k, fishQuote(vars[k]))
		}
		if setPath {
			fmt.Fprintf(w, "if set -l p (cygpath -u -p %s); set -gx PATH (string split : -- $p); end\n",
				fishQuote(path))
		}
	case "nu", "nushell":
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

const (
	cpUTF8 = 65001
	// wcNoBestFitChars makes WideCharToMultiByte write ? for a character the
	// code page lacks, rather than a lookalike (Ł as L) or a control character.
	wcNoBestFitChars = 0x400
)

// consoleBytes encodes s for w in the console's output code page, which cmd
// reads a program's output in, as for /f does. Characters the code page lacks
// become ?, and it reports whether there were any. s stays UTF-8 when w is
// the console itself (which Go writes to in UTF-16), without a console, or
// with a UTF-8 one.
func consoleBytes(w io.Writer, s string) ([]byte, bool) {
	if f, ok := w.(*os.File); ok {
		var mode uint32
		if windows.GetConsoleMode(windows.Handle(f.Fd()), &mode) == nil {
			return []byte(s), false
		}
	}
	cp, err := windows.GetConsoleOutputCP()
	if err != nil || cp == cpUTF8 {
		return []byte(s), false
	}
	if b, lossy := encodeCodePage(s, cp); b != nil {
		return b, lossy
	}
	return []byte(s), false
}

// encodeCodePage encodes s in code page cp, or returns nil if it can't. It
// also reports whether cp lacks some of s's characters.
func encodeCodePage(s string, cp uint32) ([]byte, bool) {
	units := utf16.Encode([]rune(s))
	if len(units) == 0 {
		return nil, false
	}
	proc := windows.NewLazySystemDLL("kernel32.dll").NewProc("WideCharToMultiByte")
	convert := func(flags uint32, buf []byte) int {
		n, _, _ := proc.Call(uintptr(cp), uintptr(flags),
			uintptr(unsafe.Pointer(&units[0])), uintptr(len(units)),
			uintptr(unsafe.Pointer(unsafe.SliceData(buf))), uintptr(len(buf)), 0, 0)
		return int(n)
	}
	// Some code pages, like GB18030, don't take the flag.
	flags := uint32(wcNoBestFitChars)
	n := convert(flags, nil)
	if n == 0 {
		flags = 0
		n = convert(flags, nil)
	}
	if n == 0 {
		return nil, false
	}
	buf := make([]byte, n)
	buf = buf[:convert(flags, buf)]
	return buf, !slices.Equal(decodeCodePage(buf, cp), units)
}

// decodeCodePage decodes b from code page cp, as cmd reads it.
func decodeCodePage(b []byte, cp uint32) []uint16 {
	if len(b) == 0 {
		return nil
	}
	nb := int32(len(b)) //nolint:gosec // G115: environment values, far under 2 GiB
	n, err := windows.MultiByteToWideChar(cp, 0, &b[0], nb, nil, 0)
	if err != nil {
		return nil
	}
	u := make([]uint16, n)
	if _, err = windows.MultiByteToWideChar(cp, 0, &b[0], nb, &u[0], n); err != nil {
		return nil
	}
	return u
}

func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// fishQuote quotes for fish, where \\ and \' are escapes even inside single
// quotes, so a value ending in a backslash would otherwise eat the quote.
func fishQuote(s string) string {
	return "'" + strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(s) + "'"
}
