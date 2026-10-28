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

// ToolResult prints a tool's outcome below its call: a one-line summary, a
// colored diff, or the head and tail of command output (see summarizeResult).
func (r *Renderer) ToolResult(name, result, display string, denied bool, elapsed time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, line := range summarizeResult(name, result, display, denied) {
		prefix := strings.Repeat(" ", 5)
		if i == 0 {
			prefix = "  " + r.Theme.Dim(GlyphResult) + "  "
		}
		fmt.Fprintf(r.Out, "%s%s\n", prefix, r.styleResult(line))
	}
	if elapsed > 1200*time.Millisecond {
		fmt.Fprintf(r.Out, "%s%s\n", strings.Repeat(" ", 5), r.Theme.Faint(fmt.Sprintf("(%s)", elapsed.Round(100*time.Millisecond))))
	}
	r.lastByte = '\n'
}

func (r *Renderer) styleResult(l resultLine) string {
	switch l.Kind {
	case kindErr, kindDel:
		return r.Theme.Red(l.Text)
	case kindAdd:
		return r.Theme.Green(l.Text)
	case kindWarn:
		return r.Theme.Yellow(l.Text)
	case kindFaint, kindDone:
		return r.Theme.Faint(l.Text)
	case kindActive:
		return r.Theme.Accent(l.Text)
	case kindSummary:
		return r.Theme.Gray(l.Text)
	case kindPlain:
		return l.Text
	default:
		return r.Theme.Dim(l.Text)
	}
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

// RunSummary prints the one-line cost of a finished prompt: context size, tokens
// generated, turns taken, wall time.
func (r *Renderer) RunSummary(contextTokens, outputTokens, turns int, elapsed time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.lastByte != 0 && r.lastByte != '\n' {
		r.writeLocked("\n")
	}
	r.writeLocked(r.Theme.Faint("· "+formatRunSummary(contextTokens, outputTokens, turns, elapsed)) + "\n")
}

// formatRunSummary renders "4.2k context · 87 out · 2 turns · 6.3s", leaving
// out what is zero or unremarkable (a single turn).
func formatRunSummary(contextTokens, outputTokens, turns int, elapsed time.Duration) string {
	var parts []string
	if contextTokens > 0 {
		parts = append(parts, compactTokens(contextTokens)+" context")
	}
	if outputTokens > 0 {
		parts = append(parts, compactTokens(outputTokens)+" out")
	}
	if turns > 1 {
		parts = append(parts, fmt.Sprintf("%d turns", turns))
	}
	parts = append(parts, compactDuration(elapsed))
	return strings.Join(parts, " · ")
}

func compactTokens(n int) string {
	if n >= 1000 {
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprintf("%d", n)
}

func compactDuration(d time.Duration) string {
	if d >= time.Minute {
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%.1fs", d.Seconds())
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

// PermissionView is everything the permission prompt shows.
type PermissionView struct {
	Tool, Summary, Preview string
	// Note is a warning line (e.g. why the call is outside the workspace).
	Note string
	// Rule is the rule an "always" answer would remember (empty: none).
	Rule string
	// Remember says whether a/x (session) are offered; Project whether p is.
	Remember, Project bool
}

// permissionOptions renders the answer legend for a prompt.
func (r *Renderer) permissionOptions(v PermissionView) string {
	parts := []string{"[y] allow"}
	if v.Remember {
		always := "[a] always"
		if v.Rule != "" {
			always += " " + v.Rule
		} else {
			always += " (outside the workspace)"
		}
		parts = append(parts, always)
	}
	if v.Project {
		parts = append(parts, "[p] always in this project")
	}
	parts = append(parts, "[n] deny")
	if v.Remember {
		parts = append(parts, "[x] never")
	}
	return r.Theme.Dim(strings.Join(parts, " · "))
}

// PermissionBlock renders what a gated call is about to do — a warning if it
// is unusual, the diff-colored preview, and the answer options — below the
// "⏺ Tool(summary)" line the transcript has already printed for it, before the
// REPL reads the one-key answer.
func (r *Renderer) PermissionBlock(v PermissionView) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.lastByte != 0 && r.lastByte != '\n' {
		r.writeLocked("\n")
	}
	if v.Note != "" {
		r.writeLocked("    " + r.Theme.Yellow("⚠ "+sanitizeInline(v.Note)) + "\n")
	}
	if v.Preview != "" {
		for _, line := range strings.Split(sanitizeBlock(v.Preview), "\n") {
			r.writeLocked("    " + stylePreviewLine(r, line) + "\n")
		}
	}
	r.writeLocked("  " + r.permissionOptions(v) + "\n")
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
