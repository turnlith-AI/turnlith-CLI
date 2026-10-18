package ui

import (
	"strings"
	"time"
)

func timeAfter(d time.Duration) <-chan time.Time { return time.After(d) }

// Styler is a tiny streaming markdown renderer. It tracks fenced code blocks,
// inline code, and bold spans while text streams in; fenced code never gets
// inline markdown processing. It holds at most the current line-start
// decision, so normal prose streams token by token.
type Styler struct {
	theme *Theme

	mode        styleMode
	lineStart   bool // no visible output emitted for the current line yet
	held        []byte
	lineBuf     []byte
	fenceOpened bool // inside the fence's opening line (info string)
	inline      bool
	bold        bool
	starHold    int
}

type styleMode int

const (
	modeNormal styleMode = iota
	modeFence
)

func NewStyler(t *Theme) *Styler {
	return &Styler{theme: t, lineStart: true}
}

// Write consumes a text delta and returns the styled output.
func (s *Styler) Write(delta string) string {
	var out strings.Builder
	for _, r := range delta {
		s.step(r, &out)
	}
	return out.String()
}

// Flush emits anything held back (end of an assistant turn).
func (s *Styler) Flush() string {
	var out strings.Builder
	s.flushStars(&out)
	if s.mode == modeFence {
		if len(s.lineBuf) > 0 {
			if s.fenceOpened {
				out.WriteString(s.theme.Dim(string(s.lineBuf)))
			} else {
				out.WriteString(s.theme.Code(string(s.lineBuf)))
			}
			s.lineBuf = s.lineBuf[:0]
		}
	} else if len(s.held) > 0 {
		out.Write(s.held)
		s.held = s.held[:0]
		s.lineStart = false
	}
	if s.bold {
		s.boldOff(&out)
	}
	s.inline = false
	s.bold = false
	return out.String()
}

// Reset forgets state between turns.
func (s *Styler) Reset() {
	s.mode = modeNormal
	s.lineStart = true
	s.held = s.held[:0]
	s.lineBuf = s.lineBuf[:0]
	s.fenceOpened = false
	s.inline = false
	s.bold = false
	s.starHold = 0
}

func (s *Styler) step(r rune, out *strings.Builder) {
	if s.mode == modeFence {
		s.fenceStep(r, out)
		return
	}
	if r == '\n' {
		s.endLine(out)
		return
	}
	if s.lineStart {
		s.lineStartStep(r, out)
		return
	}
	s.inlineStep(r, out)
}

// lineStartStep decides whether the line opens a code fence. It holds back
// leading spaces and up to two backticks; anything else flushes and streams.
func (s *Styler) lineStartStep(r rune, out *strings.Builder) {
	switch {
	case r == '`':
		s.held = append(s.held, '`')
		if len(s.held) == 3 {
			out.WriteString(s.theme.Dim(string(s.held)))
			s.held = s.held[:0]
			s.mode = modeFence
			s.fenceOpened = true
			s.lineStart = false
		}
		return
	case r == ' ' && len(s.held) < 8:
		s.held = append(s.held, ' ')
		return
	default:
		if len(s.held) > 0 {
			out.Write(s.held)
			s.held = s.held[:0]
		}
		s.lineStart = false
		s.inlineStep(r, out)
	}
}

// fenceStep buffers fence lines wholesale: the opening line's info string is
// dimmed; body lines are code-colored; a line that is just ``` closes it.
func (s *Styler) fenceStep(r rune, out *strings.Builder) {
	if r != '\n' {
		s.lineBuf = append(s.lineBuf, string(r)...)
		return
	}
	line := string(s.lineBuf)
	s.lineBuf = s.lineBuf[:0]
	switch {
	case s.fenceOpened:
		out.WriteString(s.theme.Dim(line))
		out.WriteByte('\n')
		s.fenceOpened = false
	case strings.HasPrefix(strings.TrimSpace(line), "```"):
		out.WriteString(s.theme.Dim(line))
		out.WriteByte('\n')
		s.mode = modeNormal
		s.lineStart = true
	default:
		out.WriteString(s.theme.Code(line))
		out.WriteByte('\n')
	}
}

func (s *Styler) endLine(out *strings.Builder) {
	s.flushStars(out)
	s.lineStart = true
	s.inline = false
	if s.bold {
		s.boldOff(out)
		s.bold = false
	}
	out.WriteByte('\n')
}

// inlineStep styles a streamed line: `code`, **bold**.
func (s *Styler) inlineStep(r rune, out *strings.Builder) {
	switch r {
	case '`':
		s.flushStars(out)
		s.inline = !s.inline
		out.WriteRune(r)
	case '*':
		s.starHold++
		if s.starHold == 2 {
			s.starHold = 0
			s.toggleBold(out)
		}
	default:
		s.flushStars(out)
		switch {
		case s.inline:
			out.WriteString(s.theme.Code(string(r)))
		case s.bold:
			out.WriteString(s.theme.Bold(string(r)))
		default:
			out.WriteRune(r)
		}
	}
}

// toggleBold switches bold on/off; the ** markers themselves are swallowed.
func (s *Styler) toggleBold(out *strings.Builder) {
	if s.bold {
		s.bold = false
		s.boldOff(out)
	} else {
		s.bold = true
		s.boldOn(out)
	}
}

// boldOn/boldOff emit the escape sequences only when colors are enabled.
func (s *Styler) boldOn(out *strings.Builder) {
	if s.theme.Colors {
		out.WriteString("\x1b[1m")
	}
}

func (s *Styler) boldOff(out *strings.Builder) {
	if s.theme.Colors {
		out.WriteString("\x1b[0m")
	}
}

// flushStars emits pending single asterisks that never became a bold pair.
func (s *Styler) flushStars(out *strings.Builder) {
	for ; s.starHold > 0; s.starHold-- {
		out.WriteByte('*')
	}
}
