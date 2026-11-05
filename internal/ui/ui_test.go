package ui

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestThemeWrapsOnlyWhenColorsAreOn(t *testing.T) {
	off := &Theme{}
	if got := off.Green("ok"); got != "ok" {
		t.Fatalf("colors off: %q", got)
	}
	on := &Theme{Colors: true}
	got := on.Green("ok")
	if !strings.HasPrefix(got, "\x1b[") || !strings.HasSuffix(got, "\x1b[0m") || !strings.Contains(got, "ok") {
		t.Fatalf("colors on: %q", got)
	}
	if on.Dim("") != "" {
		t.Fatal("an empty string must stay empty, not become an escape pair")
	}
}

func TestNewThemeForHonorsNoColorAndDumbTerm(t *testing.T) {
	// a regular file is not a terminal either, so use the environment checks
	// on their own: they must disable color regardless of the stream
	f, err := os.CreateTemp(t.TempDir(), "out")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	t.Setenv("NO_COLOR", "1")
	if NewThemeFor(f).Colors {
		t.Fatal("NO_COLOR must disable colors")
	}
	os.Unsetenv("NO_COLOR")
	t.Setenv("TERM", "dumb")
	if NewThemeFor(f).Colors {
		t.Fatal("TERM=dumb must disable colors")
	}
	t.Setenv("TERM", "xterm-256color")
	if NewThemeFor(f).Colors {
		t.Fatal("a file is not a terminal: no colors")
	}
	if IsTerminal(f) {
		t.Fatal("IsTerminal(file) = true")
	}
}

func TestNullDeviceIsNotATerminal(t *testing.T) {
	null, err := os.Open(os.DevNull)
	if err != nil {
		t.Skip("no null device")
	}
	defer null.Close()
	if IsTerminal(null) {
		t.Fatal("/dev/null is a character device but not a terminal")
	}
}

func TestRendererWritesWholeLines(t *testing.T) {
	var buf bytes.Buffer
	r := NewRenderer(&buf, &Theme{})
	r.Infof("hello %s", "world")
	r.Notice("fyi")
	r.Errorf("bad %d", 7)
	want := "hello world\n· fyi\n✖ bad 7\n"
	if buf.String() != want {
		t.Fatalf("got %q want %q", buf.String(), want)
	}
}

func TestSanitizeStripsTerminalControl(t *testing.T) {
	cases := []struct{ in, block, inline string }{
		{"plain", "plain", "plain"},
		{"a\x1b[2Jb", "a[2Jb", "a[2Jb"},                             // ESC removed, so the sequence is inert text
		{"title\x1b]0;pwned\x07", "title]0;pwned", "title]0;pwned"}, // OSC + BEL
		{"c1\u009b31mred", "c131mred", "c131mred"},                  // 8-bit CSI
		{"two\nlines\tand\ttabs", "two\nlines\tand\ttabs", "two lines and tabs"},
		{"crlf\r\nx", "crlf\nx", "crlf  x"},
		{"nul\x00del\x7f", "nuldel", "nuldel"},
		{"ünïcödé ✓ 日本", "ünïcödé ✓ 日本", "ünïcödé ✓ 日本"},
	}
	for _, c := range cases {
		if got := SanitizeBlock(c.in); got != c.block {
			t.Errorf("SanitizeBlock(%q) = %q, want %q", c.in, got, c.block)
		}
		if got := SanitizeInline(c.in); got != c.inline {
			t.Errorf("SanitizeInline(%q) = %q, want %q", c.in, got, c.inline)
		}
	}
}
