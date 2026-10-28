package ui

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// lineKind says how a line of a tool result is styled.
type lineKind byte

const (
	kindDim     lineKind = 'd' // ordinary result text
	kindFaint   lineKind = 'f' // elisions, counts
	kindErr     lineKind = 'e'
	kindWarn    lineKind = 'w'
	kindAdd     lineKind = '+'
	kindDel     lineKind = '-'
	kindCtx     lineKind = ' '
	kindSummary lineKind = 's' // "Added 2 lines, removed 1 line"
	kindDone    lineKind = 'x' // checklist items
	kindActive  lineKind = 'a'
	kindPlain   lineKind = 'p'
)

type resultLine struct {
	Text string
	Kind lineKind
}

const (
	maxPlainLines   = 5  // generic tool output shown in the transcript
	maxDisplayLines = 30 // a diff
	bashHeadLines   = 2
	bashTailLines   = 3
)

var (
	diffLineRE = regexp.MustCompile(`^\s*\d+ ([ +\-]) `)
	readLineRE = regexp.MustCompile(`^\s*\d+→`)
	moreRE     = regexp.MustCompile(`^… \((\d+) more lines\)$`)
)

// summarizeResult turns a tool result into the few styled lines the
// transcript shows under the call. The model sees the full result; the person
// reading wants "Read 120 lines", a colored diff, the tail of a failing build.
func summarizeResult(name, result, display string, denied bool) []resultLine {
	if denied {
		return []resultLine{{"denied by the user", kindWarn}}
	}
	if strings.HasPrefix(result, "Error:") {
		lines := splitResult(result)
		if len(lines) > 3 {
			lines = append(lines[:3:3], fmt.Sprintf("… (+%d more lines)", len(lines)-3))
		}
		out := make([]resultLine, len(lines))
		for i, l := range lines {
			out[i] = resultLine{l, kindErr}
		}
		return out
	}
	if display != "" {
		return displayLines(display)
	}

	lines := splitResult(result)
	switch name {
	case "Read":
		if len(lines) == 1 && lines[0] == "(empty file)" {
			return []resultLine{{"Read 0 lines (empty file)", kindDim}}
		}
		n, more := 0, 0
		for _, l := range lines {
			if readLineRE.MatchString(l) {
				n++
			} else if m := moreRE.FindStringSubmatch(l); m != nil {
				more, _ = strconv.Atoi(m[1])
			}
		}
		if n > 0 {
			text := "Read " + pluralLines(n)
			if more > 0 {
				text += fmt.Sprintf(" (%d more not shown)", more)
			}
			return []resultLine{{text, kindDim}}
		}
	case "Bash":
		if len(lines) > bashHeadLines+bashTailLines+1 {
			out := make([]resultLine, 0, bashHeadLines+bashTailLines+1)
			for _, l := range lines[:bashHeadLines] {
				out = append(out, resultLine{l, kindDim})
			}
			out = append(out, resultLine{fmt.Sprintf("… (+%d more lines)", len(lines)-bashHeadLines-bashTailLines), kindFaint})
			for _, l := range lines[len(lines)-bashTailLines:] {
				out = append(out, resultLine{l, kindDim})
			}
			return out
		}
	case "TodoWrite":
		out := make([]resultLine, len(lines))
		for i, l := range lines {
			switch {
			case strings.HasPrefix(l, "[x] "):
				out[i] = resultLine{"☒ " + l[4:], kindDone}
			case strings.HasPrefix(l, "[~] "):
				out[i] = resultLine{"◐ " + l[4:], kindActive}
			case strings.HasPrefix(l, "[ ] "):
				out[i] = resultLine{"☐ " + l[4:], kindPlain}
			default:
				out[i] = resultLine{l, kindFaint}
			}
		}
		return out
	}

	if len(lines) > maxPlainLines {
		out := make([]resultLine, 0, maxPlainLines+1)
		for _, l := range lines[:maxPlainLines] {
			out = append(out, resultLine{l, kindDim})
		}
		return append(out, resultLine{fmt.Sprintf("… (+%d more lines)", len(lines)-maxPlainLines), kindFaint})
	}
	out := make([]resultLine, len(lines))
	for i, l := range lines {
		out[i] = resultLine{l, kindDim}
	}
	return out
}

// displayLines classifies a tool's display text: a summary line, then numbered
// diff lines.
func displayLines(display string) []resultLine {
	lines := splitResult(display)
	extra := 0
	if len(lines) > maxDisplayLines {
		extra = len(lines) - maxDisplayLines
		lines = lines[:maxDisplayLines]
	}
	out := make([]resultLine, 0, len(lines)+1)
	for i, l := range lines {
		kind := kindDim
		if m := diffLineRE.FindStringSubmatch(l); m != nil {
			switch m[1] {
			case "+":
				kind = kindAdd
			case "-":
				kind = kindDel
			default:
				kind = kindCtx
			}
		} else if l == " …" || strings.HasPrefix(l, "… (") {
			kind = kindFaint
		} else if i == 0 {
			kind = kindSummary
		}
		out = append(out, resultLine{l, kind})
	}
	if extra > 0 {
		out = append(out, resultLine{fmt.Sprintf("… (+%d more lines)", extra), kindFaint})
	}
	return out
}

func splitResult(s string) []string {
	s = strings.TrimRight(sanitizeBlock(s), "\n")
	if s == "" {
		return []string{""}
	}
	return strings.Split(s, "\n")
}

func pluralLines(n int) string {
	if n == 1 {
		return "1 line"
	}
	return fmt.Sprintf("%d lines", n)
}
