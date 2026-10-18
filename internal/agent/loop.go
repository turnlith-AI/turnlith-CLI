package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/turnlith-AI/turnlith-CLI/internal/api"
	"github.com/turnlith-AI/turnlith-CLI/internal/permissions"
	"github.com/turnlith-AI/turnlith-CLI/internal/tools"
	"github.com/turnlith-AI/turnlith-CLI/internal/ui"
)

// Runner drives one agent conversation. The facade owns context assembly;
// the runner only keeps the current task's wire history (prompt + tool
// rounds) and replays it on every step.
type Runner struct {
	Client       *api.Client
	Tools        *tools.Registry
	Policy       *permissions.Policy
	Agent        string
	SystemPrompt string
	// MaxSteps caps tool rounds per prompt (0 = default).
	MaxSteps int

	history []api.Message
}

// Events receives loop progress for display.
type Events struct {
	OnText      func(delta string)
	OnTurnEnd   func() // one assistant turn finished streaming
	OnToolStart func(name, rawArgs string)
	OnToolEnd   func(name, result string, denied bool, elapsed time.Duration)
	OnNotice    func(msg string)
	OnUsage     func(usage api.Usage)
}

const defaultMaxSteps = 40

// Reset drops the local task history; server-side memory is unaffected.
func (r *Runner) Reset() { r.history = nil }

// SetAgent switches the agent (and resets the local task history, since the
// wire history belongs to one agent's task).
func (r *Runner) SetAgent(codename string) {
	r.Agent = codename
	r.Reset()
}

// Run processes one user prompt to completion, executing tool rounds until
// the model answers without tool calls. An empty prompt continues the current
// task with replayed pending results only (not used by the interactive loop).
func (r *Runner) Run(ctx context.Context, prompt string, ev Events) error {
	if strings.TrimSpace(prompt) != "" {
		r.history = []api.Message{api.UserMessage(prompt)}
	}
	if len(r.history) == 0 {
		return errors.New("no prompt and no task in progress")
	}

	maxSteps := r.MaxSteps
	if maxSteps <= 0 {
		maxSteps = defaultMaxSteps
	}
	for step := 0; ; step++ {
		if step >= maxSteps {
			if ev.OnNotice != nil {
				ev.OnNotice(fmt.Sprintf("reached the %d-step tool limit — stopping; ask again to continue", maxSteps))
			}
			return nil
		}

		req := api.ChatRequest{
			Model:    r.Agent,
			Messages: append([]api.Message{api.SystemMessage(r.SystemPrompt)}, r.history...),
			Tools:    r.Tools.Definitions(),
		}
		var handlers api.StreamHandlers
		if ev.OnText != nil {
			handlers.OnText = ev.OnText
		}
		turn, err := r.Client.StreamChat(ctx, req, handlers)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		if ev.OnTurnEnd != nil {
			ev.OnTurnEnd()
		}
		if ev.OnUsage != nil {
			ev.OnUsage(turn.Usage)
		}

		r.history = append(r.history, api.AssistantMessage(turn.Text, turn.ToolCalls))
		if len(turn.ToolCalls) == 0 {
			return nil
		}
		for _, call := range turn.ToolCalls {
			result, err := r.execute(ctx, call, ev)
			if err != nil {
				return err
			}
			r.history = append(r.history, api.ToolResultMessage(call.ID, call.Function.Name, result))
		}
	}
}

// execute runs one tool call behind the permission gate.
func (r *Runner) execute(ctx context.Context, call api.ToolCall, ev Events) (string, error) {
	name := call.Function.Name
	if ev.OnToolStart != nil {
		ev.OnToolStart(name, call.Function.Arguments)
	}
	start := time.Now()

	tool, known := r.Tools.Get(name)
	var result string
	denied := false
	switch {
	case !known:
		result = fmt.Sprintf("Error: unknown tool %q — available: %s", name, strings.Join(r.Tools.Names(), ", "))
	case tool.Gated():
		summary, preview := ui.ToolCallSummary(name, call.Function.Arguments)
		approved, err := r.Policy.Approve(ctx, permissions.Request{Tool: name, Summary: summary, Preview: preview})
		if err != nil {
			return "", err
		}
		if !approved {
			denied = true
			result = permissions.DenialMessage(name)
		} else {
			result = r.runTool(ctx, name, call.Function.Arguments)
		}
	default:
		result = r.runTool(ctx, name, call.Function.Arguments)
	}

	if ev.OnToolEnd != nil {
		ev.OnToolEnd(name, result, denied, time.Since(start))
	}
	return result, nil
}

func (r *Runner) runTool(ctx context.Context, name, rawArgs string) string {
	out, err := r.Tools.Run(ctx, name, json.RawMessage(rawArgs))
	if err != nil {
		if ctx.Err() != nil {
			return "Error: interrupted"
		}
		return "Error: " + err.Error()
	}
	return out
}
