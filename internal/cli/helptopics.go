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
	"install": `tlc install / uninstall — wire the agents into another harness

  tlc install opencode        add a "tlith" provider and the turnlith-controller MCP server
  tlc uninstall opencode      remove exactly those blocks

Options:
      --project       target ./opencode.json in the current directory
      --provider-id   provider key in the config (default: tlith)
      --no-mcp        skip the turnlith-controller MCP server block
      --use-env       write {env:TLITH_API_KEY} instead of embedding the key

Comments and other providers in the config are left untouched.
`,
	"uninstall": `tlc uninstall opencode — remove the turnlith blocks from the opencode config

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
