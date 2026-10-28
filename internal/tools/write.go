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

func (t *WriteTool) Category() string { return permissions.CategoryEdit }

type writeArgs struct {
	FilePath string `json:"file_path"`
	Content  string `json:"content"`
}

func (t *WriteTool) Run(ctx context.Context, raw json.RawMessage) (string, error) {
	var args writeArgs
	if err := decodeArgs(raw, &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	path, err := resolvePath(t.Root, args.FilePath)
	if err != nil {
		return "", err
	}

	mode := os.FileMode(0o644)
	content := args.Content
	if info, err := os.Stat(path); err == nil {
		if info.IsDir() {
			return "", fmt.Errorf("%s is a directory", path)
		}
		if !t.Tracker.WasRead(path) {
			return "", fmt.Errorf("%s already exists and has not been read in this session — read it first, or use Edit for targeted changes", path)
		}
		if t.Tracker.Stale(path) {
			return "", fmt.Errorf("%s has changed on disk since it was read — read it again before overwriting it", path)
		}
		mode = info.Mode().Perm()
		// keep a CRLF file CRLF: the model only ever saw it with plain newlines
		if existing, err := os.ReadFile(path); err == nil && isPureCRLF(string(existing)) && !strings.Contains(content, "\r") {
			content = toCRLF(content)
		}
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("cannot create parent directories for %s: %w", path, err)
	}
	if err := writeFileAtomic(path, []byte(content), mode); err != nil {
		return "", fmt.Errorf("cannot write %s: %w", path, err)
	}
	t.Tracker.MarkWritten(path)
	return fmt.Sprintf("Wrote %d lines (%d bytes) to %s", lineCount(args.Content), len(args.Content), path), nil
}
