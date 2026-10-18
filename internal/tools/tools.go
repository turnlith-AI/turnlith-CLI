// Package tools implements the local tools the model can call (filesystem,
// shell, search, memory) and the registry that exposes them to the turnlith
// facade as OpenAI-style function definitions.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/turnlith-AI/turnlith-CLI/internal/api"
)

// Tool is one callable tool.
type Tool interface {
	Name() string
	Description() string
	Schema() json.RawMessage
	// Gated reports whether calls need user permission (Claude Code style).
	Gated() bool
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

// Tracker remembers which files the session has read, so Write/Edit can
// refuse to modify files the model has never seen.
type Tracker struct {
	mu   sync.Mutex
	read map[string]bool
}

func NewTracker() *Tracker { return &Tracker{read: map[string]bool{}} }

func (t *Tracker) MarkRead(path string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.read[path] = true
}

func (t *Tracker) WasRead(path string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.read[path]
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
