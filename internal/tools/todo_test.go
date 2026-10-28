package tools

import (
	"strings"
	"testing"
)

func todos(items ...[2]string) map[string]any {
	list := make([]map[string]string, 0, len(items))
	for _, it := range items {
		list = append(list, map[string]string{"content": it[0], "status": it[1]})
	}
	return map[string]any{"todos": list}
}

func TestTodoWriteReplacesTheListAndRendersIt(t *testing.T) {
	tool := &TodoTool{}
	out := run(t, tool, todos(
		[2]string{"read the config loader", "completed"},
		[2]string{"add the retry wrapper", "in_progress"},
		[2]string{"run the test suite", "pending"},
	))
	for _, want := range []string{"[x] read the config loader", "[~] add the retry wrapper", "[ ] run the test suite", "1/3 done"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if got := tool.Items(); len(got) != 3 || got[1].Status != "in_progress" {
		t.Fatalf("items = %+v", got)
	}

	out = run(t, tool, todos([2]string{"only one left", "in_progress"}))
	if got := tool.Items(); len(got) != 1 {
		t.Fatalf("a new list replaces the old one: %+v", got)
	}
	if strings.Contains(out, "read the config loader") {
		t.Fatalf("stale items in output:\n%s", out)
	}

	out = run(t, tool, map[string]any{"todos": []any{}})
	if len(tool.Items()) != 0 || !strings.Contains(out, "cleared") {
		t.Fatalf("empty list should clear: %q %+v", out, tool.Items())
	}
}

func TestTodoWriteValidates(t *testing.T) {
	tool := &TodoTool{}
	cases := map[string]map[string]any{
		"empty content":   todos([2]string{"  ", "pending"}),
		"unknown status":  todos([2]string{"x", "doing"}),
		"two in progress": todos([2]string{"a", "in_progress"}, [2]string{"b", "in_progress"}),
		"no list":         {},
	}
	for name, args := range cases {
		if out := run(t, tool, args); !strings.HasPrefix(out, "Error:") {
			t.Errorf("%s: expected an error, got %q", name, out)
		}
	}
	var many [][2]string
	for i := 0; i < 31; i++ {
		many = append(many, [2]string{"item", "pending"})
	}
	if out := run(t, tool, todos(many...)); !strings.Contains(out, "too many") {
		t.Errorf("31 items: %q", out)
	}
	if out := run(t, tool, todos([2]string{strings.Repeat("x", 301), "pending"})); !strings.Contains(out, "too long") {
		t.Errorf("long item: %q", out)
	}
	if len(tool.Items()) != 0 {
		t.Fatalf("an invalid update must not change the list: %+v", tool.Items())
	}
}

func TestTodoToolIsFreeToUse(t *testing.T) {
	var tool Tool = &TodoTool{}
	if tool.Gated() {
		t.Fatal("tracking a checklist must never prompt")
	}
	if tool.Name() != "TodoWrite" {
		t.Fatalf("name = %q", tool.Name())
	}
	if _, ok := tool.(Concurrent); ok && tool.(Concurrent).ConcurrencySafe() {
		t.Fatal("TodoWrite mutates shared state; it must not run concurrently with itself")
	}
}
