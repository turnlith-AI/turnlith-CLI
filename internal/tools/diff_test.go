package tools

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func lines(n int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	return b.String()
}

func TestDiffShowsAChangeWithContextAndLineNumbers(t *testing.T) {
	old := "a\nb\nc\nd\ne\nf\ng\n"
	updated := "a\nb\nc\nD\ne\nf\ng\n"
	out, added, removed := Diff(old, updated, 2, 100)
	if added != 1 || removed != 1 {
		t.Fatalf("added=%d removed=%d", added, removed)
	}
	want := []string{
		"   2   b",
		"   3   c",
		"   4 - d",
		"   4 + D",
		"   5   e",
		"   6   f",
	}
	got := strings.Split(out, "\n")
	if len(got) != len(want) {
		t.Fatalf("got:\n%s\nwant:\n%s", out, strings.Join(want, "\n"))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestDiffPureAdditionAndDeletion(t *testing.T) {
	out, added, removed := Diff("a\nb\n", "a\nx\ny\nb\n", 1, 100)
	if added != 2 || removed != 0 {
		t.Fatalf("added=%d removed=%d\n%s", added, removed, out)
	}
	if !strings.Contains(out, " + x") || !strings.Contains(out, " + y") || strings.Contains(out, " - ") {
		t.Fatalf("diff:\n%s", out)
	}
	out, added, removed = Diff("a\nx\ny\nb\n", "a\nb\n", 1, 100)
	if added != 0 || removed != 2 {
		t.Fatalf("added=%d removed=%d\n%s", added, removed, out)
	}
}

func TestDiffIdenticalIsEmpty(t *testing.T) {
	if out, a, r := Diff("same\n", "same\n", 3, 100); out != "" || a != 0 || r != 0 {
		t.Fatalf("out=%q a=%d r=%d", out, a, r)
	}
}

func TestDiffSeparatesDistantHunks(t *testing.T) {
	old := lines(40)
	updated := strings.Replace(strings.Replace(old, "line 3\n", "line three\n", 1), "line 38\n", "line thirty-eight\n", 1)
	out, added, removed := Diff(old, updated, 2, 100)
	if added != 2 || removed != 2 {
		t.Fatalf("added=%d removed=%d", added, removed)
	}
	if !strings.Contains(out, "\n …\n") {
		t.Fatalf("distant hunks need a separator:\n%s", out)
	}
	if strings.Contains(out, "line 20") {
		t.Fatalf("unrelated middle lines leaked into the diff:\n%s", out)
	}
	if !strings.Contains(out, "  38 + line thirty-eight") {
		t.Fatalf("later hunk lacks its line number:\n%s", out)
	}
}

func TestDiffNoTrailingNewlineAndEmptySides(t *testing.T) {
	out, added, removed := Diff("", "one\ntwo", 2, 100)
	if added != 2 || removed != 0 || !strings.Contains(out, "   1 + one") {
		t.Fatalf("creating content: a=%d r=%d\n%s", added, removed, out)
	}
	out, added, removed = Diff("one\ntwo", "", 2, 100)
	if added != 0 || removed != 2 || !strings.Contains(out, "   2 - two") {
		t.Fatalf("emptying content: a=%d r=%d\n%s", added, removed, out)
	}
}

func TestDiffIsCappedByLines(t *testing.T) {
	out, added, removed := Diff(lines(200), strings.ReplaceAll(lines(200), "line", "LINE"), 1, 20)
	if added != 200 || removed != 200 {
		t.Fatalf("counts must cover the whole change: a=%d r=%d", added, removed)
	}
	got := strings.Split(out, "\n")
	if len(got) > 21 || !strings.Contains(got[len(got)-1], "more lines") {
		t.Fatalf("cap not applied (%d lines), last = %q", len(got), got[len(got)-1])
	}
}

func TestDiffStaysFastOnBigFiles(t *testing.T) {
	old := lines(30000)
	updated := strings.Replace(old, "line 15000\n", "changed\n", 1)
	start := time.Now()
	out, added, removed := Diff(old, updated, 3, 100)
	if added != 1 || removed != 1 || !strings.Contains(out, "15000 - line 15000") {
		t.Fatalf("a=%d r=%d\n%s", added, removed, out)
	}
	if d := time.Since(start); d > 500*time.Millisecond {
		t.Fatalf("diff took %v", d)
	}
	// rewriting a huge file wholesale must not blow memory or time either
	start = time.Now()
	_, added, removed = Diff(lines(20000), strings.ReplaceAll(lines(20000), "line", "row"), 3, 50)
	if added != 20000 || removed != 20000 {
		t.Fatalf("wholesale rewrite a=%d r=%d", added, removed)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("wholesale diff took %v", d)
	}
}

func TestDiffMovedBlocksStillCountHonestly(t *testing.T) {
	old := "a\nb\nc\nd\n"
	updated := "c\nd\na\nb\n"
	_, added, removed := Diff(old, updated, 1, 100)
	if added != 2 || removed != 2 {
		t.Fatalf("a=%d r=%d", added, removed)
	}
}
