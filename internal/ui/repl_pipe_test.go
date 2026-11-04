package ui

import (
	"io"
	"os"
	"strings"
	"testing"
)

// Piped input must read line by line: \n is the line terminator, so
// `printf 'url\nkey\n' | tlc configure` gets one value per prompt
// (regression: the paste-newline filter used to swallow all lines into one).
func TestREPLReadlinePipe(t *testing.T) {
	stdinIsTerminal = false // go test stdin is a pipe already; pin it anyway
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = saved }()

	if _, err := w.WriteString("https://example.com\nsk-test\n"); err != nil {
		t.Fatal(err)
	}
	w.Close()

	theme := NewTheme()
	repl, err := NewREPL(NewRenderer(io.Discard, theme), theme)
	if err != nil {
		t.Fatal(err)
	}
	defer repl.Close()

	line, err := repl.Readline()
	if err != nil || line != "https://example.com" {
		t.Fatalf("first line: %q, %v", line, err)
	}
	secret, err := repl.ReadSecret("key: ")
	if err != nil || secret != "sk-test" {
		t.Fatalf("secret: %q, %v", secret, err)
	}
	// exhausted pipe: EOF, not a hang or the whole buffer as one line
	if _, err := repl.Readline(); err == nil || !strings.Contains(err.Error(), "EOF") && err != io.EOF {
		t.Fatalf("want EOF after pipe drained, got %v", err)
	}
}
