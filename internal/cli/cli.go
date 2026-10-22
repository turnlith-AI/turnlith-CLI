// Package cli wires everything together: argument handling, the interactive
// REPL, the print mode, and the auxiliary commands.
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
	"sync"
	"time"

	"github.com/turnlith-AI/turnlith-CLI/internal/agent"
	"github.com/turnlith-AI/turnlith-CLI/internal/api"
	"github.com/turnlith-AI/turnlith-CLI/internal/config"
	"github.com/turnlith-AI/turnlith-CLI/internal/history"
	"github.com/turnlith-AI/turnlith-CLI/internal/permissions"
	"github.com/turnlith-AI/turnlith-CLI/internal/tools"
	"github.com/turnlith-AI/turnlith-CLI/internal/ui"
	"github.com/turnlith-AI/turnlith-CLI/internal/version"
)

const helpText = `tlc ` + version.Version + ` — turnlith coding agent for the terminal

Usage:
  tlc                        start an interactive session
  tlc "fix the failing test" start with an initial prompt
  tlc -p "explain main.go"   one-shot: print the answer and exit
  tlc -c                     continue the previous conversation here
  tlc --agent <codename>     pick the turnlith agent (model) to talk to
  tlc --yolo                 run every gated tool without asking

Commands:
  tlc agents                 list the workspace's agents
  tlc agents create          create an agent (interactive, needs an admin key)
  tlc configure              save the server URL and api key
  tlc install [opencode]     wire the server's agents into opencode (provider + MCP)
  tlc uninstall [opencode]   remove the turnlith blocks from the opencode config
  tlc help | version

Options:
  -p, --print     non-interactive; assistant text to stdout, tool activity to stderr
  -c, --continue  replay the previous local transcript for this directory
      --agent     agent codename (defaults to the saved/last-used choice)
      --yolo      start in yolo permission mode (no prompts for gated tools)
      --verbose   show token usage after each turn
      --base-url  override the server URL for this run
      --api-key   override the api key for this run

Options for ` + "`tlc agents create`" + `:
      --name      codename clients call (default: generated)
      --label     human-readable label
      --model     model spec, e.g. gemini:gemini-3.8-flash
      --effort    reasoning effort: none|minimal|low|medium|high|xhigh

Options for ` + "`tlc install opencode`" + `:
      --project       target ./opencode.json in the current directory
      --provider-id   provider key in the config (default: tlith)
      --no-mcp        skip the turnlith-controller MCP server block
      --use-env       write {env:TLITH_API_KEY} instead of embedding the key

Environment: TLITH_BASE_URL, TLITH_API_KEY (connection), TLC_AGENT (default agent)

In the REPL: /help /clear /files /agent /agents /model /effort /status /permissions /verbose /yolo /exit
@file mentions attach files to a prompt (TAB completes paths); Tab at an empty prompt cycles the permission mode
`

type options struct {
	command string // "", "configure", "agents", "agents-create", "install", "uninstall", "help", "version"
	print   bool
	cont    bool
	agent   string
	yolo    bool
	verbose bool
	baseURL string
	apiKey  string
	prompt  string

	// `tlc agents create`
	name   string
	label  string
	model  string
	effort string

	// `tlc install` / `tlc uninstall`
	harness    string
	project    bool
	providerID string
	noMcp      bool
	useEnv     bool
}

// Main is the entry point; it returns the process exit code.
func Main(argv []string) int {
	opts, err := parseArgs(argv)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tlc: %v\nrun `tlc help` for usage\n", err)
		return 2
	}
	switch opts.command {
	case "help":
		fmt.Print(helpText)
		return 0
	case "version":
		fmt.Println(version.Version)
		return 0
	case "configure":
		return runConfigure(opts)
	case "install":
		return runInstall(opts)
	case "uninstall":
		return runUninstall(opts)
	}

	app, err := newApp(opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tlc: %s\n", friendlyErr(err))
		return 1
	}
	defer app.close()

	switch opts.command {
	case "agents":
		return app.cmdAgents()
	case "agents-create":
		return app.cmdAgentsCreate()
	}
	if opts.print {
		return app.runPrint()
	}
	return app.runInteractive()
}

func parseArgs(argv []string) (options, error) {
	opts := options{}
	var promptWords []string
	next := func(i *int, flag string) (string, error) {
		*i++
		if *i >= len(argv) {
			return "", fmt.Errorf("%s needs a value", flag)
		}
		return argv[*i], nil
	}
	seenPositional := false
	seenSub := false
	for i := 0; i < len(argv); i++ {
		arg := argv[i]
		switch {
		case arg == "-h" || arg == "--help":
			opts.command = "help"
		case arg == "-v" || arg == "--version":
			opts.command = "version"
		case arg == "-p" || arg == "--print":
			opts.print = true
		case arg == "-c" || arg == "--continue":
			opts.cont = true
		case arg == "--yolo":
			opts.yolo = true
		case arg == "--verbose":
			opts.verbose = true
		case arg == "--agent":
			v, err := next(&i, arg)
			if err != nil {
				return opts, err
			}
			opts.agent = v
		case strings.HasPrefix(arg, "--agent="):
			opts.agent = strings.TrimPrefix(arg, "--agent=")
		case arg == "--name":
			v, err := next(&i, arg)
			if err != nil {
				return opts, err
			}
			opts.name = v
		case strings.HasPrefix(arg, "--name="):
			opts.name = strings.TrimPrefix(arg, "--name=")
		case arg == "--label":
			v, err := next(&i, arg)
			if err != nil {
				return opts, err
			}
			opts.label = v
		case strings.HasPrefix(arg, "--label="):
			opts.label = strings.TrimPrefix(arg, "--label=")
		case arg == "--model":
			v, err := next(&i, arg)
			if err != nil {
				return opts, err
			}
			opts.model = v
		case strings.HasPrefix(arg, "--model="):
			opts.model = strings.TrimPrefix(arg, "--model=")
		case arg == "--effort":
			v, err := next(&i, arg)
			if err != nil {
				return opts, err
			}
			opts.effort = v
		case strings.HasPrefix(arg, "--effort="):
			opts.effort = strings.TrimPrefix(arg, "--effort=")
		case arg == "--project":
			opts.project = true
		case arg == "--provider-id":
			v, err := next(&i, arg)
			if err != nil {
				return opts, err
			}
			opts.providerID = v
		case strings.HasPrefix(arg, "--provider-id="):
			opts.providerID = strings.TrimPrefix(arg, "--provider-id=")
		case arg == "--no-mcp":
			opts.noMcp = true
		case arg == "--use-env":
			opts.useEnv = true
		case arg == "--base-url":
			v, err := next(&i, arg)
			if err != nil {
				return opts, err
			}
			opts.baseURL = v
		case strings.HasPrefix(arg, "--base-url="):
			opts.baseURL = strings.TrimPrefix(arg, "--base-url=")
		case arg == "--api-key":
			v, err := next(&i, arg)
			if err != nil {
				return opts, err
			}
			opts.apiKey = v
		case strings.HasPrefix(arg, "--api-key="):
			opts.apiKey = strings.TrimPrefix(arg, "--api-key=")
		case arg == "--":
			promptWords = append(promptWords, argv[i+1:]...)
			i = len(argv)
		case strings.HasPrefix(arg, "-") && arg != "-":
			return opts, fmt.Errorf("unknown flag %q", arg)
		default:
			if !seenPositional && (arg == "configure" || arg == "agents" || arg == "help" || arg == "version" ||
				arg == "install" || arg == "uninstall") {
				opts.command = arg
				seenPositional = true
				continue
			}
			// one subcommand right after `agents`
			if opts.command == "agents" && !seenSub && len(promptWords) == 0 {
				seenSub = true
				switch arg {
				case "list":
					continue
				case "create", "new":
					opts.command = "agents-create"
					continue
				default:
					return opts, fmt.Errorf("unknown `agents` subcommand %q — use `tlc agents` or `tlc agents create`", arg)
				}
			}
			// optional harness name right after install/uninstall
			if (opts.command == "install" || opts.command == "uninstall") && opts.harness == "" && len(promptWords) == 0 {
				if arg == "opencode" {
					opts.harness = arg
					continue
				}
				return opts, fmt.Errorf("unknown harness %q — supported: opencode", arg)
			}
			seenPositional = true
			promptWords = append(promptWords, arg)
		}
	}
	opts.prompt = strings.TrimSpace(strings.Join(promptWords, " "))
	if opts.command == "agents-create" && opts.prompt != "" {
		return opts, fmt.Errorf("`tlc agents create` takes flags, not a prompt — unexpected %q", opts.prompt)
	}
	if (opts.command == "install" || opts.command == "uninstall") && opts.prompt != "" {
		return opts, fmt.Errorf("`tlc %s` takes flags and an optional harness name, not a prompt — unexpected %q", opts.command, opts.prompt)
	}
	return opts, nil
}

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
	transcript *history.Session
	verbose    bool
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
	}

	theme := ui.NewTheme()
	renderer := ui.NewRenderer(os.Stdout, theme)
	errRenderer := ui.NewRenderer(os.Stderr, &ui.Theme{Colors: ui.IsTerminal(os.Stderr)})

	a := &app{
		opts:        opts,
		client:      api.New(conn.BaseURL, conn.APIKey),
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
	mode := permissions.ModeAsk
	if opts.yolo {
		mode = permissions.ModeYolo
	}
	a.policy = &permissions.Policy{Mode: mode}
	a.runner = &agent.Runner{
		Client: a.client,
		Tools:  a.registry,
		Policy: a.policy,
	}
	return a, nil
}

func (a *app) close() {
	if a.repl != nil {
		a.repl.Close()
	}
	a.transcript.Close()
}

// ---------------------------------------------------------------- commands

func (a *app) cmdAgents() int {
	nameWidth := len("CODENAME")
	labelWidth := len("LABEL")
	for _, ag := range a.agents {
		nameWidth = max(nameWidth, len(ag.Codename))
		labelWidth = max(labelWidth, len(label(ag)))
	}
	a.renderer.Infof("%-*s  %-*s  %s", nameWidth, "CODENAME", labelWidth, "LABEL", "MODEL")
	for _, ag := range a.agents {
		a.renderer.Infof("%-*s  %-*s  %s", nameWidth, ag.Codename, labelWidth, label(ag), modelNote(ag))
	}
	return 0
}

func runConfigure(opts options) int {
	theme := ui.NewTheme()
	renderer := ui.NewRenderer(os.Stdout, theme)
	repl, err := ui.NewREPL(renderer, theme)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tlc: %v\n", err)
		return 1
	}
	defer repl.Close()

	baseURL := config.NormalizeBaseURL(opts.baseURL)
	if baseURL == "" {
		if existing, err := config.LoadConnection(); err == nil && existing.BaseURL != "" {
			baseURL = existing.BaseURL
		} else {
			baseURL = "http://localhost:8080"
		}
		renderer.Infof("turnlith server URL [%s]:", baseURL)
		answer, err := repl.Readline()
		if err != nil {
			return 1
		}
		if strings.TrimSpace(answer) != "" {
			baseURL = config.NormalizeBaseURL(answer)
		}
	}
	apiKey := strings.TrimSpace(opts.apiKey)
	if apiKey == "" {
		apiKey, err = repl.ReadSecret("api key (sk-…): ")
		if err != nil {
			return 1
		}
	}
	if baseURL == "" || apiKey == "" {
		renderer.Errorf("both a server URL and an api key are required")
		return 2
	}

	client := api.New(baseURL, apiKey)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := client.Health(ctx); err != nil {
		renderer.Errorf("health check failed: %s", friendlyErr(err))
		return 1
	}
	agents, err := client.ListAgents(ctx)
	if err != nil {
		renderer.Errorf("key check failed: %s", friendlyErr(err))
		return 1
	}
	path, err := config.SaveConnection(config.Connection{BaseURL: baseURL, APIKey: apiKey})
	if err != nil {
		renderer.Errorf("could not save the connection: %v", err)
		return 1
	}
	renderer.Infof("%s connected to %s — %d agent(s) available", theme.Green("✓"), baseURL, len(agents))
	renderer.Infof("%s", theme.Dim("saved "+path))
	if len(agents) > 0 {
		renderer.Infof("%s", theme.Dim("run `tlc` to start, or `tlc agents` to list agents"))
	} else {
		renderer.Infof("%s", theme.Yellow("no agents exist in this workspace yet — run `tlc agents create` to make one"))
	}
	return 0
}

// ---------------------------------------------------------------- interactive

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
		mode := permissions.NextMode(a.policy.Mode)
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
		line, err := a.repl.Readline()
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
	a.renderer.Infof("%s", a.theme.Faint("type /help for commands · Tab cycles the permission mode · /exit to quit"+modeHint))
}

// permissionModeLine is the status line shown when the mode changes.
func permissionModeLine(t *ui.Theme, mode permissions.Mode) string {
	switch mode {
	case permissions.ModeAuto:
		return t.Yellow("⏵ auto — file edits run without asking, Bash still asks")
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
		session, entries, err := history.Continue(a.cwd)
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
	wire := a.attachMentions(prompt, a.renderer)
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
	a.runPrompt(ctx, prompt, wire, a.renderer, a.renderer)
}

func (a *app) runPrompt(ctx context.Context, display, wire string, out, activity *ui.Renderer) {
	a.transcript.Append("user", display)

	var turnText strings.Builder
	spinner := out.StartSpinner("thinking…")
	stopSpinner := func() {
		if spinner != nil {
			spinner.Stop()
			spinner = nil
		}
	}
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
		OnToolEnd: func(name, result string, denied bool, elapsed time.Duration) {
			activity.ToolResult(result, denied, elapsed)
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
	case errors.Is(err, context.Canceled), errors.Is(err, permissions.ErrAborted):
		activity.Notice("interrupted")
	default:
		activity.Errorf("%s", friendlyErr(err))
	}
}

// handleSlash processes a REPL command; it returns true to exit.
func (a *app) handleSlash(line string) bool {
	fields := strings.Fields(line)
	switch strings.ToLower(fields[0]) {
	case "/exit", "/quit":
		return true
	case "/help":
		a.renderer.Infof("%s", ui.HelpText(a.theme))
	case "/clear":
		a.runner.Reset()
		a.renderer.Notice("local task history cleared — the agent's server-side memory is kept")
	case "/agent":
		if len(fields) > 1 {
			if strings.EqualFold(fields[1], "new") || strings.EqualFold(fields[1], "create") {
				if err := a.createAgentInSession(); err != nil {
					a.renderer.Errorf("%s", friendlyErr(err))
				}
				return false
			}
			if err := a.setAgentByName(fields[1], true); err != nil {
				a.renderer.Errorf("%s", friendlyErr(err))
			}
			return false
		}
		if err := a.pickAgent(); err != nil {
			a.renderer.Errorf("%s", friendlyErr(err))
		}
	case "/model":
		if err := a.handleModel(fields); err != nil {
			if errors.Is(err, errCancelled) {
				a.renderer.Notice("model unchanged")
				return false
			}
			a.renderer.Errorf("%s", friendlyErr(err))
		}
	case "/effort":
		if err := a.handleEffort(fields); err != nil {
			if errors.Is(err, errCancelled) {
				a.renderer.Notice("effort unchanged")
				return false
			}
			a.renderer.Errorf("%s", friendlyErr(err))
		}
	case "/agents":
		for _, ag := range a.agents {
			marker := "  "
			if ag.Codename == a.agent.Codename {
				marker = a.theme.Green("▸ ")
			}
			a.renderer.Infof("%s%-14s %s", marker, ag.Codename, a.theme.Dim(modelNote(ag)))
		}
	case "/status":
		key := a.client.APIKey
		if len(key) > 12 {
			key = key[:8] + "…" + key[len(key)-4:]
		}
		a.renderer.Infof("agent:      %s (%s)", a.agent.Codename, modelNote(a.agent))
		a.renderer.Infof("server:     %s", a.client.BaseURL)
		a.renderer.Infof("api key:    %s", key)
		a.renderer.Infof("directory:  %s", a.cwd)
		a.renderer.Infof("permissions: %s", a.policy.Mode.Description())
		a.renderer.Infof("transcript: %s", a.transcriptPath())
	case "/verbose":
		a.verbose = !a.verbose
		a.renderer.Notice(fmt.Sprintf("verbose %v", map[bool]string{true: "on", false: "off"}[a.verbose]))
	case "/files":
		a.openFileBrowser()
	case "/permissions":
		switch {
		case len(fields) > 2:
			a.renderer.Errorf("usage: /permissions [normal|auto|yolo]")
		case len(fields) == 1:
			a.setPermissionMode(permissions.NextMode(a.policy.Mode))
		default:
			mode, ok := permissions.ParseMode(fields[1])
			if !ok {
				a.renderer.Errorf("unknown permission mode %q — use normal, auto, or yolo", fields[1])
				return false
			}
			a.setPermissionMode(mode)
		}
	case "/yolo":
		if a.policy.Mode == permissions.ModeYolo {
			a.setPermissionMode(permissions.ModeAsk)
		} else {
			a.setPermissionMode(permissions.ModeYolo)
		}
	default:
		a.renderer.Infof("%s", a.theme.Dim("unknown command "+fields[0]+" — try /help"))
	}
	return false
}

// ---------------------------------------------------------------- print mode

func (a *app) runPrint() int {
	prompt := a.opts.prompt
	if prompt == "" {
		if info, err := os.Stdin.Stat(); err == nil && info.Mode()&os.ModeCharDevice == 0 {
			data, err := io.ReadAll(io.LimitReader(os.Stdin, 4*1024*1024))
			if err == nil {
				prompt = strings.TrimSpace(string(data))
			}
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
	a.runPrompt(ctx, prompt, wire, a.renderer, a.errRenderer)
	return 0
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

// ---------------------------------------------------------------- agents

func (a *app) chooseAgent(interactive bool) error {
	if a.opts.agent != "" {
		return a.setAgentByName(a.opts.agent, true)
	}
	if env := strings.TrimSpace(os.Getenv(config.EnvAgent)); env != "" {
		return a.setAgentByName(env, false)
	}
	prefs := config.LoadPrefs()
	if prefs.DefaultAgent != "" {
		if err := a.setAgentByName(prefs.DefaultAgent, false); err == nil {
			return nil
		}
	}
	if len(a.agents) == 1 {
		return a.setAgentByName(a.agents[0].Codename, false)
	}
	if !interactive {
		return fmt.Errorf("this workspace has %d agents — choose one with --agent (see `tlc agents`)", len(a.agents))
	}
	return a.pickAgent()
}

func (a *app) pickAgent() error {
	options := make([]string, 0, len(a.agents))
	for _, ag := range a.agents {
		opt := ag.Codename
		if l := label(ag); l != "-" {
			opt += " — " + l
		}
		opt += a.theme.Dim(" (" + modelNote(ag) + ")")
		options = append(options, opt)
	}
	idx, err := a.repl.Pick("Select an agent:", options)
	if err != nil {
		return err
	}
	return a.setAgentByName(a.agents[idx].Codename, true)
}

func (a *app) setAgentByName(name string, remember bool) error {
	for _, ag := range a.agents {
		if strings.EqualFold(ag.Codename, name) {
			changed := a.agent.Codename != ag.Codename
			a.agent = ag
			a.runner.SetAgent(ag.Codename)
			a.runner.SystemPrompt = agent.SystemPrompt(agent.PromptEnv{
				Cwd:           a.cwd,
				Agent:         ag.Codename,
				Model:         ag.Model,
				HasMemoryTool: true,
			})
			if remember {
				prefs := config.LoadPrefs()
				prefs.DefaultAgent = ag.Codename
				_ = config.SavePrefs(prefs)
			}
			if changed && a.repl != nil {
				a.renderer.Notice("agent switched to " + ag.Codename + " — memory is per agent")
			}
			return nil
		}
	}
	names := make([]string, len(a.agents))
	for i, ag := range a.agents {
		names[i] = ag.Codename
	}
	return fmt.Errorf("unknown agent %q — this workspace has: %s", name, strings.Join(names, ", "))
}

func (a *app) transcriptPath() string {
	if a.transcript == nil {
		return "(none)"
	}
	return a.transcript.Path
}

// ---------------------------------------------------------------- helpers

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

// friendlyErr translates API errors into actionable messages.
func friendlyErr(err error) string {
	var notConfigured *config.ErrNotConfigured
	if errors.As(err, &notConfigured) {
		return err.Error()
	}
	var apiErr *api.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.Status {
		case 401:
			return "authentication failed — the api key was rejected (run `tlc configure`)"
		case 402:
			return "insufficient credits on the turnlith account: " + apiErr.Message
		case 403:
			return "this api key cannot manage agents — creating agents, switching models and setting effort need an admin-role key (mint one in the console under API keys, or edit the agent there)"
		case 404:
			if apiErr.Code == "model_not_found" {
				return apiErr.Message + " — run `tlc agents` to list the workspace's agents"
			}
		}
		return err.Error()
	}
	if errors.Is(err, io.EOF) {
		return "the server closed the connection unexpectedly"
	}
	return err.Error()
}
