package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"

	"github.com/turnlith-AI/turnlith-CLI/internal/history"
	"github.com/turnlith-AI/turnlith-CLI/internal/permissions"
	"github.com/turnlith-AI/turnlith-CLI/internal/ui"
	"github.com/turnlith-AI/turnlith-CLI/internal/version"
)

func (a *app) runInteractive() int {
	if !ui.IsTerminal(os.Stdin) || !ui.IsTerminal(os.Stdout) {
		a.renderer.Errorf("stdin is not a terminal — use `tlc -p \"…\"` for one-shot prompts")
		return 1
	}
	repl, err := ui.NewREPL(a.renderer, a.theme)
	if err != nil {
		a.renderer.Errorf("cannot start the prompt: %v", err)
		return 1
	}
	a.repl = repl
	repl.SetCandidates(a.completeArgument)
	repl.SetPermissionMode(a.policy.Mode)
	repl.SetOnTabEmpty(func() {
		mode := permissions.NextSafeMode(a.policy.Mode)
		a.policy.Mode = mode
		repl.SetPermissionMode(mode)
		repl.WriteAbove(permissionModeLine(a.theme, mode) + "\n")
		repl.Refresh()
	})
	a.policy.Prompter = repl

	if err := a.chooseAgent(true); err != nil {
		a.renderer.Errorf("%s", friendlyErr(err))
		return 1
	}
	a.printBanner()
	a.openTranscript(a.opts.cont)

	if a.opts.prompt != "" {
		a.runWithCancel(a.opts.prompt)
	}

	interrupts := 0
	for {
		line, err := a.readInput()
		if errors.Is(err, ui.ErrInterrupt) {
			// one Ctrl-C clears the line; a second in a row quits
			if interrupts > 0 {
				a.renderer.Infof("%s", a.theme.Dim("bye"))
				return 0
			}
			interrupts++
			a.renderer.Notice("press Ctrl-C again to quit")
			continue
		}
		interrupts = 0
		if err == io.EOF {
			a.renderer.Infof("%s", a.theme.Dim("bye"))
			return 0
		}
		if err != nil {
			a.renderer.Errorf("%v", err)
			return 1
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "/") {
			if a.handleSlash(line) {
				return 0
			}
			continue
		}
		a.runWithCancel(line)
	}
}

// openFileBrowser runs the /files browser and pre-fills the next prompt with
// the picked file as an @mention.
func (a *app) openFileBrowser() {
	var touched []ui.FileBadge
	for _, f := range a.tracker.Files() {
		rel, err := filepath.Rel(a.cwd, f.Path)
		if err != nil || strings.HasPrefix(rel, "..") {
			continue // touched outside the workspace (e.g. ~ paths)
		}
		touched = append(touched, ui.FileBadge{
			Path: filepath.ToSlash(rel), Read: f.Read, Written: f.Written, Edited: f.Edited,
		})
	}
	picked, err := ui.RunFileBrowser(a.cwd, touched, a.theme, os.Stdout)
	if err != nil {
		a.renderer.Errorf("file browser: %v", err)
		return
	}
	if picked != "" && a.repl != nil {
		a.repl.Prefill("@" + picked + " ")
	}
}

// printBanner prints the session header.
func (a *app) printBanner() {
	a.renderer.Infof("%s %s · %s · %s",
		a.theme.BoldAccent("tlc"),
		a.theme.Dim("v"+version.Version),
		a.theme.Accent(a.agent.Codename)+a.theme.Dim(" ("+modelNote(a.agent)+")"),
		a.theme.Dim(shortPath(a.cwd)),
	)
	modeHint := ""
	if a.policy.Mode != permissions.ModeAsk {
		modeHint = " · permissions: " + a.policy.Mode.String()
	}
	a.renderer.Infof("%s", a.theme.Faint("type /help for commands · Tab cycles permission modes · /exit to quit"+modeHint))
	for _, note := range a.notes {
		a.renderer.Notice(note)
	}
}

// permissionModeLine is the status line shown when the mode changes.
func permissionModeLine(t *ui.Theme, mode permissions.Mode) string {
	switch mode {
	case permissions.ModeAuto:
		return t.Yellow("⏵ auto — file edits run without asking, Bash still asks")
	case permissions.ModePlan:
		return t.Violet("⏸ plan — read-only: the agent can look and plan, not edit or run commands")
	case permissions.ModeYolo:
		return t.Red("⏵⏵ yolo — every tool runs without asking")
	default:
		return t.Green("normal — Bash, Write and Edit ask before running")
	}
}

// setPermissionMode switches the mode, restyles the prompt, and reports it.
func (a *app) setPermissionMode(mode permissions.Mode) {
	a.policy.Mode = mode
	if a.repl != nil {
		a.repl.SetPermissionMode(mode)
	}
	a.renderer.Infof("%s", permissionModeLine(a.theme, mode))
}

func (a *app) openTranscript(cont bool) {
	if cont {
		session, entries, err := history.ContinueFor(a.cwd, a.agent.Codename)
		if err == nil && session != nil {
			a.transcript = session
			if session.Agent != "" && session.Agent != a.agent.Codename {
				a.renderer.Notice(fmt.Sprintf("previous transcript was with agent %q — server memory is per agent", session.Agent))
			}
			a.replay(entries)
			return
		}
		a.renderer.Notice("no previous transcript for this directory — starting fresh")
	}
	session, err := history.Start(a.cwd, a.agent.Codename)
	if err != nil {
		a.renderer.Notice("could not open a transcript file: " + err.Error())
		return
	}
	a.transcript = session
}

func (a *app) replay(entries []history.Entry) {
	if len(entries) == 0 {
		a.renderer.Notice("previous transcript is empty")
		return
	}
	const maxReplay = 12
	shown := entries
	if len(shown) > maxReplay {
		shown = shown[len(shown)-maxReplay:]
	}
	a.renderer.Notice(fmt.Sprintf("continuing — last %d of %d stored messages", len(shown), len(entries)))
	for _, e := range shown {
		switch e.Role {
		case "user":
			a.renderer.Infof("%s %s", a.theme.Accent("you ›"), indentBlock(e.Text, "  "))
		case "assistant":
			a.renderer.Infof("%s %s", a.theme.Violet("tlc ›"), indentBlock(e.Text, "  "))
		}
	}
	a.renderer.Infof("%s", a.theme.Faint("(server memory carries the full conversation; the transcript above is local)"))
}

func indentBlock(text, indent string) string {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	for i := range lines {
		if i > 0 {
			lines[i] = indent + lines[i]
		}
	}
	return strings.Join(lines, "\n")
}

// runWithCancel runs one prompt with Ctrl-C cancelling the run. The raw text
// is kept for the transcript; @file mentions are expanded on the wire.
func (a *app) runWithCancel(prompt string) {
	wire := a.withPendingNotes(a.attachMentions(prompt, a.renderer))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	done := make(chan struct{})
	go func() {
		select {
		case <-sigCh:
			a.renderer.Notice("interrupting…")
			cancel()
		case <-done:
		}
	}()
	defer func() {
		signal.Stop(sigCh)
		close(done)
	}()
	_ = a.runPrompt(ctx, prompt, wire, a.renderer, a.renderer)
}
