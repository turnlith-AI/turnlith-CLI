package ui

import (
	"fmt"
	"strings"
	"testing"
)

func texts(lines []resultLine) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = l.Text
	}
	return out
}

func kinds(lines []resultLine) string {
	var b strings.Builder
	for _, l := range lines {
		b.WriteByte(byte(l.Kind))
	}
	return b.String()
}

func numbered(n int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "%6d→line %d\n", i, i)
	}
	return b.String()
}

func TestReadResultIsSummarizedNotDumped(t *testing.T) {
	got := texts(summarizeResult("Read", numbered(120), "", false))
	if len(got) != 1 || got[0] != "Read 120 lines" {
		t.Fatalf("got %q", got)
	}
	got = texts(summarizeResult("Read", numbered(2000)+"… (340 more lines)\n", "", false))
	if len(got) != 1 || got[0] != "Read 2000 lines (340 more not shown)" {
		t.Fatalf("got %q", got)
	}
	if got := texts(summarizeResult("Read", "(empty file)", "", false)); got[0] != "Read 0 lines (empty file)" {
		t.Fatalf("got %q", got)
	}
}

func TestErrorsAndDenialsAreFlagged(t *testing.T) {
	lines := summarizeResult("Edit", "Error: old_string was not found in a.go", "", false)
	if lines[0].Kind != kindErr || !strings.Contains(lines[0].Text, "not found") {
		t.Fatalf("got %+v", lines)
	}
	lines = summarizeResult("Bash", "x", "", true)
	if len(lines) != 1 || lines[0].Kind != kindWarn || lines[0].Text != "denied by the user" {
		t.Fatalf("got %+v", lines)
	}
}

func TestDisplayDiffIsClassified(t *testing.T) {
	display := "Added 1 line, removed 1 line\n   3 - old\n   3 + new\n   4   ctx\n …\n  40 + far\n… (+7 more lines)"
	lines := summarizeResult("Edit", "Edited a.go: replaced 1 occurrence(s).", display, false)
	if got, want := kinds(lines), string([]byte{byte(kindSummary), byte(kindDel), byte(kindAdd), byte(kindCtx), byte(kindFaint), byte(kindAdd), byte(kindFaint)}); got != want {
		t.Fatalf("kinds = %q, want %q\n%v", got, want, texts(lines))
	}
	if strings.Contains(strings.Join(texts(lines), "\n"), "Edited a.go") {
		t.Fatal("the display replaces the model-facing text")
	}
}

func TestLongDisplayIsCapped(t *testing.T) {
	var b strings.Builder
	b.WriteString("Added 80 lines, removed 0 lines")
	for i := 0; i < 80; i++ {
		fmt.Fprintf(&b, "\n%4d + row", i+1)
	}
	lines := summarizeResult("Write", "Wrote 80 lines", b.String(), false)
	if len(lines) > maxDisplayLines+1 {
		t.Fatalf("%d lines", len(lines))
	}
	if last := lines[len(lines)-1]; last.Kind != kindFaint || !strings.Contains(last.Text, "more lines") {
		t.Fatalf("last = %+v", last)
	}
}

func TestBashKeepsTheTailOfLongOutput(t *testing.T) {
	var b strings.Builder
	for i := 1; i <= 40; i++ {
		fmt.Fprintf(&b, "step %d\n", i)
	}
	b.WriteString("FAIL: TestSomething\n(exit code 1)")
	got := texts(summarizeResult("Bash", b.String(), "", false))
	if got[0] != "step 1" || got[1] != "step 2" {
		t.Fatalf("head lost: %q", got)
	}
	if !strings.Contains(got[len(got)-1], "(exit code 1)") || !strings.Contains(got[len(got)-2], "FAIL: TestSomething") {
		t.Fatalf("tail lost: %q", got)
	}
	if len(got) > 7 {
		t.Fatalf("too long: %d lines", len(got))
	}
	if !strings.Contains(strings.Join(got, "\n"), "more lines") {
		t.Fatalf("no elision marker: %q", got)
	}
	short := texts(summarizeResult("Bash", "a\nb\nc", "", false))
	if len(short) != 3 {
		t.Fatalf("short output must be shown whole: %q", short)
	}
}

func TestTodoListIsShownWhole(t *testing.T) {
	result := "[x] read config\n[~] write tests\n[ ] run suite\n[ ] update docs\n[ ] push\n[ ] celebrate\n(1/6 done)"
	lines := summarizeResult("TodoWrite", result, "", false)
	if len(lines) != 7 {
		t.Fatalf("the whole checklist should show, got %d lines: %v", len(lines), texts(lines))
	}
	if lines[0].Kind != kindDone || lines[1].Kind != kindActive || lines[2].Kind != kindPlain {
		t.Fatalf("kinds = %q", kinds(lines))
	}
	if lines[0].Text != "☒ read config" || lines[1].Text != "◐ write tests" || lines[2].Text != "☐ run suite" {
		t.Fatalf("glyphs: %v", texts(lines))
	}
}

func TestOtherToolsShowTheirFirstLines(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 12; i++ {
		fmt.Fprintf(&b, "internal/f%d.go\n", i)
	}
	got := texts(summarizeResult("Glob", b.String(), "", false))
	if len(got) != maxPlainLines+1 || got[0] != "internal/f0.go" || !strings.Contains(got[len(got)-1], "+7 more lines") {
		t.Fatalf("got %q", got)
	}
}

func TestResultTextIsSanitized(t *testing.T) {
	got := texts(summarizeResult("Bash", "\x1b[31mred\x1b[0m and \x07bell\r\nnext", "", false))
	joined := strings.Join(got, "\n")
	if strings.ContainsAny(joined, "\x1b\x07\r") {
		t.Fatalf("control characters survived: %q", joined)
	}
	if !strings.Contains(joined, "red") || !strings.Contains(joined, "next") {
		t.Fatalf("text lost: %q", joined)
	}
}
