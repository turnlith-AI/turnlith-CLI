package cli

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/turnlith-AI/turnlith-CLI/internal/agent"
	"github.com/turnlith-AI/turnlith-CLI/internal/api"
	"github.com/turnlith-AI/turnlith-CLI/internal/config"
	"github.com/turnlith-AI/turnlith-CLI/internal/history"
	"github.com/turnlith-AI/turnlith-CLI/internal/permissions"
	"github.com/turnlith-AI/turnlith-CLI/internal/tools"
	"github.com/turnlith-AI/turnlith-CLI/internal/ui"
)

// app carries the wired-up session state.
type app struct {
	opts options

	client   *api.Client
	agents   []api.Agent
	agent    api.Agent
	runner   *agent.Runner
	policy   *permissions.Policy
	registry *tools.Registry
	tracker  *tools.Tracker

	theme       *ui.Theme
	renderer    *ui.Renderer
	errRenderer *ui.Renderer
	repl        *ui.REPL

	// /v1/models cache (agents, catalog, provider capabilities) backing the
	// model/effort pickers and TAB completion; dropped after mutations.
	modelsMu sync.Mutex
	models   *api.ModelsResponse

	cwd        string
	root       string // project root: the git root above cwd, else cwd
	transcript *history.Session
	verbose    bool

	// notes are one-time startup messages (rules loaded or refused), shown
	// under the banner or on stderr in print mode.
	notes []string
}

// usageError marks a bad invocation (exit status 2) as opposed to a runtime
// failure (1).
type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }

// projectRuleStore saves "always in this project" answers to the user's own
// config, keyed by project root.
type projectRuleStore struct{ root string }

func (s projectRuleStore) SaveAllow(r permissions.Rule) error {
	return config.AddProjectAllow(s.root, r.String())
}

// setupPermissions applies the mode flags and loads permission rules from
// every source: --allowed-tools/--disallowed-tools, the user's saved
// per-project allows, and the repository's shared deny list. A repository can
// forbid things but never grant itself permissions, so allow rules found in
// .tlc/settings.json are refused (and counted in a note).
func (a *app) setupPermissions() error {
	mode := permissions.ModeAsk
	switch {
	case a.opts.permissionMode != "":
		m, ok := permissions.ParseMode(a.opts.permissionMode)
		if !ok {
			return &usageError{fmt.Sprintf("--permission-mode must be normal, auto, or yolo, got %q", a.opts.permissionMode)}
		}
		mode = m
	case a.opts.yolo:
		mode = permissions.ModeYolo
	}
	a.policy.Mode = mode
	a.policy.Store = projectRuleStore{root: a.root}

	for _, s := range a.opts.allowedTools {
		r, err := permissions.ParseRule(s)
		if err != nil {
			return &usageError{err.Error()}
		}
		a.policy.AddAllow(r)
	}
	for _, s := range a.opts.disallowedTools {
		r, err := permissions.ParseRule(s)
		if err != nil {
			return &usageError{err.Error()}
		}
		a.policy.AddDeny(r)
	}

	for _, s := range config.LoadPrefs().ProjectAllow(a.root) {
		r, err := permissions.ParseRule(s)
		if err != nil {
			a.notes = append(a.notes, "ignoring an invalid saved permission rule: "+err.Error())
			continue
		}
		a.policy.AddAllow(r)
	}

	deny, ignored, err := config.LoadProjectDeny(a.root)
	if err != nil {
		a.notes = append(a.notes, fmt.Sprintf("could not read %s: %v", config.ProjectSettingsPath(a.root), err))
	}
	for _, s := range deny {
		r, err := permissions.ParseRule(s)
		if err != nil {
			a.notes = append(a.notes, "ignoring an invalid deny rule in .tlc/settings.json: "+err.Error())
			continue
		}
		a.policy.AddDeny(r)
	}
	if ignored > 0 {
		a.notes = append(a.notes, fmt.Sprintf("ignored %d allow rule(s) in .tlc/settings.json — a repository cannot grant itself permissions (answer 'p' at a prompt to allow something here yourself)", ignored))
	}
	return nil
}

func newApp(opts options) (*app, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	conn, err := config.LoadConnection()
	if err != nil {
		return nil, err
	}
	if opts.baseURL != "" {
		conn.BaseURL = config.NormalizeBaseURL(opts.baseURL)
	}
	if opts.apiKey != "" {
		conn.APIKey = strings.TrimSpace(opts.apiKey)
		warnAPIKeyFlag()
	}
	if err := config.ValidateBaseURL(conn.BaseURL); err != nil {
		return nil, err
	}

	theme := ui.NewTheme()
	renderer := ui.NewRenderer(os.Stdout, theme)
	errRenderer := ui.NewRenderer(os.Stderr, &ui.Theme{Colors: ui.IsTerminal(os.Stderr)})

	client := api.New(conn.BaseURL, conn.APIKey)
	if clientRetryDelays != nil {
		client.SetRetryDelays(clientRetryDelays...)
	}
	a := &app{
		opts:        opts,
		client:      client,
		theme:       theme,
		renderer:    renderer,
		errRenderer: errRenderer,
		cwd:         cwd,
		verbose:     opts.verbose,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	agents, err := a.client.ListAgents(ctx)
	if err != nil {
		return nil, err
	}
	if len(agents) == 0 && opts.command != "agents-create" {
		return nil, fmt.Errorf("this workspace has no agents yet — run `tlc agents create` to make one, or use the console at %s (Agents → New agent)", a.client.BaseURL)
	}
	a.agents = agents

	tracker := tools.NewTracker()
	a.tracker = tracker
	a.registry = tools.NewRegistry(
		&tools.ReadTool{Root: cwd, Tracker: tracker},
		&tools.WriteTool{Root: cwd, Tracker: tracker},
		&tools.EditTool{Root: cwd, Tracker: tracker},
		&tools.BashTool{Root: cwd},
		&tools.GlobTool{Root: cwd},
		&tools.GrepTool{Root: cwd},
		&tools.SearchMemoryTool{Client: a.client, Agent: func() string { return a.agent.Codename }},
	)
	a.policy = &permissions.Policy{}
	a.root = config.FindProjectRoot(cwd)
	a.runner = &agent.Runner{
		Client:   a.client,
		Tools:    a.registry,
		Policy:   a.policy,
		Root:     cwd,
		MaxSteps: opts.maxTurns,
	}
	if err := a.setupPermissions(); err != nil {
		return nil, err
	}
	return a, nil
}

func (a *app) close() {
	if a.repl != nil {
		a.repl.Close()
	}
	a.transcript.Close()
}

// clientRetryDelays overrides the API client's retry schedule; tests shrink it
// so a retry does not sleep for real. nil keeps the defaults.
var clientRetryDelays []time.Duration

func (a *app) transcriptPath() string {
	if a.transcript == nil {
		return "(none)"
	}
	return a.transcript.Path
}

func label(ag api.Agent) string {
	if ag.Label == nil || *ag.Label == "" {
		return "-"
	}
	return *ag.Label
}

func shortPath(p string) string {
	home, err := os.UserHomeDir()
	if err == nil && strings.HasPrefix(p, home) {
		return "~" + strings.TrimPrefix(p, home)
	}
	return p
}
