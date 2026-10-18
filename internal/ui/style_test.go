package ui

import (
	"strings"
	"testing"
)

func plain() *Theme { return &Theme{Colors: false} }
func colored() *Theme {
	return &Theme{Colors: true}
}

func TestStylerPlainPassThrough(t *testing.T) {
	s := NewStyler(plain())
	out := s.Write("hello **world**\n`code` here\n") + s.Flush()
	want := "hello world\n`code` here\n"
	if out != want {
		t.Fatalf("got %q want %q", out, want)
	}
}

func TestStylerStreamingIsIncremental(t *testing.T) {
	s := NewStyler(plain())
	// prose should not be held back waiting for a newline
	got := s.Write("The quick brown fox")
	if got != "The quick brown fox" {
		t.Fatalf("prose was buffered: %q", got)
	}
}

func TestStylerFence(t *testing.T) {
	s := NewStyler(plain())
	var out strings.Builder
	for _, delta := range []string{"Here:\n", "```go\n", "fmt.Println(\"**not bold**\")\n", "```\n", "done\n"} {
		out.WriteString(s.Write(delta))
	}
	out.WriteString(s.Flush())
	want := "Here:\n```go\nfmt.Println(\"**not bold**\")\n```\ndone\n"
	if out.String() != want {
		t.Fatalf("got %q want %q", out.String(), want)
	}
}

func TestStylerFenceSplitAcrossDeltas(t *testing.T) {
	s := NewStyler(plain())
	var out strings.Builder
	for _, delta := range []string{"a\n`", "`", "`py\n", "x=1\n", "`", "``\n", "b\n"} {
		out.WriteString(s.Write(delta))
	}
	out.WriteString(s.Flush())
	want := "a\n```py\nx=1\n```\nb\n"
	if out.String() != want {
		t.Fatalf("got %q want %q", out.String(), want)
	}
}

func TestStylerFenceFlushWithoutNewline(t *testing.T) {
	s := NewStyler(plain())
	var out strings.Builder
	out.WriteString(s.Write("```\nabc"))
	out.WriteString(s.Flush())
	want := "```\nabc"
	if out.String() != want {
		t.Fatalf("got %q want %q", out.String(), want)
	}
}

func TestStylerBoldTogglesWithEscapes(t *testing.T) {
	s := NewStyler(colored())
	out := s.Write("a **b** c") + s.Flush()
	if !strings.Contains(out, "\x1b[1m") || !strings.Contains(out, "\x1b[0m") {
		t.Fatalf("expected bold escapes, got %q", out)
	}
	if strings.Contains(out, "**") {
		t.Fatalf("bold markers should be swallowed: %q", out)
	}
}

func TestStylerSingleStarPassesThrough(t *testing.T) {
	s := NewStyler(plain())
	out := s.Write("* bullet\n") + s.Flush()
	if out != "* bullet\n" {
		t.Fatalf("got %q", out)
	}
}

func TestStylerInlineCodeNotAcrossLines(t *testing.T) {
	s := NewStyler(plain())
	out := s.Write("`open\nnext` end\n") + s.Flush()
	// inline state resets at the newline; the literal text is unchanged
	if out != "`open\nnext` end\n" {
		t.Fatalf("got %q", out)
	}
}
