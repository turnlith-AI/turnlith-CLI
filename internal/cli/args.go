package cli

import (
	"fmt"
	"strconv"
	"strings"

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
  tlc help | version         (also -h/--help and -v/--version)

Options:
  -p, --print     non-interactive; assistant text to stdout, tool activity to stderr
  -c, --continue  replay the previous local transcript for this directory
      --agent     agent codename (defaults to the saved/last-used choice)
      --yolo      start in yolo permission mode (no prompts for gated tools)
      --permission-mode  normal|auto|yolo (what asks first; --yolo is shorthand for yolo)
      --allowed-tools    permission rules to allow, e.g. "Bash(go test:*),Edit(docs/**)"
      --disallowed-tools permission rules to deny (they win over everything, even --yolo)
      --max-turns        -p: stop (exit 1) after this many model turns
      --output-format    -p: text (default), json (one result object), or stream-json (events)
      --verbose   show token usage after each turn
      --base-url  override the server URL for this run
      --api-key   override the api key for this run (visible in 'ps'; prefer the environment)
      --api-key-stdin  with 'tlc configure': read the api key from stdin

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

Exit status of -p: 0 ok, 1 the run failed, 2 usage error, 130 interrupted. Piped stdin
is appended to the prompt.

Environment: TLITH_BASE_URL, TLITH_API_KEY (connection), TLC_AGENT (default agent),
             TLC_ALLOW_INSECURE=1 (allow plain http to a non-local server),
             TLC_STREAM_IDLE_TIMEOUT (abandon a silent stream; default 5m, 0 = never)

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
	// apiKeyStdin: `tlc configure --api-key-stdin` reads the key from stdin so
	// it never appears in the process list or shell history.
	apiKeyStdin bool
	prompt      string

	// permissions and print-mode behavior
	permissionMode  string   // --permission-mode normal|auto|yolo
	allowedTools    []string // --allowed-tools, permission rules
	disallowedTools []string // --disallowed-tools, permission rules
	maxTurns        int      // --max-turns, 0 = default
	outputFormat    string   // --output-format text|json|stream-json

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

// boolFlags are the switches; valueFlags take an argument (`--flag v` or
// `--flag=v`). Both are tables so a new flag is one line, and a test checks
// every flag is documented in the help text.
var boolFlags = map[string]func(*options){
	"-h": func(o *options) { o.command = "help" }, "--help": func(o *options) { o.command = "help" },
	"-v": func(o *options) { o.command = "version" }, "--version": func(o *options) { o.command = "version" },
	"-p": func(o *options) { o.print = true }, "--print": func(o *options) { o.print = true },
	"-c": func(o *options) { o.cont = true }, "--continue": func(o *options) { o.cont = true },
	"--yolo":          func(o *options) { o.yolo = true },
	"--verbose":       func(o *options) { o.verbose = true },
	"--project":       func(o *options) { o.project = true },
	"--no-mcp":        func(o *options) { o.noMcp = true },
	"--use-env":       func(o *options) { o.useEnv = true },
	"--api-key-stdin": func(o *options) { o.apiKeyStdin = true },
}

var valueFlags = map[string]func(*options, string) error{
	"--agent":       func(o *options, v string) error { o.agent = v; return nil },
	"--name":        func(o *options, v string) error { o.name = v; return nil },
	"--label":       func(o *options, v string) error { o.label = v; return nil },
	"--model":       func(o *options, v string) error { o.model = v; return nil },
	"--effort":      func(o *options, v string) error { o.effort = v; return nil },
	"--provider-id": func(o *options, v string) error { o.providerID = v; return nil },
	"--base-url":    func(o *options, v string) error { o.baseURL = v; return nil },
	"--api-key":     func(o *options, v string) error { o.apiKey = v; return nil },
	"--allowed-tools": func(o *options, v string) error {
		o.allowedTools = append(o.allowedTools, splitRules(v)...)
		return nil
	},
	"--disallowed-tools": func(o *options, v string) error {
		o.disallowedTools = append(o.disallowedTools, splitRules(v)...)
		return nil
	},
	"--permission-mode": func(o *options, v string) error { o.permissionMode = v; return nil },
	"--max-turns": func(o *options, v string) error {
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil || n < 1 {
			return fmt.Errorf("--max-turns needs a positive number, got %q", v)
		}
		o.maxTurns = n
		return nil
	},
	"--output-format": func(o *options, v string) error {
		switch v {
		case "text", "json", "stream-json":
			o.outputFormat = v
			return nil
		}
		return fmt.Errorf("--output-format must be text, json, or stream-json, got %q", v)
	},
}

// splitRules splits a comma-separated rule list, ignoring commas inside
// parentheses ("Bash(echo a,b),Edit" is two rules).
func splitRules(s string) []string {
	var out []string
	depth, start := 0, 0
	for i, r := range s {
		switch r {
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		case ',':
			if depth == 0 {
				out = append(out, strings.TrimSpace(s[start:i]))
				start = i + 1
			}
		}
	}
	out = append(out, strings.TrimSpace(s[start:]))
	kept := out[:0]
	for _, r := range out {
		if r != "" {
			kept = append(kept, r)
		}
	}
	return kept
}

func parseArgs(argv []string) (options, error) {
	opts := options{}
	var promptWords []string
	seenPositional := false
	seenSub := false
	for i := 0; i < len(argv); i++ {
		arg := argv[i]
		name, inline, hasInline := arg, "", false
		if strings.HasPrefix(arg, "--") {
			if eq := strings.IndexByte(arg, '='); eq > 0 {
				name, inline, hasInline = arg[:eq], arg[eq+1:], true
			}
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
					return opts, fmt.Errorf("%s needs a value", name)
				}
				value = argv[i]
			}
			if err := set(&opts, value); err != nil {
				return opts, err
			}
			continue
		}
		switch {
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
