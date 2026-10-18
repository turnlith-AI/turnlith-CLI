package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// EditTool performs exact-string replacements in a file.
type EditTool struct {
	Root    string
	Tracker *Tracker
}

func (t *EditTool) Name() string { return "Edit" }

func (t *EditTool) Description() string {
	return "Replace an exact string in a file. old_string must match the file exactly " +
		"(including indentation) and must be unique unless replace_all is true. " +
		"The file must have been read in this session first."
}

func (t *EditTool) Schema() json.RawMessage {
	return schema(map[string]any{
		"file_path":   prop("string", "Absolute or working-directory-relative path to the file."),
		"old_string":  prop("string", "Exact text to replace. Include enough surrounding context to be unique."),
		"new_string":  prop("string", "Replacement text. Use an empty string to delete the matched text."),
		"replace_all": prop("boolean", "Replace every occurrence (default false: the match must be unique)."),
	}, "file_path", "old_string", "new_string")
}

func (t *EditTool) Gated() bool { return true }

type editArgs struct {
	FilePath   string `json:"file_path"`
	OldString  string `json:"old_string"`
	NewString  string `json:"new_string"`
	ReplaceAll bool   `json:"replace_all"`
}

func (t *EditTool) Run(ctx context.Context, raw json.RawMessage) (string, error) {
	var args editArgs
	if err := decodeArgs(raw, &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if args.OldString == "" {
		return "", fmt.Errorf("old_string is empty — use Write to create or replace a file")
	}
	if args.OldString == args.NewString {
		return "", fmt.Errorf("old_string and new_string are identical — nothing to do")
	}
	path, err := resolvePath(t.Root, args.FilePath)
	if err != nil {
		return "", err
	}
	if !t.Tracker.WasRead(path) {
		return "", fmt.Errorf("%s has not been read in this session — read it first so the edit matches reality", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("cannot read %s: %w", path, err)
	}
	if isBinary(data) {
		return "", fmt.Errorf("%s appears to be binary and cannot be edited as text", path)
	}
	content := string(data)

	count := strings.Count(content, args.OldString)
	if count == 0 {
		return "", fmt.Errorf("old_string was not found in %s — the file may have changed; read it again", path)
	}
	if count > 1 && !args.ReplaceAll {
		return "", fmt.Errorf("old_string occurs %d times in %s — include more surrounding context to make it unique, or set replace_all=true", count, path)
	}
	replaced := 1
	if args.ReplaceAll {
		replaced = count
	}
	updated := strings.Replace(content, args.OldString, args.NewString, replaced)

	info, err := os.Stat(path)
	mode := os.FileMode(0o644)
	if err == nil {
		mode = info.Mode().Perm()
	}
	if err := os.WriteFile(path, []byte(updated), mode); err != nil {
		return "", fmt.Errorf("cannot write %s: %w", path, err)
	}
	t.Tracker.MarkRead(path)
	return fmt.Sprintf("Edited %s: replaced %d occurrence(s).", path, replaced), nil
}
