package cli

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/turnlith-AI/turnlith-CLI/internal/history"
	"github.com/turnlith-AI/turnlith-CLI/internal/permissions"
	"github.com/turnlith-AI/turnlith-CLI/internal/ui"
)

func (a *app) runPrint() int {
	prompt := a.opts.prompt
	if piped, ok := readPipedStdin(prompt != ""); ok {
		if prompt == "" {
			prompt = piped
		} else if piped != "" {
			prompt += "\n\n<stdin>\n" + piped + "\n</stdin>"
		}
	}
	if prompt == "" {
		a.errRenderer.Errorf("no prompt given — use `tlc -p \"…\"` or pipe text on stdin")
		return 2
	}
	if err := a.chooseAgent(false); err != nil {
		a.errRenderer.Errorf("%s", friendlyErr(err))
		return 1
	}
	a.renderer.DisableSpinner()
	a.errRenderer.DisableSpinner()
	for _, note := range a.notes {
		a.errRenderer.Notice(note)
	}
	if a.policy.Mode != permissions.ModeYolo {
		a.policy.Prompter = printPrompter{renderer: a.errRenderer, theme: a.theme}
	}
	if session, err := history.Start(a.cwd, a.agent.Codename); err == nil {
		a.transcript = session
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	defer signal.Stop(sigCh)
	go func() {
		<-sigCh
		cancel()
	}()

	wire := a.attachMentions(prompt, a.errRenderer)
	err := a.runPrompt(ctx, prompt, wire, a.renderer, a.errRenderer)
	switch {
	case err == nil:
		return 0
	case errors.Is(err, context.Canceled), errors.Is(err, permissions.ErrAborted):
		return exitInterrupted
	default:
		return 1
	}
}

// exitInterrupted is the conventional shell status for SIGINT (128 + 2).
const exitInterrupted = 130

// stdinIdleGrace is how long print mode waits for the first byte of piped
// input when a prompt was already given, so a stdin that is attached but never
// written to (a CI runner, a parent holding the pipe open) cannot hang a run.
var stdinIdleGrace = 3 * time.Second

// readPipedStdin returns the text piped into the process, trimmed. ok is false
// when stdin is a terminal or unreadable. With hasPrompt the read gives up if
// no byte arrives within stdinIdleGrace; without one the prompt can only come
// from stdin, so it waits for EOF.
func readPipedStdin(hasPrompt bool) (string, bool) {
	info, err := os.Stdin.Stat()
	if err != nil || info.Mode()&os.ModeCharDevice != 0 {
		return "", false
	}
	type result struct {
		data []byte
		err  error
	}
	done := make(chan result, 1)
	started := make(chan struct{})
	go func() {
		r := bufio.NewReader(io.LimitReader(os.Stdin, 4*1024*1024))
		if _, err := r.Peek(1); err != nil && err != io.EOF {
			close(started)
			done <- result{err: err}
			return
		}
		close(started)
		data, err := io.ReadAll(r)
		done <- result{data: data, err: err}
	}()
	if hasPrompt {
		select {
		case <-started:
		case <-time.After(stdinIdleGrace):
			return "", false
		}
	}
	res := <-done
	if res.err != nil {
		return "", false
	}
	return strings.TrimSpace(string(res.data)), true
}

// printPrompter denies gated tools in non-interactive mode.
type printPrompter struct {
	renderer *ui.Renderer
	theme    *ui.Theme
}

func (p printPrompter) Prompt(req permissions.Request) (permissions.Decision, error) {
	p.renderer.Infof("%s", p.theme.Yellow("· denied "+req.Tool+" (non-interactive; pass --yolo to allow)"))
	return permissions.Deny, nil
}
