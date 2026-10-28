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
	lineStyle   lineStyle // heading or quote styling for the rest of the line
}

type lineStyle int

const (
	styleNone lineStyle = iota
	styleHeading
	styleQuote
)

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
		s.flushHeld(&out)
		s.lineStart = false
	}
	if s.bold {
		s.boldOff(&out)
	}
	s.inline = false
	s.bold = false
	s.lineStyle = styleNone
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
	s.lineStyle = styleNone
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

// lineStartStep decides what the line is before any of it is shown: a code
// fence (```), a heading (# … ######), a bullet (- * +), a quote (>), or plain
// text. It holds back only the possible marker — leading spaces plus up to six
// marker characters — and decides on the next character; anything else
// flushes the held text and streams normally.
func (s *Styler) lineStartStep(r rune, out *strings.Builder) {
	indent := 0
	for indent < len(s.held) && s.held[indent] == ' ' {
		indent++
	}
	marker := s.held[indent:]
	switch {
	case r == ' ' && len(marker) == 0 && indent < 8:
		s.held = append(s.held, ' ')
		return
	case r == '`' && allBytes(marker, '`') && len(marker) < 3:
		s.held = append(s.held, '`')
		if len(marker)+1 == 3 {
			out.WriteString(s.theme.Dim(string(s.held)))
			s.held = s.held[:0]
			s.mode = modeFence
			s.fenceOpened = true
			s.lineStart = false
		}
		return
	case r == '#' && allBytes(marker, '#') && len(marker) < 6:
		s.held = append(s.held, '#')
		return
	case len(marker) == 0 && (r == '-' || r == '*' || r == '+' || r == '>'):
		s.held = append(s.held, byte(r))
		return
	case r == ' ' && len(marker) > 0 && allBytes(marker, '#'):
		// "## Title": the hashes are structure, not text
		s.held = s.held[:0]
		s.lineStyle = styleHeading
		s.lineStart = false
		return
	case r == ' ' && len(marker) == 1 && (marker[0] == '-' || marker[0] == '*' || marker[0] == '+'):
		out.WriteString(strings.Repeat(" ", indent))
		out.WriteString(s.theme.Accent("•") + " ")
		s.held = s.held[:0]
		s.lineStart = false
		return
	case r == ' ' && len(marker) == 1 && marker[0] == '>':
		out.WriteString(s.theme.Dim("▎ "))
		s.held = s.held[:0]
		s.lineStyle = styleQuote
		s.lineStart = false
		return
	}
	s.flushHeld(out)
	s.lineStart = false
	s.inlineStep(r, out)
}

// flushHeld gives up on a held block marker: the indentation is written as is
// and the marker characters take the inline path, so a leading ` or ** still
// opens a code span or bold run.
func (s *Styler) flushHeld(out *strings.Builder) {
	held := append([]byte(nil), s.held...)
	s.held = s.held[:0]
	indent := 0
	for indent < len(held) && held[indent] == ' ' {
		indent++
	}
	out.Write(held[:indent])
	for _, b := range held[indent:] {
		s.inlineStep(rune(b), out)
	}
}

func allBytes(b []byte, c byte) bool {
	for _, x := range b {
		if x != c {
			return false
		}
	}
	return true
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
	if s.lineStart && len(s.held) > 0 {
		s.flushHeld(out)
	}
	s.flushStars(out)
	s.lineStart = true
	s.lineStyle = styleNone
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
		case s.lineStyle == styleHeading:
			out.WriteString(s.theme.BoldAccent(string(r)))
		case s.lineStyle == styleQuote:
			out.WriteString(s.theme.Dim(string(r)))
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
