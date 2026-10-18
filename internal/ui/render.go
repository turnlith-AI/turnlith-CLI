package ui

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

// Renderer writes the transcript: streamed assistant text, tool call blocks,
// notices, and usage lines.
type Renderer struct {
	Out   io.Writer
	Theme *Theme

	styler *Styler
	mu     sync.Mutex

	spinnerEnabled bool
	lastByte       byte
	wroteText      bool
}

func NewRenderer(out io.Writer, theme *Theme) *Renderer {
	return &Renderer{Out: out, Theme: theme, styler: NewStyler(theme), spinnerEnabled: true}
}

// DisableSpinner is used by non-interactive output.
func (r *Renderer) DisableSpinner() { r.spinnerEnabled = false }

func (r *Renderer) writeLocked(s string) {
	fmt.Fprint(r.Out, s)
	if len(s) > 0 {
		r.lastByte = s[len(s)-1]
	}
}

// Text streams a styled assistant text delta.
func (r *Renderer) Text(delta string) {
	styled := r.styler.Write(delta)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.writeLocked(styled)
	r.wroteText = true
}

// EndText flushes the styler and ends the line.
func (r *Renderer) EndText() {
	styled := r.styler.Flush()
	r.styler.Reset()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.writeLocked(styled)
	if r.wroteText && r.lastByte != '\n' {
		r.writeLocked("\n")
	}
	r.wroteText = false
}

// EnsureBreak starts output on a fresh line if needed.
func (r *Renderer) EnsureBreak() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.lastByte != 0 && r.lastByte != '\n' {
		r.writeLocked("\n")
	}
}

// ToolStart prints the assistant's tool invocation line.
func (r *Renderer) ToolStart(name, summary string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.lastByte != 0 && r.lastByte != '\n' {
		r.writeLocked("\n")
	}
	line := r.Theme.Accent(GlyphTool) + " " + r.Theme.Bold(name)
	if summary != "" {
		line += r.Theme.Dim("(" + sanitizeInline(summary) + ")")
	}
	r.writeLocked(line + "\n")
}

// ToolResult prints the (display-truncated) tool result below its call.
func (r *Renderer) ToolResult(result string, denied bool, elapsed time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	indent := "  " + r.Theme.Dim(GlyphResult) + "  "

	var lines []string
	if denied {
		lines = []string{r.Theme.Yellow("denied by the user")}
	} else {
		lines = strings.Split(sanitizeBlock(result), "\n")
	}
	const maxLines = 5
	shown := lines
	if len(shown) > maxLines {
		shown = shown[:maxLines]
	}
	for i, line := range shown {
		if i == 0 {
			fmt.Fprintf(r.Out, "%s%s\n", indent, styleResultLine(r, line))
		} else {
			fmt.Fprintf(r.Out, "%s%s\n", strings.Repeat(" ", 5), styleResultLine(r, line))
		}
	}
	if len(lines) > maxLines {
		fmt.Fprintf(r.Out, "%s%s\n", strings.Repeat(" ", 5), r.Theme.Faint(fmt.Sprintf("… (+%d more lines)", len(lines)-maxLines)))
	}
	if elapsed > 1200*time.Millisecond {
		fmt.Fprintf(r.Out, "%s%s\n", strings.Repeat(" ", 5), r.Theme.Faint(fmt.Sprintf("(%s)", elapsed.Round(100*time.Millisecond))))
	}
	r.lastByte = '\n'
}

func styleResultLine(r *Renderer, line string) string {
	if strings.HasPrefix(line, "Error:") {
		return r.Theme.Red(line)
	}
	return r.Theme.Dim(line)
}

// Notice prints a dim informational line.
func (r *Renderer) Notice(msg string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.lastByte != 0 && r.lastByte != '\n' {
		r.writeLocked("\n")
	}
	r.writeLocked(r.Theme.Dim("· "+msg) + "\n")
}

// Infof prints a plain line (banner-ish).
func (r *Renderer) Infof(format string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.lastByte != 0 && r.lastByte != '\n' {
		r.writeLocked("\n")
	}
	r.writeLocked(fmt.Sprintf(format, args...) + "\n")
}

// Errorf prints a red error line.
func (r *Renderer) Errorf(format string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.lastByte != 0 && r.lastByte != '\n' {
		r.writeLocked("\n")
	}
	r.writeLocked(r.Theme.Red("✖ ") + fmt.Sprintf(format, args...) + "\n")
}

// UsageLine reports token accounting after a turn (verbose mode).
func (r *Renderer) UsageLine(contextTokens, outputTokens int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.writeLocked(r.Theme.Faint(fmt.Sprintf("· %s context · %s output", humanTokens(contextTokens), humanTokens(outputTokens))) + "\n")
	r.lastByte = '\n'
}

func humanTokens(n int) string {
	switch {
	case n >= 1000:
		return fmt.Sprintf("%.1fk tokens", float64(n)/1000)
	case n == 1:
		return "1 token"
	default:
		return fmt.Sprintf("%d tokens", n)
	}
}

// PermissionBlock renders a gated tool call's summary, preview, and options
// before the REPL reads the answer.
func (r *Renderer) PermissionBlock(name string, preview string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.lastByte != 0 && r.lastByte != '\n' {
		r.writeLocked("\n")
	}
	if preview != "" {
		for _, line := range strings.Split(sanitizeBlock(preview), "\n") {
			r.writeLocked("  " + stylePreviewLine(r, line) + "\n")
		}
	}
	r.writeLocked("  " + r.Theme.Dim("allow? [enter] yes · [a] always · [n] no · [x] never this session") + "\n")
	r.lastByte = '\n'
}

func stylePreviewLine(r *Renderer, line string) string {
	switch {
	case strings.HasPrefix(line, "-"):
		return r.Theme.Red(line)
	case strings.HasPrefix(line, "+"):
		return r.Theme.Green(line)
	case strings.HasPrefix(line, "…"):
		return r.Theme.Faint(line)
	default:
		return r.Theme.Dim(line)
	}
}

// sanitizeBlock removes control characters from tool output before display,
// keeping newlines and tabs.
func sanitizeBlock(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '\n' || r == '\t':
			b.WriteRune(r)
		case r < 0x20 || r == 0x7f:
			// drop other control characters
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// sanitizeInline is sanitizeBlock for single-line summaries.
func sanitizeInline(s string) string {
	s = strings.NewReplacer("\n", " ", "\r", " ", "\t", " ").Replace(s)
	return sanitizeBlock(s)
}
