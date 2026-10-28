// Package tools implements the local tools the model can call (filesystem,
// shell, search, memory) and the registry that exposes them to the turnlith
// facade as OpenAI-style function definitions.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/turnlith-AI/turnlith-CLI/internal/api"
)

// Tool is one callable tool.
type Tool interface {
	Name() string
	Description() string
	Schema() json.RawMessage
	// Gated reports whether calls need user permission (Claude Code style).
	Gated() bool
	// Category classifies the tool for the permission policy: auto mode
	// approves edits without asking, everything else still consults the mode.
	Category() string
	Run(ctx context.Context, args json.RawMessage) (string, error)
}

// Result is what a tool call produced: Text goes to the model, Display (when
// set) is what the user sees in the transcript instead — a diff for an edit,
// where the model only needs a one-line confirmation.
type Result struct {
	Text    string
	Display string
}

// ResultRunner is implemented by tools that have something richer to show the
// user than the text they hand the model.
type ResultRunner interface {
	RunResult(ctx context.Context, args json.RawMessage) (Result, error)
}

// Preparer is implemented by tools that can check a call and describe its
// effect without performing it. The agent loop calls it before asking for
// permission: a call that cannot work (text not found, file unread) comes
// straight back to the model as an error instead of bothering the user, and
// the returned preview (a diff) is what the permission prompt shows.
type Preparer interface {
	Prepare(args json.RawMessage) (preview string, err error)
}

// ArgGater is implemented by tools whose need for permission depends on the
// arguments of a call (a Read is free inside the workspace but asks outside
// it). Calls that need permission are "external": auto mode does not wave them
// through and an "always" answer for the tool does not cover them.
type ArgGater interface {
	NeedsPermission(args json.RawMessage) (reason string, need bool)
}

// Concurrent marks tools whose calls are safe to run side by side with other
// calls in the same model turn: no shared mutable state, no effect on the
// workspace. The agent loop batches adjacent such calls.
type Concurrent interface {
	ConcurrencySafe() bool
}

// Registry holds the active tool set.
type Registry struct {
	list   []Tool
	byName map[string]Tool
}

func NewRegistry(list ...Tool) *Registry {
	r := &Registry{byName: make(map[string]Tool, len(list))}
	for _, t := range list {
		r.list = append(r.list, t)
		r.byName[t.Name()] = t
	}
	return r
}

// Definitions renders the tools as facade tool definitions.
func (r *Registry) Definitions() []api.ToolDef {
	defs := make([]api.ToolDef, 0, len(r.list))
	for _, t := range r.list {
		defs = append(defs, api.NewToolDef(t.Name(), t.Description(), t.Schema()))
	}
	return defs
}

func (r *Registry) Get(name string) (Tool, bool) {
	t, ok := r.byName[name]
	return t, ok
}

func (r *Registry) Names() []string {
	names := make([]string, 0, len(r.list))
	for _, t := range r.list {
		names = append(names, t.Name())
	}
	return names
}

// RunResult executes a tool by name, returning the user-facing display too
// when the tool provides one.
func (r *Registry) RunResult(ctx context.Context, name string, args json.RawMessage) (Result, error) {
	t, ok := r.byName[name]
	if !ok {
		return Result{}, fmt.Errorf("unknown tool %q — available: %s", name, strings.Join(r.Names(), ", "))
	}
	if rr, ok := t.(ResultRunner); ok {
		return rr.RunResult(ctx, args)
	}
	text, err := t.Run(ctx, args)
	return Result{Text: text}, err
}

// Run executes a tool by name.
func (r *Registry) Run(ctx context.Context, name string, args json.RawMessage) (string, error) {
	t, ok := r.byName[name]
	if !ok {
		return "", fmt.Errorf("unknown tool %q — available: %s", name, strings.Join(r.Names(), ", "))
	}
	return t.Run(ctx, args)
}

// FileActivity records what the session has done to one file.
type FileActivity struct {
	Path    string
	Read    bool
	Written bool // created or replaced wholesale
	Edited  bool // modified in place
	Touched time.Time
}

// fileStamp is what a file looked like when the model last saw it.
type fileStamp struct {
	mtime time.Time
	size  int64
}

// Tracker remembers which files the session has read, so Write/Edit can
// refuse to modify files the model has never seen — or has not seen since they
// changed on disk — and which files it has read, written, or edited, for
// /files and the file browser. Entries are keyed by the symlink-resolved path,
// so a file read through a link and edited through its target is one file.
type Tracker struct {
	mu    sync.Mutex
	read  map[string]fileStamp
	files map[string]*FileActivity
	undo  []undoEntry
}

func NewTracker() *Tracker {
	return &Tracker{read: map[string]fileStamp{}, files: map[string]*FileActivity{}}
}

// canonicalPath cleans p and resolves symlinks when the path exists.
func canonicalPath(p string) string {
	p = filepath.Clean(p)
	if real, err := filepath.EvalSymlinks(p); err == nil {
		return real
	}
	return p
}

func (t *Tracker) mark(path string, mark func(*FileActivity)) {
	key := canonicalPath(path)
	var stamp fileStamp
	if info, err := os.Stat(key); err == nil {
		stamp = fileStamp{mtime: info.ModTime(), size: info.Size()}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.read[key] = stamp
	f := t.files[key]
	if f == nil {
		f = &FileActivity{Path: path}
		t.files[key] = f
	}
	mark(f)
	f.Touched = time.Now()
}

func (t *Tracker) MarkRead(path string) {
	t.mark(path, func(f *FileActivity) { f.Read = true })
}

// MarkWritten records a Write; written files count as read for the
// read-before-edit rule.
func (t *Tracker) MarkWritten(path string) {
	t.mark(path, func(f *FileActivity) { f.Written = true })
}

// MarkEdited records an Edit; edited files count as read for the
// read-before-edit rule.
func (t *Tracker) MarkEdited(path string) {
	t.mark(path, func(f *FileActivity) { f.Edited = true })
}

func (t *Tracker) WasRead(path string) bool {
	key := canonicalPath(path)
	t.mu.Lock()
	defer t.mu.Unlock()
	_, ok := t.read[key]
	return ok
}

// Stale reports whether a file the session has seen was modified on disk
// since — by an editor, a formatter, git — so an edit built on the old
// contents would clobber it. Unknown or vanished files are not stale; the
// tool reports those errors itself.
func (t *Tracker) Stale(path string) bool {
	key := canonicalPath(path)
	t.mu.Lock()
	stamp, ok := t.read[key]
	t.mu.Unlock()
	if !ok {
		return false
	}
	info, err := os.Stat(key)
	if err != nil {
		return false
	}
	return !info.ModTime().Equal(stamp.mtime) || info.Size() != stamp.size
}

// Files returns the session's file activity, most recently touched first.
func (t *Tracker) Files() []FileActivity {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]FileActivity, 0, len(t.files))
	for _, f := range t.files {
		out = append(out, *f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Touched.After(out[j].Touched) })
	return out
}

// decodeArgs unmarshals tool arguments; empty arguments decode to zero values.
func decodeArgs(raw json.RawMessage, v any) error {
	if len(raw) == 0 {
		raw = []byte("{}")
	}
	return json.Unmarshal(raw, v)
}

// schema builds a JSON-Schema object for tool parameters.
func schema(props map[string]any, required ...string) json.RawMessage {
	obj := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		obj["required"] = required
	}
	data, _ := json.Marshal(obj)
	return data
}

func prop(typ, description string) map[string]any {
	return map[string]any{"type": typ, "description": description}
}

func enumProp(values []string, description string) map[string]any {
	anyValues := make([]any, len(values))
	for i, v := range values {
		anyValues[i] = v
	}
	return map[string]any{"type": "string", "enum": anyValues, "description": description}
}

func arrayProp(description string) map[string]any {
	return map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": description}
}
