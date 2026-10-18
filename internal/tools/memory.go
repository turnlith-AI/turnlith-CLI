package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/turnlith-AI/turnlith-CLI/internal/api"
)

// SearchMemoryTool queries the agent's own long-term memory through the
// turnlith-controller MCP endpoint: durable facts and older session history
// that no longer fit the assembled context window.
type SearchMemoryTool struct {
	Client *api.Client
	// Agent returns the codename of the agent the conversation currently runs
	// against (memory is per agent).
	Agent func() string
}

func (t *SearchMemoryTool) Name() string { return "SearchMemory" }

func (t *SearchMemoryTool) Description() string {
	return "Search this agent's turnlith memory for older information: durable facts distilled from " +
		"past sessions (scope \"memory\"), the agent's full stored conversation history (scope " +
		"\"session\"), or both (scope \"all\", default). Use short keyword-rich queries when you " +
		"need details from earlier work that is not in the current context."
}

func (t *SearchMemoryTool) Schema() json.RawMessage {
	return schema(map[string]any{
		"query": prop("string", "Short, keyword-rich search query."),
		"scope": enumProp([]string{"all", "session", "memory"}, "What to search: all (default), session, or memory."),
	}, "query")
}

func (t *SearchMemoryTool) Gated() bool { return false }

type memoryArgs struct {
	Query string `json:"query"`
	Scope string `json:"scope"`
}

func (t *SearchMemoryTool) Run(ctx context.Context, raw json.RawMessage) (string, error) {
	var args memoryArgs
	if err := decodeArgs(raw, &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if args.Query == "" {
		return "", fmt.Errorf("query is empty")
	}
	scope := args.Scope
	if scope == "" {
		scope = "all"
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	agent := ""
	if t.Agent != nil {
		agent = t.Agent()
	}
	params := map[string]any{"query": args.Query, "scope": scope}
	if agent != "" {
		params["agent"] = agent
	}
	text, err := t.Client.CallTool(ctx, "search_memory", params)
	if err != nil {
		return "", fmt.Errorf("memory search failed: %w", err)
	}
	if agent != "" {
		return fmt.Sprintf("[agent %s]\n%s", agent, text), nil
	}
	return text, nil
}
