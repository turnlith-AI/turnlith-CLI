package cli

import (
	"fmt"
	"strings"

	"github.com/turnlith-AI/turnlith-CLI/internal/harness"
	"github.com/turnlith-AI/turnlith-CLI/internal/version"
)

const helpText = `tlc ` + version.Version + ` — connect your turnlith workspace to your coding tools

Usage:
  tlc <command> [options]

Commands:
  tlc configure              save the server URL and api key
  tlc agents                 list the workspace's agents
  tlc agents create          create an agent (interactive, needs an admin key)
  tlc install [client...]    wire the server's agents into coding clients (opencode, pi,
                             continue, cursor, cline); no name = every client it detects
  tlc uninstall [client...]  remove the turnlith entries from those clients
  tlc doctor                 check the connection, key, agents and coding clients
  tlc completion <shell>     print a completion script (bash, zsh, fish)
  tlc help [command]         show help; tlc version prints the version
                             (also -h/--help and -v/--version)

Options:
      --base-url      override the server URL for this run
      --api-key       override the api key for this run (visible in 'ps'; prefer the environment)
      --api-key-stdin with 'tlc configure': read the api key from stdin

Options for ` + "`tlc agents create`" + `:
      --name      codename clients call (default: generated)
      --label     human-readable label
      --model     model spec, e.g. gemini:gemini-3.8-flash
      --effort    reasoning effort: none|minimal|low|medium|high|xhigh

Options for ` + "`tlc install`" + `:
      --list          show each client, what tlc can write there and what is installed
      --if-detected   no client named: do nothing (exit 0) when none is detected
      --project       write the project-level config (./opencode.json, ./.cursor/mcp.json, ./.continue)
      --provider-id   provider key in the config (default: tlith)
      --no-mcp        skip the turnlith-controller MCP server
      --use-env       reference $TLITH_API_KEY instead of embedding the key

Exit status: 0 ok, 1 the command failed, 2 usage error.

Environment: TLITH_BASE_URL, TLITH_API_KEY (connection, win over the saved file),
             TLC_ALLOW_INSECURE=1 (allow plain http to a non-local server),
             NO_COLOR (no colors)
`

type options struct {
	command string // "", "configure", "doctor", "agents", "agents-create", "install", "uninstall", "completion", "help", "version"
	baseURL string
	apiKey  string
	// apiKeyStdin: `tlc configure --api-key-stdin` reads the key from stdin so
	// it never appears in the process list or shell history.
	apiKeyStdin bool

	// shell is the argument of `tlc completion <shell>`.
	shell string

	// help: -h/--help (or `tlc help <topic>`) asks for help, per command when
	// a command was given
	wantHelp  bool
	helpTopic string

	// `tlc agents create`
	name   string
	label  string
	model  string
	effort string

	// `tlc install` / `tlc uninstall`
	clients    []string // canonical client ids; empty = detect (install) / find (uninstall)
	project    bool
	providerID string
	noMcp      bool
	useEnv     bool
	list       bool // install --list: show the clients and their state, change nothing
	ifDetected bool // install: no client named and none detected is not an error
}

// boolFlags are the switches; valueFlags take an argument (`--flag v` or
// `--flag=v`). Both are tables so a new flag is one line, and a test checks
// every flag is documented in the help text and the completions.
var boolFlags = map[string]func(*options){
	"-h": func(o *options) { o.wantHelp = true }, "--help": func(o *options) { o.wantHelp = true },
	"-v": func(o *options) { o.command = "version" }, "--version": func(o *options) { o.command = "version" },
	"--project":       func(o *options) { o.project = true },
	"--no-mcp":        func(o *options) { o.noMcp = true },
	"--use-env":       func(o *options) { o.useEnv = true },
	"--list":          func(o *options) { o.list = true },
	"--if-detected":   func(o *options) { o.ifDetected = true },
	"--api-key-stdin": func(o *options) { o.apiKeyStdin = true },
}

var valueFlags = map[string]func(*options, string) error{
	"--name":        func(o *options, v string) error { o.name = v; return nil },
	"--label":       func(o *options, v string) error { o.label = v; return nil },
	"--model":       func(o *options, v string) error { o.model = v; return nil },
	"--effort":      func(o *options, v string) error { o.effort = v; return nil },
	"--provider-id": func(o *options, v string) error { o.providerID = v; return nil },
	"--base-url":    func(o *options, v string) error { o.baseURL = v; return nil },
	"--api-key":     func(o *options, v string) error { o.apiKey = v; return nil },
}

// removedFlags are the switches of the coding agent that tlc used to include.
// They get one clear message instead of a generic "unknown flag".
var removedFlags = map[string]bool{
	"-p": true, "--print": true, "-c": true, "--continue": true,
	"--yolo": true, "--verbose": true, "--agent": true,
	"--permission-mode": true, "--allowed-tools": true, "--disallowed-tools": true,
	"--max-turns": true, "--output-format": true,
}

// commands are the words accepted in the command position.
var commands = []string{"configure", "agents", "install", "uninstall", "doctor", "completion", "help", "version"}

// usageError marks a bad invocation (exit status 2) as opposed to a runtime
// failure (1).
type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }

const removedNote = "tlc no longer includes a coding agent: it connects your turnlith workspace to your coding tools — see `tlc help`"

// resolveClientNames maps one `install`/`uninstall` argument to canonical
// client ids: a client name or alias, or "all".
func resolveClientNames(arg string) ([]string, error) {
	if strings.EqualFold(arg, "all") {
		return harness.Names(), nil
	}
	if c, ok := harness.Lookup(arg); ok {
		return []string{c.ID}, nil
	}
	return nil, fmt.Errorf("unknown client %q — supported: %s, or \"all\"", arg, strings.Join(harness.Names(), ", "))
}

func contains(list []string, s string) bool {
	for _, item := range list {
		if item == s {
			return true
		}
	}
	return false
}

// suggest returns the word of candidates closest to s (edit distance <= 2),
// or "" when nothing is close.
func suggest(s string, candidates []string) string {
	best, bestDist := "", 3
	for _, c := range candidates {
		if d := editDistance(strings.ToLower(s), c); d < bestDist {
			best, bestDist = c, d
		}
	}
	return best
}

func editDistance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur := make([]int, len(rb)+1)
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(rb)]
}

func parseArgs(argv []string) (options, error) {
	opts := options{}
	var positional []string
	for i := 0; i < len(argv); i++ {
		arg := argv[i]
		name, inline, hasInline := arg, "", false
		if strings.HasPrefix(arg, "--") {
			if eq := strings.IndexByte(arg, '='); eq > 0 {
				name, inline, hasInline = arg[:eq], arg[eq+1:], true
			}
		}
		if removedFlags[name] {
			return opts, &usageError{fmt.Sprintf("%s was removed — %s", name, removedNote)}
		}
		if set, ok := boolFlags[name]; ok && !hasInline {
			set(&opts)
			continue
		}
		if set, ok := valueFlags[name]; ok {
			value := inline
			if !hasInline {
				i++
				if i >= len(argv) {
					return opts, &usageError{fmt.Sprintf("%s needs a value", name)}
				}
				value = argv[i]
			}
			if err := set(&opts, value); err != nil {
				return opts, &usageError{err.Error()}
			}
			continue
		}
		switch {
		case arg == "--":
			positional = append(positional, argv[i+1:]...)
			i = len(argv)
		case strings.HasPrefix(arg, "-") && arg != "-":
			return opts, &usageError{fmt.Sprintf("unknown flag %q", arg)}
		default:
			positional = append(positional, arg)
		}
	}

	if opts.command == "version" { // -v/--version wins over everything else
		return opts, nil
	}
	if len(positional) > 0 {
		opts.command = positional[0]
		positional = positional[1:]
		if !contains(commands, opts.command) {
			msg := fmt.Sprintf("unknown command %q", opts.command)
			if s := suggest(opts.command, commands); s != "" {
				msg += fmt.Sprintf(" — did you mean `tlc %s`?", s)
			} else if strings.ContainsAny(opts.command, " \t") {
				msg += " — " + removedNote
			}
			return opts, &usageError{msg}
		}
	}

	// help: -h/--help anywhere, or `tlc help [topic]`
	if opts.command == "help" {
		opts.wantHelp = true
		if len(positional) > 0 {
			opts.helpTopic, positional = positional[0], positional[1:]
			if _, ok := helpTopics[opts.helpTopic]; !ok {
				return opts, &usageError{fmt.Sprintf("no help for %q — try: %s", opts.helpTopic, strings.Join(helpTopicNames(), ", "))}
			}
		}
	} else if opts.wantHelp {
		if _, ok := helpTopics[opts.command]; ok {
			opts.helpTopic = opts.command
		}
		opts.command = "help"
		return opts, nil
	}

	switch opts.command {
	case "":
		if opts.hasFlags() {
			return opts, &usageError{"missing command — run `tlc help`"}
		}
		opts.command = "help"
	case "agents":
		if len(positional) > 0 {
			sub := positional[0]
			positional = positional[1:]
			switch sub {
			case "list":
			case "create", "new":
				opts.command = "agents-create"
			default:
				return opts, &usageError{fmt.Sprintf("unknown `agents` subcommand %q — use `tlc agents` or `tlc agents create`", sub)}
			}
		}
	case "install", "uninstall":
		for _, arg := range positional {
			ids, err := resolveClientNames(arg)
			if err != nil {
				return opts, &usageError{err.Error()}
			}
			for _, id := range ids {
				if !contains(opts.clients, id) {
					opts.clients = append(opts.clients, id)
				}
			}
		}
		positional = nil
	case "completion":
		if len(positional) > 0 {
			opts.shell, positional = positional[0], positional[1:]
		}
	}
	if len(positional) > 0 {
		return opts, &usageError{fmt.Sprintf("`tlc %s` takes no argument %q", strings.TrimSuffix(opts.command, "-create"), positional[0])}
	}
	return opts, nil
}

// hasFlags reports whether any option was set, to tell `tlc` (show help) from
// `tlc --base-url x` (a command is missing).
func (o options) hasFlags() bool {
	return o.baseURL != "" || o.apiKey != "" || o.apiKeyStdin || o.name != "" || o.label != "" ||
		o.model != "" || o.effort != "" || o.project || o.providerID != "" || o.noMcp || o.useEnv ||
		o.list || o.ifDetected
}
