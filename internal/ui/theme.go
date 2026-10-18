// Package ui renders the terminal experience: streaming styled output, tool
// call blocks, spinners, and the readline-backed prompt.
package ui

import (
	"fmt"
	"os"
	"strings"
)

// Theme holds the ANSI styling helpers. When colors are disabled every
// method returns its input unchanged.
type Theme struct {
	Colors bool
}

// NewTheme decides whether to colorize: enabled unless NO_COLOR is set, the
// terminal is dumb, or output is not a tty.
func NewTheme() *Theme {
	if _, set := os.LookupEnv("NO_COLOR"); set {
		return &Theme{Colors: false}
	}
	if strings.EqualFold(os.Getenv("TERM"), "dumb") {
		return &Theme{Colors: false}
	}
	if !isTerminal(os.Stdout) {
		return &Theme{Colors: false}
	}
	return &Theme{Colors: true}
}

func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// IsTerminal reports whether the process has an interactive stdout, used to
// pick interactive vs print mode defaults.
func IsTerminal(f *os.File) bool { return isTerminal(f) }

const (
	codeAccent = "38;5;209" // turnlith vermilion (#ff875f)
	codeViolet = "38;5;141" // secondary accents
	codeDim    = "38;5;245"
	codeFaint  = "38;5;240"
	codeRed    = "38;5;203"
	codeGreen  = "38;5;114"
	codeYellow = "38;5;221"
	codeGray   = "38;5;250"
	codeCode   = "38;5;245"
	codeBold   = "1"
)

func (t *Theme) wrap(code, s string) string {
	if !t.Colors || s == "" {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

func (t *Theme) Accent(s string) string { return t.wrap(codeAccent, s) }
func (t *Theme) Violet(s string) string { return t.wrap(codeViolet, s) }
func (t *Theme) Dim(s string) string    { return t.wrap(codeDim, s) }
func (t *Theme) Faint(s string) string  { return t.wrap(codeFaint, s) }
func (t *Theme) Red(s string) string    { return t.wrap(codeRed, s) }
func (t *Theme) Green(s string) string  { return t.wrap(codeGreen, s) }
func (t *Theme) Yellow(s string) string { return t.wrap(codeYellow, s) }
func (t *Theme) Gray(s string) string   { return t.wrap(codeGray, s) }
func (t *Theme) Code(s string) string   { return t.wrap(codeCode, s) }
func (t *Theme) Bold(s string) string   { return t.wrap(codeBold, s) }

func (t *Theme) BoldAccent(s string) string {
	if !t.Colors || s == "" {
		return s
	}
	return "\x1b[1;38;5;209m" + s + "\x1b[0m"
}

// Glyphs used in the transcript, matching the harness vocabulary.
const (
	GlyphTool   = "⏺"
	GlyphResult = "⎿"
	GlyphPrompt = ">"
)

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// Spinner prints a single-line animation while the model is working.
type Spinner struct {
	renderer *Renderer
	msg      string
	stop     chan struct{}
	done     chan struct{}
}

func (r *Renderer) StartSpinner(msg string) *Spinner {
	if !r.Theme.Colors || !r.spinnerEnabled {
		return nil
	}
	sp := &Spinner{renderer: r, msg: msg, stop: make(chan struct{}), done: make(chan struct{})}
	go sp.loop()
	return sp
}

func (s *Spinner) loop() {
	defer close(s.done)
	i := 0
	for {
		frame := s.renderer.Theme.Accent(spinnerFrames[i%len(spinnerFrames)])
		s.renderer.mu.Lock()
		fmt.Fprintf(s.renderer.Out, "\r\x1b[K%s %s", frame, s.renderer.Theme.Dim(s.msg))
		s.renderer.mu.Unlock()
		i++
		select {
		case <-s.stop:
			return
		case <-timeAfter(100):
		}
	}
}

func (s *Spinner) Stop() {
	if s == nil {
		return
	}
	select {
	case <-s.stop:
		return
	default:
		close(s.stop)
	}
	<-s.done
	s.renderer.mu.Lock()
	fmt.Fprint(s.renderer.Out, "\r\x1b[K")
	s.renderer.mu.Unlock()
}
