package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeEditor writes a shell script that replaces the file it is given with
// content, the way an editor session ends after the user types and saves.
func fakeEditor(t *testing.T, content string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("needs a POSIX shell")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "ed.sh")
	// the file to edit is the last argument (VISUAL may carry flags before it)
	body := "#!/bin/sh\nfor f; do :; done\nprintf '%s' '" + strings.ReplaceAll(content, "'", `'\''`) + "' > \"$f\"\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return script
}

func TestEditInEditorReturnsWhatWasSaved(t *testing.T) {
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", fakeEditor(t, "first line\nsecond line\n"))
	got, err := editInEditor("")
	if err != nil {
		t.Fatal(err)
	}
	if got != "first line\nsecond line" {
		t.Fatalf("got %q", got)
	}
}

func TestEditInEditorPrefersVISUALAndAcceptsArguments(t *testing.T) {
	script := fakeEditor(t, "from visual")
	t.Setenv("VISUAL", script+" --wait")
	t.Setenv("EDITOR", "/definitely/not/an/editor")
	got, err := editInEditor("seed")
	if err != nil || got != "from visual" {
		t.Fatalf("got %q err=%v", got, err)
	}
}

func TestEditInEditorSeedsTheFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs a POSIX shell")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "cat.sh")
	// an "editor" that keeps the file as it found it
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", script)
	got, err := editInEditor("draft from the prompt")
	if err != nil || got != "draft from the prompt" {
		t.Fatalf("got %q err=%v", got, err)
	}
}

func TestEditInEditorReportsAMissingEditor(t *testing.T) {
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "/definitely/not/an/editor")
	if _, err := editInEditor(""); err == nil || !strings.Contains(err.Error(), "editor") {
		t.Fatalf("err = %v", err)
	}
}

func TestEditInEditorEmptyResultMeansCancel(t *testing.T) {
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", fakeEditor(t, "  \n"))
	got, err := editInEditor("")
	if err != nil || got != "" {
		t.Fatalf("got %q err=%v", got, err)
	}
}

func TestJoinContinuation(t *testing.T) {
	cases := []struct {
		lines []string
		want  string
		more  bool // does the last line ask for another?
	}{
		{[]string{"single"}, "single", false},
		{[]string{`one \`}, "one", true},
		{[]string{`one \`, "two"}, "one\ntwo", false},
		{[]string{`a \`, `b \`, "c"}, "a\nb\nc", false},
		{[]string{`ends with a backslash\\`}, `ends with a backslash\\`, false}, // an escaped backslash is literal
	}
	for _, c := range cases {
		line := ""
		for i, l := range c.lines {
			joined, more := continueLine(line, l, i > 0)
			line = joined
			if i == len(c.lines)-1 && more != c.more {
				t.Errorf("%q: more=%v want %v", c.lines, more, c.more)
			}
		}
		if line != c.want {
			t.Errorf("%q -> %q, want %q", c.lines, line, c.want)
		}
	}
}
