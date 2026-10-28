package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/turnlith-AI/turnlith-CLI/internal/agent"
	"github.com/turnlith-AI/turnlith-CLI/internal/api"
	"github.com/turnlith-AI/turnlith-CLI/internal/permissions"
	"github.com/turnlith-AI/turnlith-CLI/internal/ui"
)

// runStats is what the last run did, for the --output-format result object.
type runStats struct {
	lastText      string // text of the final model turn: the answer
	turns         int
	toolCalls     int
	contextTokens int // context size of the latest turn
	outputTokens  int // generated tokens across all turns
}

// emit sends a machine-readable event (stream-json mode); a no-op otherwise.
func (a *app) emit(ev map[string]any) {
	if a.onEvent != nil {
		a.onEvent(ev)
	}
}

// rawOrString embeds tool arguments as JSON when they parse, as text if not.
func rawOrString(raw string) any {
	if json.Valid([]byte(raw)) && strings.TrimSpace(raw) != "" {
		return json.RawMessage(raw)
	}
	return raw
}

// runPrompt runs one prompt to completion, reporting problems on activity. It
// returns the run's error (already reported) so print mode can pick an exit
// code; the interactive loop ignores it and prompts again.
func (a *app) runPrompt(ctx context.Context, display, wire string, out, activity *ui.Renderer) error {
	a.transcript.Append("user", display)
	a.stats = runStats{}

	var turnText strings.Builder
	var spinner *ui.Spinner
	stopSpinner := func() {
		if spinner != nil {
			spinner.Stop()
			spinner = nil
		}
	}
	startSpinner := func(msg string, delay time.Duration) {
		stopSpinner()
		spinner = out.StartSpinnerAfter(msg, delay)
	}
	startSpinner("thinking…", 0)

	// Retries happen inside the client, before any output of the turn; say so
	// instead of leaving the user staring at a spinner.
	a.client.OnRetry = func(attempt int, wait time.Duration, err error) {
		stopSpinner()
		activity.Notice(fmt.Sprintf("%s — retrying in %s (attempt %d)", retryReason(err), wait.Round(100*time.Millisecond), attempt))
		startSpinner("thinking…", 0)
	}
	defer func() { a.client.OnRetry = nil }()

	events := agent.Events{
		OnText: func(delta string) {
			stopSpinner()
			out.Text(delta)
			turnText.WriteString(delta)
			a.emit(map[string]any{"type": "text", "delta": delta})
		},
		OnTurnEnd: func() {
			out.EndText()
			a.stats.lastText = turnText.String()
			if turnText.Len() > 0 {
				a.transcript.Append("assistant", turnText.String())
				turnText.Reset()
			}
		},
		OnToolStart: func(name, rawArgs string) {
			stopSpinner()
			summary, _ := ui.ToolCallSummary(name, rawArgs)
			activity.ToolStart(name, summary)
			a.stats.toolCalls++
			a.emit(map[string]any{"type": "tool_use", "name": name, "input": rawOrString(rawArgs)})
		},
		OnToolExec: func(name string) {
			// only slow tools earn a spinner; quick reads never flash one
			startSpinner("running "+name+"…", 400*time.Millisecond)
		},
		OnToolEnd: func(name, result string, denied bool, elapsed time.Duration) {
			stopSpinner()
			activity.ToolResult(result, denied, elapsed)
			a.emit(map[string]any{
				"type": "tool_result", "name": name, "denied": denied,
				"is_error": denied || strings.HasPrefix(result, "Error:"),
				"content":  truncateForEvent(result),
			})
			startSpinner("thinking…", 0)
		},
		OnNotice: func(msg string) {
			stopSpinner()
			activity.Notice(msg)
			a.emit(map[string]any{"type": "notice", "message": msg})
		},
		OnUsage: func(u api.Usage) {
			a.stats.turns++
			a.stats.contextTokens = u.PromptTokens
			a.stats.outputTokens += u.CompletionTokens
			if a.verbose {
				activity.UsageLine(u.PromptTokens, u.CompletionTokens)
			}
		},
	}
	err := a.runner.Run(ctx, wire, events)
	stopSpinner()
	out.EndText()

	// In print mode out is stdout and activity is stderr, so errors and tool
	// activity never pollute piped assistant text; interactively both are
	// stdout and the experience is unchanged.
	switch {
	case err == nil:
	case errors.Is(err, agent.ErrStepLimit):
		// the step-limit notice already said what happened
	case errors.Is(err, context.Canceled), errors.Is(err, permissions.ErrAborted):
		activity.Notice("interrupted")
	default:
		activity.Errorf("%s", friendlyErr(err))
	}
	return err
}

// truncateForEvent bounds a tool result embedded in a stream-json event.
func truncateForEvent(s string) string {
	const max = 4000
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}
