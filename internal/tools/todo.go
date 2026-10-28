package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/turnlith-AI/turnlith-CLI/internal/permissions"
)

// TodoItem is one checklist entry.
type TodoItem struct {
	Content string `json:"content"`
	Status  string `json:"status"` // pending | in_progress | completed
}

// TodoTool keeps the agent's working checklist for multi-step tasks. It lives
// in memory only and replaces the whole list on every call, so the list the
// model sees is always the list it last wrote — and the user, who sees the
// rendered result, can follow along.
type TodoTool struct {
	mu    sync.Mutex
	items []TodoItem
}

const (
	todoMaxItems   = 30
	todoMaxContent = 300
)

func (t *TodoTool) Name() string { return "TodoWrite" }

func (t *TodoTool) Description() string {
	return "Keep a short checklist while you work on a task with three or more steps. " +
		"Send the complete list every time (it replaces the previous one). " +
		"Keep exactly one item in_progress, mark items completed as soon as they are done, " +
		"and skip the tool entirely for simple requests."
}

func (t *TodoTool) Schema() json.RawMessage {
	return json.RawMessage(`{
  "type": "object",
  "properties": {
    "todos": {
      "type": "array",
      "description": "The full checklist, in order.",
      "items": {
        "type": "object",
        "properties": {
          "content": {"type": "string", "description": "What to do, in the imperative (\"Add the retry wrapper\")."},
          "status": {"type": "string", "enum": ["pending", "in_progress", "completed"]}
        },
        "required": ["content", "status"]
      }
    }
  },
  "required": ["todos"]
}`)
}

func (t *TodoTool) Gated() bool      { return false }
func (t *TodoTool) Category() string { return permissions.CategoryMemory }

// Items returns a copy of the current list.
func (t *TodoTool) Items() []TodoItem {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]TodoItem(nil), t.items...)
}

func (t *TodoTool) Run(_ context.Context, raw json.RawMessage) (string, error) {
	var args struct {
		Todos *[]TodoItem `json:"todos"`
	}
	if err := decodeArgs(raw, &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if args.Todos == nil {
		return "", fmt.Errorf("todos is required (send an empty list to clear it)")
	}
	items := make([]TodoItem, 0, len(*args.Todos))
	inProgress := 0
	if len(*args.Todos) > todoMaxItems {
		return "", fmt.Errorf("too many items (%d, max %d) — keep the list to the steps that matter", len(*args.Todos), todoMaxItems)
	}
	for i, it := range *args.Todos {
		it.Content = strings.TrimSpace(it.Content)
		switch {
		case it.Content == "":
			return "", fmt.Errorf("item %d has no content", i+1)
		case len(it.Content) > todoMaxContent:
			return "", fmt.Errorf("item %d is too long (%d characters, max %d)", i+1, len(it.Content), todoMaxContent)
		}
		switch it.Status {
		case "pending", "completed":
		case "in_progress":
			inProgress++
		default:
			return "", fmt.Errorf("item %d has unknown status %q — use pending, in_progress or completed", i+1, it.Status)
		}
		items = append(items, it)
	}
	if inProgress > 1 {
		return "", fmt.Errorf("%d items are in_progress; keep exactly one", inProgress)
	}

	t.mu.Lock()
	t.items = items
	t.mu.Unlock()

	if len(items) == 0 {
		return "Todo list cleared.", nil
	}
	var b strings.Builder
	done := 0
	for _, it := range items {
		switch it.Status {
		case "completed":
			done++
			b.WriteString("[x] ")
		case "in_progress":
			b.WriteString("[~] ")
		default:
			b.WriteString("[ ] ")
		}
		b.WriteString(it.Content + "\n")
	}
	fmt.Fprintf(&b, "(%d/%d done)", done, len(items))
	return b.String(), nil
}
