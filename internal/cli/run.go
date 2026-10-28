package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/turnlith-AI/turnlith-CLI/internal/agent"
	"github.com/turnlith-AI/turnlith-CLI/internal/api"
	"github.com/turnlith-AI/turnlith-CLI/internal/permissions"
	"github.com/turnlith-AI/turnlith-CLI/internal/ui"
)

// runPrompt runs one prompt to completion, reporting problems on activity. It
// returns the run's error (already reported) so print mode can pick an exit
// code; the interactive loop ignores it and prompts again.
func (a *app) runPrompt(ctx context.Context, display, wire string, out, activity *ui.Renderer) error {
	a.transcript.Append("user", display)

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
		},
		OnTurnEnd: func() {
			out.EndText()
			if turnText.Len() > 0 {
				a.transcript.Append("assistant", turnText.String())
				turnText.Reset()
			}
		},
		OnToolStart: func(name, rawArgs string) {
			stopSpinner()
			summary, _ := ui.ToolCallSummary(name, rawArgs)
			activity.ToolStart(name, summary)
		},
		OnToolExec: func(name string) {
			// only slow tools earn a spinner; quick reads never flash one
			startSpinner("running "+name+"…", 400*time.Millisecond)
		},
		OnToolEnd: func(name, result string, denied bool, elapsed time.Duration) {
			stopSpinner()
			activity.ToolResult(result, denied, elapsed)
			startSpinner("thinking…", 0)
		},
		OnNotice: func(msg string) {
			stopSpinner()
			activity.Notice(msg)
		},
		OnUsage: func(u api.Usage) {
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
