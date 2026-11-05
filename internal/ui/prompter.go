package ui

import (
	"strings"

	"github.com/ergochat/readline"
)

// ErrInterrupt reports Ctrl-C at a prompt.
var ErrInterrupt = readline.ErrInterrupt

// Prompter asks questions on a terminal: free text, hidden secrets, numbered
// lists and yes/no. It keeps no history, so nothing typed at a setup prompt is
// ever written to disk.
type Prompter struct {
	rl       *readline.Instance
	renderer *Renderer
	theme    *Theme
}

// NewPrompter opens a readline instance on stdin/stdout.
func NewPrompter(renderer *Renderer, theme *Theme) (*Prompter, error) {
	rl, err := readline.NewEx(&readline.Config{
		Prompt:                 theme.Accent(GlyphPrompt) + " ",
		DisableAutoSaveHistory: true,
		InterruptPrompt:        "^C",
		EOFPrompt:              "",
	})
	if err != nil {
		return nil, err
	}
	return &Prompter{rl: rl, renderer: renderer, theme: theme}, nil
}

// Readline reads one line; ErrInterrupt on Ctrl-C, io.EOF on Ctrl-D.
func (p *Prompter) Readline() (string, error) { return p.rl.Readline() }

// ReadSecret reads a value without echoing it (the api key).
func (p *Prompter) ReadSecret(prompt string) (string, error) {
	raw, err := p.rl.ReadPassword(prompt)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(raw)), nil
}

// Close releases the terminal.
func (p *Prompter) Close() { _ = p.rl.Close() }
