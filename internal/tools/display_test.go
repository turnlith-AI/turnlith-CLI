package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runResult(t *testing.T, tool Tool, args map[string]any) (Result, error) {
	t.Helper()
	raw, _ := json.Marshal(args)
	rr, ok := tool.(ResultRunner)
	if !ok {
		t.Fatalf("%s does not implement ResultRunner", tool.Name())
	}
	return rr.RunResult(context.Background(), raw)
}

func prepare(t *testing.T, tool Tool, args map[string]any) (string, error) {
	t.Helper()
	raw, _ := json.Marshal(args)
	p, ok := tool.(Preparer)
	if !ok {
		t.Fatalf("%s does not implement Preparer", tool.Name())
	}
	return p.Prepare(raw)
}

func TestEditResultShowsTheDiffButTheModelGetsTheShortMessage(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.go", "package a\n\nfunc one() int { return 1 }\n\nfunc two() int { return 2 }\n")
	tracker := NewTracker()
	run(t, &ReadTool{Root: dir, Tracker: tracker}, map[string]any{"file_path": "a.go"})
	edit := &EditTool{Root: dir, Tracker: tracker}

	res, err := runResult(t, edit, map[string]any{"file_path": "a.go", "old_string": "return 1", "new_string": "return 10"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Text, "replaced 1 occurrence") || strings.Contains(res.Text, "return 10") {
		t.Fatalf("model text should stay short: %q", res.Text)
	}
	for _, want := range []string{"Added 1 line, removed 1 line", "   3 - func one() int { return 1 }", "   3 + func one() int { return 10 }"} {
		if !strings.Contains(res.Display, want) {
			t.Errorf("display lacks %q:\n%s", want, res.Display)
		}
	}
	// Run still works for callers that only want the text
	if out := run(t, edit, map[string]any{"file_path": "a.go", "old_string": "return 2", "new_string": "return 20"}); !strings.Contains(out, "replaced 1 occurrence") {
		t.Fatalf("Run: %s", out)
	}
}

func TestWriteResultDescribesCreationAndOverwrites(t *testing.T) {
	dir := t.TempDir()
	tracker := NewTracker()
	write := &WriteTool{Root: dir, Tracker: tracker}

	res, err := runResult(t, write, map[string]any{"file_path": "new/x.txt", "content": "alpha\nbeta\ngamma\n"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(res.Display, "Created new file (3 lines)") || !strings.Contains(res.Display, "   1 + alpha") {
		t.Fatalf("creation display:\n%s", res.Display)
	}
	if !strings.Contains(res.Text, "Wrote 3 lines") {
		t.Fatalf("model text = %q", res.Text)
	}

	res, err = runResult(t, write, map[string]any{"file_path": "new/x.txt", "content": "alpha\nBETA\ngamma\n"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(res.Display, "Added 1 line, removed 1 line") || !strings.Contains(res.Display, "   2 - beta") {
		t.Fatalf("overwrite display:\n%s", res.Display)
	}

	res, err = runResult(t, write, map[string]any{"file_path": "new/x.txt", "content": "alpha\nBETA\ngamma\n"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Display, "No changes") {
		t.Fatalf("identical rewrite:\n%s", res.Display)
	}
}

func TestPrepareValidatesWithoutWriting(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "a.txt", "one\ntwo\ntwo\n")
	tracker := NewTracker()
	edit := &EditTool{Root: dir, Tracker: tracker}

	if _, err := prepare(t, edit, map[string]any{"file_path": "a.txt", "old_string": "one", "new_string": "1"}); err == nil || !strings.Contains(err.Error(), "has not been read") {
		t.Fatalf("unread file: %v", err)
	}
	run(t, &ReadTool{Root: dir, Tracker: tracker}, map[string]any{"file_path": "a.txt"})

	if _, err := prepare(t, edit, map[string]any{"file_path": "a.txt", "old_string": "nope", "new_string": "x"}); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("missing text: %v", err)
	}
	if _, err := prepare(t, edit, map[string]any{"file_path": "a.txt", "old_string": "two", "new_string": "2"}); err == nil || !strings.Contains(err.Error(), "occurs 2 times") {
		t.Fatalf("ambiguous: %v", err)
	}
	preview, err := prepare(t, edit, map[string]any{"file_path": "a.txt", "old_string": "one", "new_string": "ONE"})
	if err != nil || !strings.Contains(preview, "   1 - one") || !strings.Contains(preview, "   1 + ONE") {
		t.Fatalf("preview=%q err=%v", preview, err)
	}
	if data, _ := os.ReadFile(path); string(data) != "one\ntwo\ntwo\n" {
		t.Fatalf("Prepare modified the file: %q", data)
	}

	write := &WriteTool{Root: dir, Tracker: tracker}
	if _, err := prepare(t, write, map[string]any{"file_path": "a.txt", "content": "x\n"}); err != nil {
		t.Fatalf("overwrite of a read file: %v", err)
	}
	other := writeFile(t, dir, "unread.txt", "x")
	if _, err := prepare(t, write, map[string]any{"file_path": "unread.txt", "content": "y"}); err == nil || !strings.Contains(err.Error(), "has not been read") {
		t.Fatalf("unread overwrite: %v", err)
	}
	if data, _ := os.ReadFile(other); string(data) != "x" {
		t.Fatal("Prepare modified a file")
	}
	// credential paths fail at preparation time too
	if _, err := prepare(t, write, map[string]any{"file_path": filepath.Join(os.Getenv("HOME"), ".ssh", "id_rsa"), "content": "k"}); err == nil || !strings.Contains(err.Error(), "blocked") {
		t.Fatalf("credential path: %v", err)
	}
}

func TestPreviewOfANewFileShowsItsFirstLines(t *testing.T) {
	dir := t.TempDir()
	write := &WriteTool{Root: dir, Tracker: NewTracker()}
	var b strings.Builder
	for i := 0; i < 40; i++ {
		b.WriteString("row\n")
	}
	preview, err := prepare(t, write, map[string]any{"file_path": "big.txt", "content": b.String()})
	if err != nil {
		t.Fatal(err)
	}
	if n := len(strings.Split(preview, "\n")); n > 14 {
		t.Fatalf("preview of a new file should be short, got %d lines", n)
	}
	if !strings.Contains(preview, "more lines") {
		t.Fatalf("preview should say it is cut:\n%s", preview)
	}
}
