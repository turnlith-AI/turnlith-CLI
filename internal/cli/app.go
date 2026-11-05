package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/turnlith-AI/turnlith-CLI/internal/api"
	"github.com/turnlith-AI/turnlith-CLI/internal/config"
	"github.com/turnlith-AI/turnlith-CLI/internal/ui"
)

// console is where a command writes: results to stdout, errors and progress
// notes to stderr, so a pipeline sees only data.
type console struct {
	out, err        *ui.Renderer
	theme, errTheme *ui.Theme
}

func newConsole() console {
	theme, errTheme := ui.NewTheme(), ui.NewThemeFor(os.Stderr)
	return console{
		out:      ui.NewRenderer(os.Stdout, theme),
		err:      ui.NewRenderer(os.Stderr, errTheme),
		theme:    theme,
		errTheme: errTheme,
	}
}

// app carries the state of a command that talks to the server as the saved
// connection: `tlc agents` and `tlc agents create`.
type app struct {
	opts   options
	client *api.Client
	agents []api.Agent
	con    console

	// the prompter is opened on first use and closed with the app
	prompter *ui.Prompter
}

// clientRetryDelays overrides the API client's retry schedule; tests shrink it
// so a retry does not sleep for real. nil keeps the defaults.
var clientRetryDelays []time.Duration

// newClient builds the API client for a connection and makes retries
// visible: a 429 that takes seconds to clear should not look like a hang.
func newClient(conn config.Connection, con console) *api.Client {
	client := api.New(conn.BaseURL, conn.APIKey)
	if clientRetryDelays != nil {
		client.SetRetryDelays(clientRetryDelays...)
	}
	client.OnRetry = func(attempt int, wait time.Duration, err error) {
		con.err.Notice(fmt.Sprintf("%s — retrying in %s (attempt %d)", retryReason(err), wait.Round(100*time.Millisecond), attempt))
	}
	return client
}

// resolveConnection loads the saved connection, applies --base-url/--api-key
// and refuses an unsafe server URL.
func resolveConnection(opts options) (config.Connection, error) {
	conn, err := config.LoadConnection()
	if err != nil {
		return conn, err
	}
	if opts.baseURL != "" {
		conn.BaseURL = config.NormalizeBaseURL(opts.baseURL)
	}
	if opts.apiKey != "" {
		conn.APIKey = strings.TrimSpace(opts.apiKey)
		warnAPIKeyFlag()
	}
	if err := config.ValidateBaseURL(conn.BaseURL); err != nil {
		return conn, err
	}
	return conn, nil
}

func newApp(opts options) (*app, error) {
	conn, err := resolveConnection(opts)
	if err != nil {
		return nil, err
	}
	con := newConsole()
	a := &app{opts: opts, client: newClient(conn, con), con: con}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	agents, err := a.client.ListAgents(ctx)
	if err != nil {
		return nil, err
	}
	if len(agents) == 0 && opts.command != "agents-create" {
		return nil, fmt.Errorf("this workspace has no agents yet — run `tlc agents create` to make one, or use the console at %s (Agents → New agent)", a.client.BaseURL)
	}
	a.agents = agents
	return a, nil
}

func (a *app) close() {
	if a.prompter != nil {
		a.prompter.Close()
	}
}

// errNoTerminal reports a prompt that needed a person at a terminal.
var errNoTerminal = errors.New("this needs a terminal")

// prompt returns the app's prompter, opening it on first use. It fails with
// errNoTerminal unless stdin and stdout are both terminals, so scripts never
// block waiting for an answer.
func (a *app) prompt() (*ui.Prompter, error) {
	if a.prompter != nil {
		return a.prompter, nil
	}
	if !ui.IsTerminal(os.Stdin) || !ui.IsTerminal(os.Stdout) {
		return nil, errNoTerminal
	}
	p, err := ui.NewPrompter(a.con.out, a.con.theme)
	if err != nil {
		return nil, err
	}
	a.prompter = p
	return p, nil
}

// label renders an agent's label for tables, safely.
func label(ag api.Agent) string {
	if ag.Label == nil || strings.TrimSpace(*ag.Label) == "" {
		return "-"
	}
	return ui.SanitizeInline(*ag.Label)
}

func shortPath(p string) string {
	home, err := os.UserHomeDir()
	if err == nil && home != "" && strings.HasPrefix(p, home) {
		return "~" + strings.TrimPrefix(p, home)
	}
	return p
}

// maskKey shows enough of an api key to recognise it: the prefix and last four
// characters of a long key, only a few leading characters of a short one.
func maskKey(key string) string {
	switch {
	case len(key) > 16:
		return key[:6] + "…" + key[len(key)-4:]
	case len(key) > 4:
		return key[:3] + "…"
	default:
		return "…"
	}
}

// warnAPIKeyFlag explains why --api-key is the least safe way to pass a key.
func warnAPIKeyFlag() {
	fmt.Fprintln(os.Stderr, "tlc: warning: --api-key is visible to other users in the process list; prefer TLITH_API_KEY, `tlc configure`, or --api-key-stdin")
}
