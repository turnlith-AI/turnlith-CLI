package cli

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/turnlith-AI/turnlith-CLI/internal/tools"
)

// changeApp is a test app rooted in a temp dir with real file tools.
func changeApp(t *testing.T) (*app, *strings.Builder, string) {
	t.Helper()
	a, out := testApp(t, "http://127.0.0.1:1", testAgents())
	dir := t.TempDir()
	a.cwd, a.root = dir, dir
	a.tracker = tools.NewTracker()
	a.registry = tools.NewRegistry(
		&tools.ReadTool{Root: dir, Tracker: a.tracker},
		&tools.EditTool{Root: dir, Tracker: a.tracker},
		&tools.WriteTool{Root: dir, Tracker: a.tracker},
	)
	return a, out, dir
}

func toolRun(t *testing.T, a *app, name string, args map[string]any) string {
	t.Helper()
	raw, _ := json.Marshal(args)
	res, err := a.registry.RunResult(context.Background(), name, raw)
	if err != nil {
		return "Error: " + err.Error()
	}
	return res.Text
}

func TestUndoCommandRevertsAndTellsTheModelNextTurn(t *testing.T) {
	a, out, dir := changeApp(t)
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	toolRun(t, a, "Read", map[string]any{"file_path": "a.txt"})
	toolRun(t, a, "Edit", map[string]any{"file_path": "a.txt", "old_string": "one", "new_string": "two"})

	a.handleSlash("/undo")
	if data, _ := os.ReadFile(path); string(data) != "one\n" {
		t.Fatalf("file = %q", data)
	}
	mustContain(t, "output", out.String(), "restored a.txt")

	wire := a.withPendingNotes("carry on")
	mustContain(t, "wire prompt", wire, "/undo")
	mustContain(t, "wire prompt", wire, "a.txt")
	mustContain(t, "wire prompt", wire, "carry on")
	if again := a.withPendingNotes("later"); again != "later" {
		t.Fatalf("a note must be delivered once, got %q", again)
	}

	out.Reset()
	a.handleSlash("/undo")
	mustContain(t, "output", out.String(), "nothing to undo")
}

func TestUndoCommandRefusesWhenTheFileMovedOnAndForceOverrides(t *testing.T) {
	a, out, dir := changeApp(t)
	path := filepath.Join(dir, "a.txt")
	_ = os.WriteFile(path, []byte("one\n"), 0o644)
	toolRun(t, a, "Read", map[string]any{"file_path": "a.txt"})
	toolRun(t, a, "Edit", map[string]any{"file_path": "a.txt", "old_string": "one", "new_string": "two"})
	_ = os.WriteFile(path, []byte("two and my notes\n"), 0o644)

	a.handleSlash("/undo")
	mustContain(t, "output", out.String(), "changed since")
	mustContain(t, "output", out.String(), "/undo force")
	if data, _ := os.ReadFile(path); !strings.Contains(string(data), "my notes") {
		t.Fatalf("file was clobbered: %q", data)
	}
	a.handleSlash("/undo force")
	if data, _ := os.ReadFile(path); string(data) != "one\n" {
		t.Fatalf("forced undo: %q", data)
	}
}

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestDiffCommandShowsOnlyWhatTheSessionChanged(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	a, out, dir := changeApp(t)
	_ = os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\n"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "other.txt"), []byte("untouched\n"), 0o644)
	gitIn(t, dir, "init", "-q")
	gitIn(t, dir, "add", ".")
	gitIn(t, dir, "commit", "-q", "-m", "init")
	// a change made outside the session must not show up
	_ = os.WriteFile(filepath.Join(dir, "other.txt"), []byte("edited by me\n"), 0o644)

	a.handleSlash("/diff")
	mustContain(t, "output", out.String(), "no files changed")

	toolRun(t, a, "Read", map[string]any{"file_path": "a.txt"})
	toolRun(t, a, "Edit", map[string]any{"file_path": "a.txt", "old_string": "one", "new_string": "ONE"})
	toolRun(t, a, "Write", map[string]any{"file_path": "brand-new.txt", "content": "hello\n"})
	out.Reset()
	a.handleSlash("/diff")
	got := out.String()
	mustContain(t, "diff", got, "-one")
	mustContain(t, "diff", got, "+ONE")
	mustContain(t, "diff", got, "brand-new.txt")
	if strings.Contains(got, "edited by me") || strings.Contains(got, "other.txt") {
		t.Fatalf("diff included files the session did not touch:\n%s", got)
	}
}

func TestDiffCommandWithoutGit(t *testing.T) {
	a, out, dir := changeApp(t)
	t.Setenv("PATH", t.TempDir()) // no git on PATH
	_ = os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\n"), 0o644)
	toolRun(t, a, "Read", map[string]any{"file_path": "a.txt"})
	toolRun(t, a, "Edit", map[string]any{"file_path": "a.txt", "old_string": "one", "new_string": "two"})
	a.handleSlash("/diff")
	mustContain(t, "output", out.String(), "git")
	mustContain(t, "output", out.String(), "a.txt")
}

func TestOSC52SequenceCarriesTheTextAsBase64(t *testing.T) {
	got := osc52("héllo")
	want := "\x1b]52;c;aMOpbGxv\a"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestCopyCommandWithNothingToCopy(t *testing.T) {
	a, out, _ := changeApp(t)
	a.handleSlash("/copy")
	mustContain(t, "output", out.String(), "nothing to copy")
}
