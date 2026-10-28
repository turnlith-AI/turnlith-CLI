package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/turnlith-AI/turnlith-CLI/internal/permissions"
)

// WriteTool creates or replaces a file wholesale.
type WriteTool struct {
	Root    string
	Tracker *Tracker
}

func (t *WriteTool) Name() string { return "Write" }

func (t *WriteTool) Description() string {
	return "Write a file (creates it, or replaces the whole file if it exists). " +
		"Existing files must have been read in this session first; prefer Edit for small changes. " +
		"Parent directories are created automatically."
}

func (t *WriteTool) Schema() json.RawMessage {
	return schema(map[string]any{
		"file_path": prop("string", "Absolute or working-directory-relative path to write."),
		"content":   prop("string", "The complete file content."),
	}, "file_path", "content")
}

func (t *WriteTool) Gated() bool { return true }

// NeedsPermission asks before touching files outside the working directory.
func (t *WriteTool) NeedsPermission(raw json.RawMessage) (string, bool) {
	var args struct {
		FilePath string `json:"file_path"`
	}
	if decodeArgs(raw, &args) != nil {
		return "", false
	}
	return outsideWorkspace(t.Root, args.FilePath)
}

func (t *WriteTool) Category() string { return permissions.CategoryEdit }

type writeArgs struct {
	FilePath string `json:"file_path"`
	Content  string `json:"content"`
}

// writePlan is a validated write, ready to apply.
type writePlan struct {
	path    string
	mode    os.FileMode
	orig    []byte // previous bytes (nil for a new file)
	out     []byte
	before  string // LF-normalized previous text ("" for a new file)
	after   string
	created bool
	lines   int
	bytes   int
}

func (t *WriteTool) plan(raw json.RawMessage) (*writePlan, error) {
	var args writeArgs
	if err := decodeArgs(raw, &args); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	path, err := resolvePath(t.Root, args.FilePath)
	if err != nil {
		return nil, err
	}
	p := &writePlan{path: path, mode: 0o644, created: true, after: args.Content, lines: lineCount(args.Content), bytes: len(args.Content)}
	content := args.Content
	if info, err := os.Stat(path); err == nil {
		if info.IsDir() {
			return nil, fmt.Errorf("%s is a directory", path)
		}
		if !t.Tracker.WasRead(path) {
			return nil, fmt.Errorf("%s already exists and has not been read in this session — read it first, or use Edit for targeted changes", path)
		}
		if t.Tracker.Stale(path) {
			return nil, fmt.Errorf("%s has changed on disk since it was read — read it again before overwriting it", path)
		}
		p.created = false
		p.mode = info.Mode().Perm()
		if existing, err := os.ReadFile(path); err == nil {
			p.orig = existing
			p.before = toLF(string(existing))
			// keep a CRLF file CRLF: the model only ever saw it with plain newlines
			if isPureCRLF(string(existing)) && !strings.Contains(content, "\r") {
				content = toCRLF(content)
			}
		}
	}
	p.out = []byte(content)
	return p, nil
}

// Prepare implements Preparer: validate the write and preview it — a diff for
// an overwrite, the first lines for a new file.
func (t *WriteTool) Prepare(raw json.RawMessage) (string, error) {
	p, err := t.plan(raw)
	if err != nil {
		return "", err
	}
	if p.created {
		preview, _, _ := Diff("", p.after, 0, 12)
		return preview, nil
	}
	diff, _, _ := Diff(p.before, p.after, 2, 14)
	return diff, nil
}

func (t *WriteTool) Run(ctx context.Context, raw json.RawMessage) (string, error) {
	res, err := t.RunResult(ctx, raw)
	return res.Text, err
}

// RunResult writes the file; the model gets a confirmation, the user a
// description of what changed.
func (t *WriteTool) RunResult(_ context.Context, raw json.RawMessage) (Result, error) {
	p, err := t.plan(raw)
	if err != nil {
		return Result{}, err
	}
	if err := os.MkdirAll(filepath.Dir(p.path), 0o755); err != nil {
		return Result{}, fmt.Errorf("cannot create parent directories for %s: %w", p.path, err)
	}
	if err := writeFileAtomic(p.path, p.out, p.mode); err != nil {
		return Result{}, fmt.Errorf("cannot write %s: %w", p.path, err)
	}
	t.Tracker.recordChange(p.path, !p.created, p.orig, p.mode, p.out)
	t.Tracker.MarkWritten(p.path)
	display := changeDisplay(p.before, p.after)
	if p.created {
		preview, _, _ := Diff("", p.after, 0, 12)
		display = fmt.Sprintf("Created new file (%s)", plural(p.lines, "line"))
		if preview != "" {
			display += "\n" + preview
		}
	}
	return Result{
		Text:    fmt.Sprintf("Wrote %d lines (%d bytes) to %s", p.lines, p.bytes, p.path),
		Display: display,
	}, nil
}
