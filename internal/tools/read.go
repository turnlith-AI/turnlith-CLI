package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// ReadTool reads a text file with line numbers.
type ReadTool struct {
	Root    string
	Tracker *Tracker
}

func (t *ReadTool) Name() string { return "Read" }

func (t *ReadTool) Description() string {
	return "Read a text file from the local filesystem. Output includes line numbers " +
		"(use offset/limit for large files). Always read a file before editing it."
}

func (t *ReadTool) Schema() json.RawMessage {
	return schema(map[string]any{
		"file_path": prop("string", "Absolute or working-directory-relative path to the file."),
		"offset":    prop("integer", "Line number to start reading from (1-based). Defaults to 1."),
		"limit":     prop("integer", "Maximum number of lines to read. Defaults to 2000."),
	}, "file_path")
}

func (t *ReadTool) Gated() bool { return false }

const (
	readDefaultLimit = 2000
	readMaxBytes     = 256 * 1024
)

type readArgs struct {
	FilePath string `json:"file_path"`
	Offset   int    `json:"offset"`
	Limit    int    `json:"limit"`
}

func (t *ReadTool) Run(ctx context.Context, raw json.RawMessage) (string, error) {
	var args readArgs
	if err := decodeArgs(raw, &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	path, err := resolvePath(t.Root, args.FilePath)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("cannot access %s: %w", path, err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("%s is a directory — use Glob or Grep to explore it, or Bash(ls)", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("cannot read %s: %w", path, err)
	}
	if isBinary(data) {
		return "", fmt.Errorf("%s appears to be binary and cannot be read as text", path)
	}
	if len(data) == 0 {
		t.Tracker.MarkRead(path)
		return "(empty file)", nil
	}

	content := strings.ReplaceAll(string(data), "\r\n", "\n")
	lines := strings.Split(content, "\n")
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1] // trailing newline is a terminator, not an empty line
	}

	start := 0
	if args.Offset > 1 {
		start = args.Offset - 1
	}
	if start > len(lines) {
		start = len(lines)
	}
	limit := args.Limit
	if limit <= 0 {
		limit = readDefaultLimit
	}
	end := start + limit
	if end > len(lines) {
		end = len(lines)
	}

	var b strings.Builder
	for i := start; i < end; i++ {
		line := lines[i]
		if len(line) > 2000 {
			line = truncateText(line, 2000)
		}
		fmt.Fprintf(&b, "%6d→%s\n", i+1, line)
	}
	if end < len(lines) {
		fmt.Fprintf(&b, "… (%d more lines)\n", len(lines)-end)
	}
	out := truncateText(b.String(), readMaxBytes)
	t.Tracker.MarkRead(path)
	return out, nil
}
