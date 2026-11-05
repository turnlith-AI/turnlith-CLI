package ui

import (
	"fmt"
	"io"
	"strings"
	"sync"
)

// Renderer writes whole lines of human-facing output to one stream.
type Renderer struct {
	Out   io.Writer
	Theme *Theme

	mu sync.Mutex
}

// NewRenderer returns a renderer writing to out.
func NewRenderer(out io.Writer, theme *Theme) *Renderer {
	return &Renderer{Out: out, Theme: theme}
}

func (r *Renderer) line(s string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	fmt.Fprintln(r.Out, s)
}

// Notice prints a dim informational line.
func (r *Renderer) Notice(msg string) { r.line(r.Theme.Dim("· " + msg)) }

// Infof prints a plain line.
func (r *Renderer) Infof(format string, args ...any) { r.line(fmt.Sprintf(format, args...)) }

// Errorf prints a red error line.
func (r *Renderer) Errorf(format string, args ...any) {
	r.line(r.Theme.Red("✖ ") + fmt.Sprintf(format, args...))
}

// SanitizeBlock removes control characters — escape sequences included — from
// text that came from outside tlc (a server's error message, an agent label)
// before it reaches the terminal, keeping newlines and tabs. Without it a
// hostile server could rewrite the screen, set the window title or hide text.
func SanitizeBlock(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '\n' || r == '\t':
			b.WriteRune(r)
		case r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f):
			// drop other control characters, C1 included (0x9b is a CSI)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// SanitizeInline is SanitizeBlock for a single line: newlines and tabs become
// spaces.
func SanitizeInline(s string) string {
	return SanitizeBlock(strings.NewReplacer("\n", " ", "\r", " ", "\t", " ").Replace(s))
}
