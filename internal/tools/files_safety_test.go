package tools

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func readAll(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestEditMatchesAcrossLinesInCRLFFile(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "win.txt", "alpha\r\nbeta\r\ngamma\r\n")
	tracker := NewTracker()
	run(t, &ReadTool{Root: dir, Tracker: tracker}, map[string]any{"file_path": "win.txt"})

	out := run(t, &EditTool{Root: dir, Tracker: tracker}, map[string]any{
		"file_path": "win.txt", "old_string": "alpha\nbeta", "new_string": "one\ntwo\nthree",
	})
	if !strings.Contains(out, "replaced 1 occurrence") {
		t.Fatalf("edit failed: %s", out)
	}
	if got, want := readAll(t, path), "one\r\ntwo\r\nthree\r\ngamma\r\n"; got != want {
		t.Fatalf("file = %q, want %q (CRLF endings must survive)", got, want)
	}
}

func TestEditLeavesMixedLineEndingsAlone(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "mixed.txt", "a\r\nb\nc\n")
	tracker := NewTracker()
	run(t, &ReadTool{Root: dir, Tracker: tracker}, map[string]any{"file_path": "mixed.txt"})

	out := run(t, &EditTool{Root: dir, Tracker: tracker}, map[string]any{
		"file_path": "mixed.txt", "old_string": "b\nc", "new_string": "B\nC",
	})
	if !strings.Contains(out, "replaced 1 occurrence") {
		t.Fatalf("edit failed: %s", out)
	}
	if got, want := readAll(t, path), "a\r\nB\nC\n"; got != want {
		t.Fatalf("file = %q, want %q", got, want)
	}
}

func TestWritePreservesCRLFOfExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "win.txt", "old\r\nlines\r\n")
	tracker := NewTracker()
	run(t, &ReadTool{Root: dir, Tracker: tracker}, map[string]any{"file_path": "win.txt"})

	out := run(t, &WriteTool{Root: dir, Tracker: tracker}, map[string]any{
		"file_path": "win.txt", "content": "new\nlines\n",
	})
	if !strings.Contains(out, "Wrote") {
		t.Fatalf("write failed: %s", out)
	}
	if got, want := readAll(t, path), "new\r\nlines\r\n"; got != want {
		t.Fatalf("file = %q, want %q", got, want)
	}
}

func TestEditRefusesFileChangedSinceRead(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "a.txt", "one\ntwo\n")
	tracker := NewTracker()
	run(t, &ReadTool{Root: dir, Tracker: tracker}, map[string]any{"file_path": "a.txt"})

	// someone else (an editor, a formatter, git) rewrites the file
	if err := os.WriteFile(path, []byte("one\ntwo\nthree-from-elsewhere\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatal(err)
	}

	out := run(t, &EditTool{Root: dir, Tracker: tracker}, map[string]any{
		"file_path": "a.txt", "old_string": "one", "new_string": "1",
	})
	if !strings.Contains(out, "changed on disk since it was read") {
		t.Fatalf("expected stale-file error: %s", out)
	}
	out = run(t, &WriteTool{Root: dir, Tracker: tracker}, map[string]any{"file_path": "a.txt", "content": "x"})
	if !strings.Contains(out, "changed on disk since it was read") {
		t.Fatalf("write should refuse too: %s", out)
	}
	if got := readAll(t, path); !strings.Contains(got, "three-from-elsewhere") {
		t.Fatalf("stale edit clobbered the file: %q", got)
	}

	// reading again refreshes the stamp and unblocks the edit
	run(t, &ReadTool{Root: dir, Tracker: tracker}, map[string]any{"file_path": "a.txt"})
	out = run(t, &EditTool{Root: dir, Tracker: tracker}, map[string]any{
		"file_path": "a.txt", "old_string": "one", "new_string": "1",
	})
	if !strings.Contains(out, "replaced 1 occurrence") {
		t.Fatalf("edit after re-read failed: %s", out)
	}
}

func TestEditsOwnWritesDoNotLookStale(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.txt", "one\ntwo\n")
	tracker := NewTracker()
	run(t, &ReadTool{Root: dir, Tracker: tracker}, map[string]any{"file_path": "a.txt"})
	edit := &EditTool{Root: dir, Tracker: tracker}
	for _, step := range [][2]string{{"one", "1"}, {"two", "2"}} {
		out := run(t, edit, map[string]any{"file_path": "a.txt", "old_string": step[0], "new_string": step[1]})
		if !strings.Contains(out, "replaced 1 occurrence") {
			t.Fatalf("consecutive edit %q failed: %s", step[0], out)
		}
	}
}

func TestEditThroughSymlinkKeepsTheLink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on windows")
	}
	dir := t.TempDir()
	real := writeFile(t, dir, "real.txt", "hello\n")
	link := filepath.Join(dir, "link.txt")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	tracker := NewTracker()
	// read via the link, edit via the real path: same file, one tracker entry
	run(t, &ReadTool{Root: dir, Tracker: tracker}, map[string]any{"file_path": "link.txt"})
	out := run(t, &EditTool{Root: dir, Tracker: tracker}, map[string]any{
		"file_path": "real.txt", "old_string": "hello", "new_string": "bye",
	})
	if !strings.Contains(out, "replaced 1 occurrence") {
		t.Fatalf("edit failed: %s", out)
	}
	// editing through the link must not replace the link with a regular file
	out = run(t, &EditTool{Root: dir, Tracker: tracker}, map[string]any{
		"file_path": "link.txt", "old_string": "bye", "new_string": "ciao",
	})
	if !strings.Contains(out, "replaced 1 occurrence") {
		t.Fatalf("edit via link failed: %s", out)
	}
	info, err := os.Lstat(link)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("link was replaced: %v %v", info, err)
	}
	if got := readAll(t, real); got != "ciao\n" {
		t.Fatalf("real = %q", got)
	}
}

func TestEditIsAtomicAndKeepsMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "run.sh")
	if err := os.WriteFile(path, []byte("echo a\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	tracker := NewTracker()
	run(t, &ReadTool{Root: dir, Tracker: tracker}, map[string]any{"file_path": "run.sh"})
	out := run(t, &EditTool{Root: dir, Tracker: tracker}, map[string]any{
		"file_path": "run.sh", "old_string": "echo a", "new_string": "echo b",
	})
	if !strings.Contains(out, "replaced 1 occurrence") {
		t.Fatalf("edit failed: %s", out)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		names := []string{}
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("leftover files after edit: %v", names)
	}
	if runtime.GOOS != "windows" {
		info, _ := os.Stat(path)
		if info.Mode().Perm() != 0o755 {
			t.Fatalf("mode = %v, want 0755", info.Mode().Perm())
		}
	}
}
