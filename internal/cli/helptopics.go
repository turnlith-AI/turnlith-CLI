package cli

// helpTopics are the per-command help pages: `tlc agents --help`,
// `tlc help install`. The general page is helpText.
var helpTopics = map[string]string{
	"agents": `tlc agents — the workspace's agents

  tlc agents                list agents: codename, label, model
  tlc agents create         create an agent (asks for what you leave out on a terminal)

Options for create:
      --name      codename clients call (default: generated)
      --label     human-readable label
      --model     model spec, e.g. gemini:gemini-3.8-flash
      --effort    reasoning effort: none|minimal|low|medium|high|xhigh (when the provider supports it)

Creating agents and changing a model needs an admin-role key; chatting needs an agent-role key.
`,
	"configure": `tlc configure — save the server URL and api key

  tlc configure --base-url https://turnlith.com      asks for the key without echoing it
  echo "$KEY" | tlc configure --base-url … --api-key-stdin

The key and URL are verified (health check, then an authenticated call) before
they are saved to ~/.config/tlith/config.json (mode 600). The URL must be
https:// — plain http is accepted only for localhost, or with TLC_ALLOW_INSECURE=1.
--api-key works too but shows up in the process list.
`,
	"doctor": `tlc doctor — find out why it doesn't work

Checks, in order: the saved connection, the server's health, the api key, the
selected agent (and whether its provider has credentials), then the local
environment (terminal, shell, git, ripgrep, config permissions, project
instructions).

Exit status is 1 when something failed, 0 otherwise (warnings do not fail).
Inside a session, /doctor runs the same checks.
`,
	"install": `tlc install / uninstall — wire the agents into your coding clients

  tlc install                 every client it detects (opencode, pi, continue, cursor, cline)
  tlc install cursor pi       just these ("all" = every client)
  tlc install --list          show each client, what tlc can write there, and what is installed
  tlc uninstall [client...]   remove exactly what install added (no name = every client that has it)

What each client gets:
  opencode   a "tlith" provider (the agents as models) + the turnlith-controller MCP server
  pi         a "tlith" provider in ~/.pi/agent/models.json (Pi has no MCP)
  continue   models + MCP server as tlc-owned files under ~/.continue
  cursor     the MCP server in ~/.cursor/mcp.json — model settings are manual (printed)
  cline      the MCP server in Cline's settings — model settings are manual (printed)

Options:
      --list          show the clients and their state; change nothing
      --if-detected   no client named: exit 0 quietly when none is detected (for scripts)
      --project       write the project-level config (opencode, cursor, continue)
      --provider-id   provider key in the config (default: tlith; opencode, pi, continue)
      --no-mcp        skip the turnlith-controller MCP server
      --use-env       reference $TLITH_API_KEY instead of embedding the key (not cline)

A flag a client cannot honour skips that client with a note (an error when it is the
only one named). Comments and other entries in JSON configs are left untouched.
`,
	"uninstall": `tlc uninstall [client...] — remove the turnlith entries added by tlc install

  tlc uninstall               every client that has them
  tlc uninstall opencode      just opencode

Only what tlc wrote is removed; the files tlc owns for Continue are deleted, anything
you wrote yourself is left alone. --project and --provider-id work as for install.

See: tlc help install
`,
	"completion": `tlc completion — shell completion for tlc

  tlc completion bash > ~/.local/share/bash-completion/completions/tlc
  tlc completion zsh  > "${fpath[1]}/_tlc"
  tlc completion fish > ~/.config/fish/completions/tlc.fish

The scripts are generated from the same flag tables the parser uses, so they
always match this version.
`,
}
