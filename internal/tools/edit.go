package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/turnlith-AI/turnlith-CLI/internal/permissions"
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

// NeedsPermission asks before touching files outside the working directory.
func (t *EditTool) NeedsPermission(raw json.RawMessage) (string, bool) {
	var args struct {
		FilePath string `json:"file_path"`
	}
	if decodeArgs(raw, &args) != nil {
		return "", false
	}
	return outsideWorkspace(t.Root, args.FilePath)
}

func (t *EditTool) Category() string { return permissions.CategoryEdit }

type editArgs struct {
	FilePath   string `json:"file_path"`
	OldString  string `json:"old_string"`
	NewString  string `json:"new_string"`
	ReplaceAll bool   `json:"replace_all"`
}

// editPlan is a validated edit, ready to apply.
type editPlan struct {
	path     string
	mode     os.FileMode
	orig     []byte // the file as it was
	out      []byte // exact bytes to write (line endings restored)
	before   string // LF-normalized text before and after, for the diff
	after    string
	replaced int
}

// plan reads the file and works out the edit without touching it.
func (t *EditTool) plan(raw json.RawMessage) (*editPlan, error) {
	var args editArgs
	if err := decodeArgs(raw, &args); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if args.OldString == "" {
		return nil, fmt.Errorf("old_string is empty — use Write to create or replace a file")
	}
	if args.OldString == args.NewString {
		return nil, fmt.Errorf("old_string and new_string are identical — nothing to do")
	}
	path, err := resolvePath(t.Root, args.FilePath)
	if err != nil {
		return nil, err
	}
	if !t.Tracker.WasRead(path) {
		return nil, fmt.Errorf("%s has not been read in this session — read it first so the edit matches reality", path)
	}
	if t.Tracker.Stale(path) {
		return nil, fmt.Errorf("%s has changed on disk since it was read — read it again so the edit matches reality", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read %s: %w", path, err)
	}
	if IsBinary(data) {
		return nil, fmt.Errorf("%s appears to be binary and cannot be edited as text", path)
	}
	content := string(data)

	// Read shows CRLF files with plain newlines, so the model's old_string
	// carries "\n". Match on LF text and restore CRLF afterwards.
	oldString, newString := args.OldString, args.NewString
	crlf := isPureCRLF(content)
	if crlf {
		content, oldString, newString = toLF(content), toLF(oldString), toLF(newString)
	}

	count := strings.Count(content, oldString)
	if count == 0 {
		return nil, fmt.Errorf("old_string was not found in %s — the file may have changed; read it again", path)
	}
	if count > 1 && !args.ReplaceAll {
		return nil, fmt.Errorf("old_string occurs %d times in %s — include more surrounding context to make it unique, or set replace_all=true", count, path)
	}
	replaced := 1
	if args.ReplaceAll {
		replaced = count
	}
	updated := strings.Replace(content, oldString, newString, replaced)
	out := updated
	if crlf {
		out = toCRLF(updated)
	}
	mode := os.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	return &editPlan{path: path, mode: mode, orig: data, out: []byte(out), before: content, after: updated, replaced: replaced}, nil
}

// Prepare implements Preparer: validate the edit and preview it as a diff.
func (t *EditTool) Prepare(raw json.RawMessage) (string, error) {
	p, err := t.plan(raw)
	if err != nil {
		return "", err
	}
	diff, _, _ := Diff(p.before, p.after, 2, 14)
	return diff, nil
}

func (t *EditTool) Run(ctx context.Context, raw json.RawMessage) (string, error) {
	res, err := t.RunResult(ctx, raw)
	return res.Text, err
}

// RunResult applies the edit; the model gets a one-line confirmation, the
// user a diff.
func (t *EditTool) RunResult(_ context.Context, raw json.RawMessage) (Result, error) {
	p, err := t.plan(raw)
	if err != nil {
		return Result{}, err
	}
	if err := writeFileAtomic(p.path, p.out, p.mode); err != nil {
		return Result{}, fmt.Errorf("cannot write %s: %w", p.path, err)
	}
	t.Tracker.recordChange(p.path, true, p.orig, p.mode, p.out)
	t.Tracker.MarkEdited(p.path)
	return Result{
		Text:    fmt.Sprintf("Edited %s: replaced %d occurrence(s).", p.path, p.replaced),
		Display: changeDisplay(p.before, p.after),
	}, nil
}
