package tools

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func editFile(t *testing.T, tracker *Tracker, dir, name, from, to string) {
	t.Helper()
	out := run(t, &EditTool{Root: dir, Tracker: tracker}, map[string]any{"file_path": name, "old_string": from, "new_string": to})
	if !strings.Contains(out, "replaced") {
		t.Fatalf("edit %s: %s", name, out)
	}
}

func TestUndoRestoresEditsNewestFirst(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "a.txt", "one\ntwo\nthree\n")
	tracker := NewTracker()
	run(t, &ReadTool{Root: dir, Tracker: tracker}, map[string]any{"file_path": "a.txt"})
	editFile(t, tracker, dir, "a.txt", "one", "1")
	editFile(t, tracker, dir, "a.txt", "two", "2")

	if got := tracker.UndoDepth(); got != 2 {
		t.Fatalf("depth = %d", got)
	}
	res, err := tracker.Undo(false)
	if err != nil || res.Path != path || res.Removed {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if data, _ := os.ReadFile(path); string(data) != "1\ntwo\nthree\n" {
		t.Fatalf("after first undo: %q", data)
	}
	if _, err := tracker.Undo(false); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(path); string(data) != "one\ntwo\nthree\n" {
		t.Fatalf("after second undo: %q", data)
	}
	if _, err := tracker.Undo(false); !errors.Is(err, ErrNothingToUndo) {
		t.Fatalf("err = %v, want ErrNothingToUndo", err)
	}
}

func TestUndoOfANewFileRemovesIt(t *testing.T) {
	dir := t.TempDir()
	tracker := NewTracker()
	run(t, &WriteTool{Root: dir, Tracker: tracker}, map[string]any{"file_path": "sub/new.txt", "content": "hello\n"})
	path := filepath.Join(dir, "sub", "new.txt")
	res, err := tracker.Undo(false)
	if err != nil || !res.Removed {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("file should be gone")
	}
}

func TestUndoRestoresAnOverwrittenFileAndItsMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "run.sh")
	if err := os.WriteFile(path, []byte("echo old\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	tracker := NewTracker()
	run(t, &ReadTool{Root: dir, Tracker: tracker}, map[string]any{"file_path": "run.sh"})
	run(t, &WriteTool{Root: dir, Tracker: tracker}, map[string]any{"file_path": "run.sh", "content": "echo new\n"})
	if _, err := tracker.Undo(false); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	info, _ := os.Stat(path)
	if string(data) != "echo old\n" || (info.Mode().Perm() != 0o755 && os.PathSeparator == '/') {
		t.Fatalf("data=%q mode=%v", data, info.Mode())
	}
}

func TestUndoRefusesToClobberLaterChanges(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "a.txt", "one\n")
	tracker := NewTracker()
	run(t, &ReadTool{Root: dir, Tracker: tracker}, map[string]any{"file_path": "a.txt"})
	editFile(t, tracker, dir, "a.txt", "one", "uno")
	// the user keeps working in their editor
	if err := os.WriteFile(path, []byte("uno plus my own work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := tracker.Undo(false); !errors.Is(err, ErrChangedSince) {
		t.Fatalf("err = %v, want ErrChangedSince", err)
	}
	if data, _ := os.ReadFile(path); !strings.Contains(string(data), "my own work") {
		t.Fatalf("undo clobbered later changes: %q", data)
	}
	if tracker.UndoDepth() != 1 {
		t.Fatal("a refused undo must keep the entry")
	}
	if _, err := tracker.Undo(true); err != nil {
		t.Fatalf("force: %v", err)
	}
	if data, _ := os.ReadFile(path); string(data) != "one\n" {
		t.Fatalf("forced undo: %q", data)
	}
}

func TestUndoMakesTheModelReReadTheFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.txt", "one\n")
	tracker := NewTracker()
	run(t, &ReadTool{Root: dir, Tracker: tracker}, map[string]any{"file_path": "a.txt"})
	editFile(t, tracker, dir, "a.txt", "one", "two")
	if _, err := tracker.Undo(false); err != nil {
		t.Fatal(err)
	}
	out := run(t, &EditTool{Root: dir, Tracker: tracker}, map[string]any{"file_path": "a.txt", "old_string": "one", "new_string": "x"})
	if !strings.Contains(out, "changed on disk since it was read") {
		t.Fatalf("after /undo the model's picture of the file is stale; it must read again: %s", out)
	}
}

func TestUndoHistoryIsBounded(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.txt", "0\n")
	tracker := NewTracker()
	run(t, &ReadTool{Root: dir, Tracker: tracker}, map[string]any{"file_path": "a.txt"})
	for i := 0; i < undoMaxEntries+10; i++ {
		editFile(t, tracker, dir, "a.txt", string(rune('0'+i%10)), string(rune('0'+(i+1)%10)))
	}
	if got := tracker.UndoDepth(); got != undoMaxEntries {
		t.Fatalf("depth = %d, want %d", got, undoMaxEntries)
	}
}

func TestChangedFilesListsWhatTheSessionModified(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.txt", "a\n")
	writeFile(t, dir, "b.txt", "b\n")
	tracker := NewTracker()
	run(t, &ReadTool{Root: dir, Tracker: tracker}, map[string]any{"file_path": "a.txt"})
	run(t, &ReadTool{Root: dir, Tracker: tracker}, map[string]any{"file_path": "b.txt"})
	editFile(t, tracker, dir, "a.txt", "a", "A")
	run(t, &WriteTool{Root: dir, Tracker: tracker}, map[string]any{"file_path": "c.txt", "content": "c\n"})
	got := tracker.ChangedFiles()
	if len(got) != 2 {
		t.Fatalf("got %v (b.txt was only read)", got)
	}
}
