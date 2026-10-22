// Package tools implements the local tools the model can call (filesystem,
// shell, search, memory) and the registry that exposes them to the turnlith
// facade as OpenAI-style function definitions.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
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

// Tracker remembers which files the session has read, so Write/Edit can
// refuse to modify files the model has never seen — and which files it has
// read, written, or edited, for /files and the file browser.
type Tracker struct {
	mu    sync.Mutex
	read  map[string]bool
	files map[string]*FileActivity
}

func NewTracker() *Tracker {
	return &Tracker{read: map[string]bool{}, files: map[string]*FileActivity{}}
}

func (t *Tracker) mark(path string, mark func(*FileActivity)) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.read[path] = true
	f := t.files[path]
	if f == nil {
		f = &FileActivity{Path: path}
		t.files[path] = f
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
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.read[path]
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
