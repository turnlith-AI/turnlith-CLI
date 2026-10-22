package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func run(t *testing.T, tool Tool, args map[string]any) string {
	t.Helper()
	raw, _ := json.Marshal(args)
	out, err := tool.Run(context.Background(), raw)
	if err != nil {
		return "Error: " + err.Error()
	}
	return out
}

func TestReadNumbersLinesWithOffset(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.txt", "one\ntwo\nthree\nfour\n")
	tool := &ReadTool{Root: dir, Tracker: NewTracker()}

	out := run(t, tool, map[string]any{"file_path": "a.txt"})
	for _, want := range []string{"     1→one", "     4→four"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
	out = run(t, tool, map[string]any{"file_path": "a.txt", "offset": 2, "limit": 2})
	if !strings.Contains(out, "     2→two") || !strings.Contains(out, "     3→three") {
		t.Fatalf("offset output:\n%s", out)
	}
	if strings.Contains(out, "four") {
		t.Fatalf("limit not applied:\n%s", out)
	}
	// reading marks the file as read for edits
	if !tool.Tracker.WasRead(filepath.Join(dir, "a.txt")) {
		t.Fatal("read did not mark file")
	}
}

func TestReadRejectsDirectoryAndBinary(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "bin.dat", "abc\x00def")
	tool := &ReadTool{Root: dir, Tracker: NewTracker()}
	if out := run(t, tool, map[string]any{"file_path": "."}); !strings.Contains(out, "directory") {
		t.Fatalf("dir read: %s", out)
	}
	if out := run(t, tool, map[string]any{"file_path": "bin.dat"}); !strings.Contains(out, "binary") {
		t.Fatalf("binary read: %s", out)
	}
}

func TestEditRequiresUniqueMatchAndRead(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "a.go", "package a\n\nfunc a() {}\nfunc a2() {}\n")
	tracker := NewTracker()
	tool := &EditTool{Root: dir, Tracker: tracker}

	if out := run(t, tool, map[string]any{"file_path": "a.go", "old_string": "func", "new_string": "fn"}); !strings.Contains(out, "has not been read") {
		t.Fatalf("expected read-first error: %s", out)
	}
	read := &ReadTool{Root: dir, Tracker: tracker}
	run(t, read, map[string]any{"file_path": "a.go"})

	if out := run(t, tool, map[string]any{"file_path": "a.go", "old_string": "func", "new_string": "fn"}); !strings.Contains(out, "occurs 2 times") {
		t.Fatalf("expected uniqueness error: %s", out)
	}
	out := run(t, tool, map[string]any{"file_path": "a.go", "old_string": "func a() {}", "new_string": "func a() { return }"})
	if !strings.Contains(out, "replaced 1 occurrence") {
		t.Fatalf("edit failed: %s", out)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "func a() { return }") || !strings.Contains(string(data), "func a2() {}") {
		t.Fatalf("file content:\n%s", data)
	}
	out = run(t, tool, map[string]any{"file_path": "a.go", "old_string": "func ", "new_string": "fn ", "replace_all": true})
	if !strings.Contains(out, "replaced 2 occurrence") {
		t.Fatalf("replace_all failed: %s", out)
	}
}

func TestWriteRequiresReadForExistingFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "exists.txt", "old")
	tracker := NewTracker()
	write := &WriteTool{Root: dir, Tracker: tracker}

	if out := run(t, write, map[string]any{"file_path": "exists.txt", "content": "new"}); !strings.Contains(out, "has not been read") {
		t.Fatalf("expected read-first error: %s", out)
	}
	// new files are fine
	if out := run(t, write, map[string]any{"file_path": "sub/new.txt", "content": "hi\n"}); !strings.Contains(out, "Wrote") {
		t.Fatalf("write new file failed: %s", out)
	}
	// once read, overwrite is allowed
	run(t, &ReadTool{Root: dir, Tracker: tracker}, map[string]any{"file_path": "exists.txt"})
	if out := run(t, write, map[string]any{"file_path": "exists.txt", "content": "new"}); !strings.Contains(out, "Wrote") {
		t.Fatalf("overwrite failed: %s", out)
	}
}

func TestGlobPatterns(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "top.go", "x")
	writeFile(t, dir, "pkg/inner.go", "x")
	writeFile(t, dir, "pkg/deep/deeper.go", "x")
	writeFile(t, dir, "pkg/notes.md", "x")
	writeFile(t, dir, "node_modules/skip.go", "x")
	tool := &GlobTool{Root: dir}

	out := run(t, tool, map[string]any{"pattern": "**/*.go"})
	for _, want := range []string{"top.go", "pkg/inner.go", "pkg/deep/deeper.go"} {
		if !strings.Contains(out, want) {
			t.Fatalf("** missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "skip.go") {
		t.Fatalf("node_modules was not skipped:\n%s", out)
	}
	out = run(t, tool, map[string]any{"pattern": "*.go"})
	if !strings.Contains(out, "top.go") || strings.Contains(out, "inner.go") {
		t.Fatalf("top-level glob:\n%s", out)
	}
	out = run(t, tool, map[string]any{"pattern": "*.ts"})
	if !strings.Contains(out, "No files matched") {
		t.Fatalf("no-match glob: %s", out)
	}
}

func TestBashOutputExitAndTimeout(t *testing.T) {
	dir := t.TempDir()
	tool := &BashTool{Root: dir, DefaultTimeout: 10 * time.Second}

	out := run(t, tool, map[string]any{"command": "echo hi && echo err >&2 && exit 3"})
	if !strings.Contains(out, "hi") || !strings.Contains(out, "[stderr]") || !strings.Contains(out, "(exit code 3)") {
		t.Fatalf("bash output:\n%s", out)
	}

	start := time.Now()
	out = run(t, tool, map[string]any{"command": "sleep 5", "timeout_ms": 300})
	if !strings.Contains(out, "timed out") {
		t.Fatalf("timeout output:\n%s", out)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("timeout took %s — process group not killed?", elapsed)
	}
}

func TestGrepContentAndFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.go", "package main\n// TODO: fix\n")
	writeFile(t, dir, "b.txt", "nothing here\n")
	tool := &GrepTool{Root: dir}

	out := run(t, tool, map[string]any{"pattern": "TODO", "output_mode": "content"})
	if !strings.Contains(out, "a.go:2:") || !strings.Contains(out, "TODO: fix") {
		t.Fatalf("content grep:\n%s", out)
	}
	out = run(t, tool, map[string]any{"pattern": "TODO"})
	if !strings.Contains(out, "a.go") || strings.Contains(out, "b.txt") {
		t.Fatalf("files grep:\n%s", out)
	}
	out = run(t, tool, map[string]any{"pattern": "tOdO", "ignore_case": true})
	if !strings.Contains(out, "a.go") {
		t.Fatalf("ignore_case grep:\n%s", out)
	}
	out = run(t, tool, map[string]any{"pattern": "zzz"})
	if !strings.Contains(out, "No matches found") {
		t.Fatalf("no-match grep: %s", out)
	}
	out = run(t, tool, map[string]any{"pattern": "TODO", "glob": "*.txt"})
	if !strings.Contains(out, "No matches found") {
		t.Fatalf("glob filter grep: %s", out)
	}
}

func TestLimitedBufferCaps(t *testing.T) {
	buf := newLimitedBuffer(4)
	if _, err := buf.Write([]byte("abcdefgh")); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.HasPrefix(out, "abcd") || !strings.Contains(out, "truncated") {
		t.Fatalf("buffer = %q", out)
	}
}

func TestTrackerRecordsActivity(t *testing.T) {
	tr := NewTracker()
	a, b, c := "/proj/a.go", "/proj/b.go", "/proj/c.go"
	tr.MarkRead(a)
	time.Sleep(time.Millisecond)
	tr.MarkWritten(b)
	time.Sleep(time.Millisecond)
	tr.MarkEdited(c)

	files := tr.Files()
	if len(files) != 3 {
		t.Fatalf("files = %+v", files)
	}
	// most recently touched first
	if files[0].Path != c || files[1].Path != b || files[2].Path != a {
		t.Fatalf("order = %+v", files)
	}
	if !files[0].Edited || files[0].Written || files[0].Read {
		t.Fatalf("c activity = %+v", files[0])
	}
	if !files[1].Written || files[1].Edited {
		t.Fatalf("b activity = %+v", files[1])
	}
	if !files[2].Read || files[2].Written {
		t.Fatalf("a activity = %+v", files[2])
	}
	// writing and editing imply read, so the model can keep editing
	if !tr.WasRead(b) || !tr.WasRead(c) {
		t.Fatal("written/edited files must count as read")
	}
	if tr.WasRead("/proj/missing.go") {
		t.Fatal("unknown file reported as read")
	}
}
