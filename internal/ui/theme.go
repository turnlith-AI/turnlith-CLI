// Package ui is the terminal output of tlc: colors, line rendering, sanitizing
// of server-supplied text, and the readline-backed prompts used by the
// interactive setup flows.
package ui

import (
	"os"
	"strings"
)

// Theme holds the ANSI styling helpers. When colors are disabled every
// method returns its input unchanged.
type Theme struct {
	Colors bool
}

// NewTheme styles output for stdout.
func NewTheme() *Theme { return NewThemeFor(os.Stdout) }

// NewThemeFor decides whether to colorize output written to f: enabled unless
// NO_COLOR is set, the terminal is dumb, or f is not a terminal.
func NewThemeFor(f *os.File) *Theme {
	if _, set := os.LookupEnv("NO_COLOR"); set {
		return &Theme{}
	}
	if strings.EqualFold(os.Getenv("TERM"), "dumb") {
		return &Theme{}
	}
	return &Theme{Colors: IsTerminal(f)}
}

// IsTerminal reports whether f is an interactive terminal (a character
// device) rather than a pipe, a file or the null device — which is a character
// device too, and must never be mistaken for somebody to prompt.
func IsTerminal(f *os.File) bool {
	info, err := f.Stat()
	if err != nil || info.Mode()&os.ModeCharDevice == 0 {
		return false
	}
	if null, err := os.Stat(os.DevNull); err == nil && os.SameFile(info, null) {
		return false
	}
	return true
}

const (
	codeAccent = "38;5;209" // turnlith vermilion (#ff875f)
	codeDim    = "38;5;245"
	codeRed    = "38;5;203"
	codeGreen  = "38;5;114"
	codeYellow = "38;5;221"
	codeBold   = "1"
)

func (t *Theme) wrap(code, s string) string {
	if !t.Colors || s == "" {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

func (t *Theme) Accent(s string) string { return t.wrap(codeAccent, s) }
func (t *Theme) Dim(s string) string    { return t.wrap(codeDim, s) }
func (t *Theme) Red(s string) string    { return t.wrap(codeRed, s) }
func (t *Theme) Green(s string) string  { return t.wrap(codeGreen, s) }
func (t *Theme) Yellow(s string) string { return t.wrap(codeYellow, s) }
func (t *Theme) Bold(s string) string   { return t.wrap(codeBold, s) }

// GlyphPrompt is the input prompt marker.
const GlyphPrompt = "❯"
