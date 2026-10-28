package tools

import (
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// The undo history: before every Write/Edit the tracker keeps what the file
// held, so /undo can put it back. It lives in memory for the session only.
const (
	undoMaxEntries = 50
	undoMaxBytes   = 32 << 20 // total snapshot size across entries
)

// ErrNothingToUndo means the history is empty.
var ErrNothingToUndo = errors.New("nothing to undo")

// ErrChangedSince means the file no longer holds what the agent last wrote —
// the user (or a tool) changed it since — so undoing would destroy that work.
var ErrChangedSince = errors.New("the file changed after the agent's edit")

type undoEntry struct {
	path    string // as the tool addressed it
	existed bool   // false: the change created the file
	content []byte // what the file held before
	mode    os.FileMode
	after   [sha256.Size]byte // hash of what the change wrote
	at      time.Time
}

// UndoResult describes a reverted change.
type UndoResult struct {
	Path    string
	Removed bool // the file was created by the agent and has been deleted
	Left    int  // changes that can still be undone
}

// recordChange remembers a completed write: orig is the file's previous bytes
// (existed=false for a new file), written what it holds now.
func (t *Tracker) recordChange(path string, existed bool, orig []byte, mode os.FileMode, written []byte) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.undo = append(t.undo, undoEntry{
		path: path, existed: existed, content: append([]byte(nil), orig...), mode: mode,
		after: sha256.Sum256(written), at: time.Now(),
	})
	for len(t.undo) > undoMaxEntries || undoBytes(t.undo) > undoMaxBytes {
		if len(t.undo) == 0 {
			break
		}
		t.undo = t.undo[1:]
	}
}

func undoBytes(entries []undoEntry) int {
	n := 0
	for _, e := range entries {
		n += len(e.content)
	}
	return n
}

// UndoDepth is how many changes can be undone.
func (t *Tracker) UndoDepth() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.undo)
}

// Undo reverts the most recent change. Unless force is set it refuses when
// the file was modified after the agent wrote it, keeping the entry.
func (t *Tracker) Undo(force bool) (UndoResult, error) {
	t.mu.Lock()
	if len(t.undo) == 0 {
		t.mu.Unlock()
		return UndoResult{}, ErrNothingToUndo
	}
	e := t.undo[len(t.undo)-1]
	t.mu.Unlock()

	current, readErr := os.ReadFile(e.path)
	if !force && (readErr != nil || sha256.Sum256(current) != e.after) {
		if readErr != nil && os.IsNotExist(readErr) && !e.existed {
			// the new file is already gone: nothing left to revert
		} else {
			return UndoResult{Path: e.path}, ErrChangedSince
		}
	}

	res := UndoResult{Path: e.path}
	if e.existed {
		if err := writeFileAtomic(e.path, e.content, e.mode); err != nil {
			return res, err
		}
	} else {
		if err := os.Remove(e.path); err != nil && !os.IsNotExist(err) {
			return res, err
		}
		res.Removed = true
		removeEmptyParents(filepath.Dir(e.path))
	}

	t.mu.Lock()
	t.undo = t.undo[:len(t.undo)-1]
	res.Left = len(t.undo)
	t.mu.Unlock()
	return res, nil
}

// removeEmptyParents deletes directories a created file leaves empty, up to
// (not including) the first non-empty one. It never goes above a directory
// that still has content.
func removeEmptyParents(dir string) {
	for i := 0; i < 8; i++ {
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) > 0 {
			return
		}
		if os.Remove(dir) != nil {
			return
		}
		dir = filepath.Dir(dir)
	}
}

// ChangedFiles lists the files this session wrote or edited, most recent first.
func (t *Tracker) ChangedFiles() []string {
	var out []string
	for _, f := range t.Files() {
		if f.Written || f.Edited {
			out = append(out, f.Path)
		}
	}
	return out
}
