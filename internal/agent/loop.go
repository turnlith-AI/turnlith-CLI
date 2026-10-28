package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
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
	// Root is the workspace directory; permission rules match file paths
	// relative to it.
	Root string
	// MaxSteps caps tool rounds per prompt (0 = default).
	MaxSteps int

	history []api.Message
}

// ToolOutcome describes one finished tool call for display. Result is what
// the model was told; Display, when set, is what the user should see instead
// (a diff for an edit).
type ToolOutcome struct {
	Name    string
	Result  string
	Display string
	Denied  bool
	Elapsed time.Duration
}

// Events receives loop progress for display.
type Events struct {
	OnText      func(delta string)
	OnTurnEnd   func() // one assistant turn finished streaming
	OnToolStart func(name, rawArgs string)
	// OnToolExec fires once a call is cleared to run (after any permission
	// prompt), right before the tool starts — the moment to show activity.
	OnToolExec func(name string)
	OnToolEnd  func(ToolOutcome)
	OnNotice   func(msg string)
	OnUsage    func(usage api.Usage)
}

const defaultMaxSteps = 40

// ErrStepLimit ends a run that used up its model turns without finishing. The
// OnNotice event already told the user; callers that script tlc treat it as a
// failure.
var ErrStepLimit = errors.New("step limit reached")

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
			return ErrStepLimit
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
		if turn.FinishReason == "length" && ev.OnNotice != nil {
			ev.OnNotice("the model hit its output limit and the reply was cut off")
		}

		r.history = append(r.history, api.AssistantMessage(turn.Text, turn.ToolCalls))
		if len(turn.ToolCalls) == 0 {
			return nil
		}
		results, err := r.executeCalls(ctx, turn.ToolCalls, ev)
		if err != nil {
			return err
		}
		for i, call := range turn.ToolCalls {
			r.history = append(r.history, api.ToolResultMessage(call.ID, call.Function.Name, results[i]))
		}
	}
}

// maxParallelTools bounds how many read-only calls of one model turn run at
// once.
const maxParallelTools = 4

// parallelSafe reports whether a call can be batched: a known tool that
// declares itself concurrency-safe, needs no permission for these arguments,
// and so can neither prompt nor change anything.
func (r *Runner) parallelSafe(call api.ToolCall) bool {
	tool, ok := r.Tools.Get(call.Function.Name)
	if !ok || tool.Gated() {
		return false
	}
	if c, ok := tool.(tools.Concurrent); !ok || !c.ConcurrencySafe() {
		return false
	}
	if g, ok := tool.(tools.ArgGater); ok {
		if _, need := g.NeedsPermission(json.RawMessage(call.Function.Arguments)); need {
			return false
		}
	}
	return true
}

// executeCalls runs one model turn's tool calls and returns their results in
// call order. Runs of adjacent concurrency-safe calls execute side by side;
// everything else runs one at a time in the order asked, so effects keep their
// order and permission prompts never overlap.
func (r *Runner) executeCalls(ctx context.Context, calls []api.ToolCall, ev Events) ([]string, error) {
	results := make([]string, len(calls))
	for i := 0; i < len(calls); {
		j := i
		for j < len(calls) && r.parallelSafe(calls[j]) {
			j++
		}
		if j-i >= 2 {
			copy(results[i:j], r.runBatch(ctx, calls[i:j], ev))
			i = j
			continue
		}
		result, err := r.execute(ctx, calls[i], ev)
		if err != nil {
			return nil, err
		}
		results[i] = result
		i++
	}
	return results, nil
}

// runBatch executes safe calls concurrently, then reports them to the UI in
// order as ordinary start/end pairs (output from overlapping calls would
// otherwise interleave).
func (r *Runner) runBatch(ctx context.Context, calls []api.ToolCall, ev Events) []string {
	outs := make([]tools.Result, len(calls))
	elapsed := make([]time.Duration, len(calls))
	sem := make(chan struct{}, maxParallelTools)
	var wg sync.WaitGroup
	for i := range calls {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			start := time.Now()
			outs[i] = r.runTool(ctx, calls[i].Function.Name, calls[i].Function.Arguments)
			elapsed[i] = time.Since(start)
		}(i)
	}
	wg.Wait()
	texts := make([]string, len(calls))
	for i, call := range calls {
		texts[i] = outs[i].Text
		if ev.OnToolStart != nil {
			ev.OnToolStart(call.Function.Name, call.Function.Arguments)
		}
		if ev.OnToolEnd != nil {
			ev.OnToolEnd(ToolOutcome{Name: call.Function.Name, Result: outs[i].Text, Display: outs[i].Display, Elapsed: elapsed[i]})
		}
	}
	return texts
}

// execute runs one tool call: validate it, ask permission if it needs any,
// run it. A call that cannot work (text not found, file never read) comes back
// as an error without bothering the user.
func (r *Runner) execute(ctx context.Context, call api.ToolCall, ev Events) (string, error) {
	name := call.Function.Name
	args := json.RawMessage(call.Function.Arguments)
	if ev.OnToolStart != nil {
		ev.OnToolStart(name, call.Function.Arguments)
	}
	var elapsed time.Duration // the tool's own run time, not the wait at a prompt

	tool, known := r.Tools.Get(name)
	var res tools.Result
	denied := false
	switch {
	case !known:
		res.Text = fmt.Sprintf("Error: unknown tool %q — available: %s", name, strings.Join(r.Tools.Names(), ", "))
	default:
		var preview string
		var prepErr error
		if p, ok := tool.(tools.Preparer); ok {
			preview, prepErr = p.Prepare(args)
		}
		// A tool may be free in general yet need a yes for particular
		// arguments (a Read outside the working directory).
		var external bool
		var reason string
		if g, ok := tool.(tools.ArgGater); ok {
			reason, external = g.NeedsPermission(args)
		}
		switch {
		case prepErr != nil:
			res.Text = "Error: " + prepErr.Error()
		case tool.Gated() || external:
			summary, uiPreview := ui.ToolCallSummary(name, call.Function.Arguments)
			if preview != "" {
				uiPreview = preview
			}
			approved, err := r.Policy.Approve(ctx, permissions.Request{
				Tool:     name,
				Category: tool.Category(),
				Summary:  summary,
				Preview:  uiPreview,
				Subject:  tools.Subject(r.Root, name, args),
				External: external,
				Reason:   reason,
			})
			if err != nil {
				return "", err
			}
			if !approved {
				denied = true
				res.Text = r.Policy.DenialFor(name)
			} else {
				res, elapsed = r.timedExec(ctx, name, call.Function.Arguments, ev)
			}
		default:
			res, elapsed = r.timedExec(ctx, name, call.Function.Arguments, ev)
		}
	}

	if ev.OnToolEnd != nil {
		ev.OnToolEnd(ToolOutcome{Name: name, Result: res.Text, Display: res.Display, Denied: denied, Elapsed: elapsed})
	}
	return res.Text, nil
}

// timedExec announces and runs a cleared call and reports how long it took.
func (r *Runner) timedExec(ctx context.Context, name, rawArgs string, ev Events) (tools.Result, time.Duration) {
	if ev.OnToolExec != nil {
		ev.OnToolExec(name)
	}
	start := time.Now()
	res := r.runTool(ctx, name, rawArgs)
	return res, time.Since(start)
}

func (r *Runner) runTool(ctx context.Context, name, rawArgs string) tools.Result {
	res, err := r.Tools.RunResult(ctx, name, json.RawMessage(rawArgs))
	if err != nil {
		if ctx.Err() != nil {
			return tools.Result{Text: "Error: interrupted"}
		}
		return tools.Result{Text: "Error: " + err.Error()}
	}
	return res
}
