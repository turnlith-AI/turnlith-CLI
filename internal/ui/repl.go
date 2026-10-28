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

	// candidates supplies argument completions for slash commands (agent
	// codenames, model ids, effort levels, @file mentions); nil completes
	// command names only.
	candidates func(before, prefix string) []string
	// onTabEmpty runs when Tab is pressed on an empty line (readline has no
	// candidates then, so Tab is free); the app cycles the permission mode.
	onTabEmpty func()
}

// SetCandidates wires the argument completions the ui package cannot know.
func (r *REPL) SetCandidates(fn func(before, prefix string) []string) { r.candidates = fn }

// SetOnTabEmpty installs the empty-line Tab action.
func (r *REPL) SetOnTabEmpty(fn func()) { r.onTabEmpty = fn }

// SetPermissionMode restyles the input prompt to reflect the mode.
func (r *REPL) SetPermissionMode(mode permissions.Mode) {
	r.rl.SetPrompt(PromptForMode(r.theme, mode))
}

// WriteAbove prints text above the input line; it goes through readline's
// prompt-aware writer so the input line is redrawn cleanly below.
func (r *REPL) WriteAbove(s string) { _, _ = r.rl.Write([]byte(s)) }

// Refresh redraws the prompt and input line.
func (r *REPL) Refresh() { r.rl.Refresh() }

// Prefill places text in the input buffer of the next Readline (the file
// browser uses it to insert an @mention).
func (r *REPL) Prefill(text string) { r.rl.SetDefault(text) }

func NewREPL(renderer *Renderer, theme *Theme) (*REPL, error) {
	r := &REPL{renderer: renderer, theme: theme, out: renderer.Out}
	// The listener needs the instance it is installed on; it is only invoked
	// while reading a line, after NewEx returned, so the late assignment is safe.
	var instance *readline.Instance
	rl, err := readline.NewEx(&readline.Config{
		Prompt:          PromptForMode(theme, permissions.ModeAsk),
		HistoryFile:     filepath.Join(config.StateDir(), "history"),
		HistoryLimit:    1000,
		InterruptPrompt: "^C",
		EOFPrompt:       "exit",
		AutoComplete: completer{extra: func(before, prefix string) []string {
			if r.candidates == nil {
				return nil
			}
			return r.candidates(before, prefix)
		}},
		Listener: func(line []rune, pos int, key rune) ([]rune, int, bool) {
			// "/" opening a fresh command line: suggest the commands above the
			// prompt. Write goes through readline's prompt-aware writer, so the
			// input line is redrawn cleanly below the menu.
			if key == '/' && len(line) == 1 && instance != nil {
				_, _ = instance.Write([]byte(SuggestText(r.theme)))
			}
			// Tab on an empty line has no completion candidates, so it is free
			// for the app's mode cycler (Claude Code-style shift+tab).
			if key == '\t' && len(line) == 0 && r.onTabEmpty != nil {
				r.onTabEmpty()
			}
			return nil, 0, false
		},
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
	r.rl = rl
	instance = rl
	return r, nil
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

// Prompt implements permissions.Prompter for gated tool calls. One keypress
// answers — no Enter: y allow · a always · p always in this project · n deny ·
// x never; Enter allows, Ctrl-C aborts. a/p/x are only live when there is an
// honest rule to remember. Platforms without a raw-mode stdin fall back to
// typing.
func (r *REPL) Prompt(req permissions.Request) (permissions.Decision, error) {
	view := PermissionView{Tool: req.Tool, Summary: req.Summary, Preview: req.Preview, Note: req.Reason}
	if rule := permissions.SuggestRule(req); rule != nil {
		view.Remember, view.Project, view.Rule = true, true, rule.String()
		if rule.Pattern == "" {
			view.Rule = rule.Tool + " (any " + strings.ToLower(rule.Tool) + ")"
		}
	} else if req.External {
		view.Remember = true
	}
	r.renderer.PermissionBlock(view)
	for {
		if key, ok := readSingleKey(); ok {
			switch key {
			case "y", "enter":
				r.echoAnswer("y allow")
				return permissions.AllowOnce, nil
			case "a":
				if view.Remember {
					r.echoAnswer("a always (this session)")
					return permissions.AllowAlways, nil
				}
			case "p":
				if view.Project {
					r.echoAnswer("p always in this project")
					return permissions.AllowProject, nil
				}
			case "n", "esc":
				r.echoAnswer("n deny")
				return permissions.Deny, nil
			case "x":
				if view.Remember {
					r.echoAnswer("x never (this session)")
					return permissions.DenyAlways, nil
				}
			case "ctrlC":
				r.echoAnswer("^C")
				return permissions.Deny, permissions.ErrAborted
			}
			continue
		}
		// fallback: a typed line
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
			if view.Remember {
				return permissions.AllowAlways, nil
			}
		case "p", "project":
			if view.Project {
				return permissions.AllowProject, nil
			}
		case "n", "no":
			return permissions.Deny, nil
		case "x", "never":
			if view.Remember {
				return permissions.DenyAlways, nil
			}
		}
		r.renderer.Infof("%s", r.theme.Dim("please answer with one of the letters shown"))
	}
}

// echoAnswer shows the chosen answer under the permission block (raw mode
// has no echo of its own).
func (r *REPL) echoAnswer(answer string) {
	fmt.Fprintln(r.out, "  "+r.theme.Accent(answer))
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
