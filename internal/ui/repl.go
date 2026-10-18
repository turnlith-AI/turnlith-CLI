package ui

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/ergochat/readline"

	"github.com/turnlith-AI/turnlith-CLI/internal/config"
	"github.com/turnlith-AI/turnlith-CLI/internal/permissions"
)

// The placeholder stands in for pasted newlines: readline submits on both
// CR and LF, so LF is filtered into this rune to make multi-line paste land
// in the buffer instead of firing one submission per line.
const newlinePlaceholder = '↵'

// REPL is the interactive prompt: user input, permission answers, and
// selection lists, all backed by one readline instance.
type REPL struct {
	rl       *readline.Instance
	renderer *Renderer
	theme    *Theme
	out      io.Writer
}

func NewREPL(renderer *Renderer, theme *Theme) (*REPL, error) {
	rl, err := readline.NewEx(&readline.Config{
		Prompt:          "> ",
		HistoryFile:     filepath.Join(config.StateDir(), "history"),
		HistoryLimit:    1000,
		InterruptPrompt: "^C",
		EOFPrompt:       "exit",
		FuncFilterInputRune: func(r rune) (rune, bool) {
			if r == '\n' {
				return newlinePlaceholder, true
			}
			return r, true
		},
	})
	if err != nil {
		return nil, err
	}
	return &REPL{rl: rl, renderer: renderer, theme: theme, out: renderer.Out}, nil
}

// ErrInterrupt reports Ctrl-C at the prompt.
var ErrInterrupt = readline.ErrInterrupt

// Readline reads one input line; ErrInterrupt on Ctrl-C, io.EOF on Ctrl-D.
func (r *REPL) Readline() (string, error) {
	line, err := r.rl.Readline()
	line = strings.ReplaceAll(line, string(newlinePlaceholder), "\n")
	return line, err
}

// ReadSecret reads a hidden value (api key entry).
func (r *REPL) ReadSecret(prompt string) (string, error) {
	raw, err := r.rl.ReadPassword(prompt)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(raw)), nil
}

func (r *REPL) Close() { _ = r.rl.Close() }

// Prompt implements permissions.Prompter for gated tool calls.
func (r *REPL) Prompt(req permissions.Request) (permissions.Decision, error) {
	r.renderer.PermissionBlock(req.Tool, req.Preview)
	for {
		answer, err := r.Readline()
		if err != nil {
			if errors.Is(err, readline.ErrInterrupt) {
				return permissions.Deny, permissions.ErrAborted
			}
			return permissions.Deny, err
		}
		switch strings.ToLower(strings.TrimSpace(answer)) {
		case "", "y", "yes":
			return permissions.AllowOnce, nil
		case "a", "always":
			return permissions.AllowAlways, nil
		case "n", "no":
			return permissions.Deny, nil
		case "x", "never":
			return permissions.DenyAlways, nil
		default:
			r.renderer.Infof("%s", r.theme.Dim("please answer y, a, n, or x"))
		}
	}
}

// Pick shows a numbered list and returns the chosen index. An empty answer
// picks the first option; a codename/number is accepted.
func (r *REPL) Pick(title string, options []string) (int, error) {
	r.renderer.Infof("%s", title)
	for i, opt := range options {
		r.renderer.Infof("  %s %s", r.theme.Accent(fmt.Sprintf("%d.", i+1)), opt)
	}
	for {
		answer, err := r.Readline()
		if err != nil {
			return 0, err
		}
		answer = strings.TrimSpace(answer)
		if answer == "" {
			return 0, nil
		}
		if n, err := strconv.Atoi(answer); err == nil && n >= 1 && n <= len(options) {
			return n - 1, nil
		}
		for i, opt := range options {
			if strings.EqualFold(answer, opt) {
				return i, nil
			}
		}
		r.renderer.Infof("%s", r.theme.Dim("pick a number from the list"))
	}
}

// Confirm asks a yes/no question (used by configure-style flows).
func (r *REPL) Confirm(question string) (bool, error) {
	r.renderer.Infof("%s %s", question, r.theme.Dim("[Y/n]"))
	for {
		answer, err := r.Readline()
		if err != nil {
			return false, err
		}
		switch strings.ToLower(strings.TrimSpace(answer)) {
		case "", "y", "yes":
			return true, nil
		case "n", "no":
			return false, nil
		}
	}
}
