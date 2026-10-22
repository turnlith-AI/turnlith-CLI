package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/turnlith-AI/turnlith-CLI/internal/tools"
)

func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestExpandMentionsAttachesFiles(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"main.go":        "package main\n",
		"sub/deeper.go":  "package sub\n",
		"node_modules/x": "should not matter\n",
	})
	tracker := tools.NewTracker()

	wire, attached, problems := expandMentions(dir, "explain @main.go and @sub/deeper.go, email me at a@b.com", tracker)
	if len(problems) != 0 {
		t.Fatalf("problems = %v", problems)
	}
	if len(attached) != 2 {
		t.Fatalf("attached = %+v", attached)
	}
	if !strings.Contains(wire, `<file path="main.go">`) || !strings.Contains(wire, "package main") {
		t.Fatalf("wire prompt is missing the main.go block:\n%s", wire)
	}
	if !strings.Contains(wire, `<file path="sub/deeper.go">`) {
		t.Fatalf("wire prompt is missing the deeper.go block:\n%s", wire)
	}
	if !strings.HasPrefix(wire, "explain @main.go") {
		t.Fatalf("the user text must stay verbatim at the front:\n%s", wire)
	}
	if !tracker.WasRead(filepath.Join(dir, "main.go")) {
		t.Fatal("attached files must count as read")
	}
}

func TestExpandMentionsLeavesUnknownAndReportsProblems(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"blob.bin": "\x00\x01binary",
		"adir/x":   "x",
	})

	wire, attached, problems := expandMentions(dir, "see @nowhere, @blob.bin, @adir, and @adir/x.", nil)
	if len(attached) != 1 || attached[0].Path != "adir/x" {
		t.Fatalf("attached = %+v", attached)
	}
	if len(problems) != 2 {
		t.Fatalf("problems = %v", problems)
	}
	if !strings.Contains(wire, "@nowhere") || !strings.Contains(wire, "@blob.bin") {
		t.Fatalf("unresolvable mentions must stay literal:\n%s", wire)
	}
	// trailing sentence punctuation must not break resolution
	if !strings.Contains(wire, `<file path="adir/x">`) {
		t.Fatalf("wire prompt = %s", wire)
	}
}

func TestCapMentionTruncates(t *testing.T) {
	long := strings.Repeat("line\n", 3000)
	capped, truncated := capMention(long)
	if !truncated || strings.Count(capped, "\n") > 2501 {
		t.Fatalf("truncated=%v lines=%d", truncated, strings.Count(capped, "\n"))
	}
	if _, truncated := capMention("short"); truncated {
		t.Fatal("short content must not be truncated")
	}
}

func TestPathCandidatesMatchesBoundariesAndSkips(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"main.go":              "x",
		"internal/cli/cli.go":  "x",
		"internal/ui/repl.go":  "x",
		"node_modules/skip.go": "x",
		".git/config":          "x",
	})

	got := pathCandidates(dir, "")
	if len(got) == 0 || got[0] != "internal/" {
		t.Fatalf("empty query should list directories first, got %v", got)
	}
	for _, p := range got {
		if strings.HasPrefix(p, "node_modules/") || strings.HasPrefix(p, ".git/") {
			t.Fatalf("skip-listed tree leaked into candidates: %v", got)
		}
	}

	got = pathCandidates(dir, "cli.go")
	if len(got) != 1 || got[0] != "internal/cli/cli.go" {
		t.Fatalf("boundary match failed: %v", got)
	}

	got = pathCandidates(dir, "ui/re")
	if len(got) != 1 || got[0] != "internal/ui/repl.go" {
		t.Fatalf("prefix match failed: %v", got)
	}

	got = pathCandidates(dir, "internal/")
	// everything under internal/, directories first, the dir itself excluded
	want := []string{"internal/cli/", "internal/ui/", "internal/cli/cli.go", "internal/ui/repl.go"}
	if len(got) != len(want) {
		t.Fatalf("directory drilling = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("directory drilling = %v, want %v", got, want)
		}
	}

	if got := pathCandidates(dir, "zzz"); len(got) != 0 {
		t.Fatalf("no match expected: %v", got)
	}
}

func TestCompleteArgumentOffersMentionPaths(t *testing.T) {
	dir := writeTree(t, map[string]string{"internal/cli/cli.go": "x"})
	a, _ := testApp(t, "http://127.0.0.1:1", testAgents())
	a.cwd = dir

	got := a.completeArgument("look at", "@cli.g")
	if len(got) != 1 || got[0] != "@internal/cli/cli.go" {
		t.Fatalf("mention completion = %v", got)
	}
	if got := a.completeArgument("look at", "cli.g"); got != nil {
		t.Fatalf("non-@ tokens must not complete to paths: %v", got)
	}
}
