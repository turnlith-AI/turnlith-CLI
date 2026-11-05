# tlc reference

Detailed behavior for `tlc`. For an overview and quick start, see the [README](../README.md).

- [Configuration](#configuration)
- [Usage and flags](#usage-and-flags)
- [Agents](#agents)
- [Using turnlith inside coding clients](#using-turnlith-inside-coding-clients)
- [Diagnosing problems](#diagnosing-problems)
- [Connection resilience](#connection-resilience)
- [Output and exit status](#output-and-exit-status)

## Configuration

```sh
tlc configure --base-url https://turnlith.com
```

`tlc configure` asks for the key without echoing it, verifies the server's
health endpoint and the key, and only then saves. Nothing is saved if either
check fails. For scripts, pipe the key in
(`echo "$KEY" | tlc configure --base-url … --api-key-stdin`) instead of using
`--api-key`, which other users on the machine can see in the process list. Without
a terminal and without those, `configure` stops with an explanation instead of
waiting for input.

The server URL must be `https://`; plain `http://` is accepted only for
`localhost`/loopback unless you set `TLC_ALLOW_INSECURE=1` for a network you
trust. Every command that sends the key holds `--base-url` to this rule too.

The connection lives in `~/.config/tlith/config.json` (mode 600, replaced
atomically, `$XDG_CONFIG_HOME/tlith` when set) — the same path the retired
`tlith` setup helper used, so existing setups keep working. Environment
overrides `TLITH_BASE_URL` / `TLITH_API_KEY` win over the file, for CI or
throwaway runs.

The installer script accepts the same values through the environment, for
one-paste setup (the key is piped to `tlc configure` on stdin, never placed in
a URL or on a command line):

```sh
curl -fsSL https://raw.githubusercontent.com/turnlith-AI/turnlith-CLI/main/install.sh \
  | TLITH_API_KEY=sk-… TLITH_BASE_URL=https://turnlith.com sh
```

The installer verifies the release checksum and installs to `~/.local/bin/tlc`
(`TLC_INSTALL_DIR` and `TLC_VERSION` override the destination and version), then
runs `tlc configure` and `tlc install --if-detected`.

## Usage and flags

```sh
tlc configure                # save and verify the connection
tlc agents                   # list the workspace's agents
tlc agents create            # create an agent (wizard on a terminal)
tlc install                  # wire the agents into every client it detects
tlc install cursor pi        # just these ("all" = every client)
tlc install --list           # what is detected and installed; changes nothing
tlc uninstall                # remove what install added, from every client that has it
tlc doctor                   # diagnose the setup
tlc completion zsh           # print a completion script
```

| Flag | Meaning |
| --- | --- |
| `--base-url <url>` | Server URL for this run. |
| `--api-key <key>` | API key for this run (visible in `ps`; prefer the environment or `--api-key-stdin`). |
| `--api-key-stdin` | `configure`: read the key from stdin. |
| `-h`, `--help` | Help; `tlc <command> --help` for one command. |
| `-v`, `--version` | Print the version. |

Flags may appear before or after the command. A word that is not a command is an
error (with a suggestion when it looks like a typo); a flag from the removed
coding agent (`-p`, `-c`, `--yolo`, `--agent`, …) is rejected with an
explanation.

## Agents

`tlc agents` lists codename, label and model (with the reasoning effort when
set). Create one interactively or entirely from flags:

```sh
tlc agents create --name bug-hunter --label "Bug hunter" \
                  --model openai:gpt-6-sol --effort high
```

`tlc agents create` takes `--name` (codename; generated when omitted, 3–63
lowercase letters, digits and hyphens), `--label`, `--model`, and `--effort`
(`none|minimal|low|medium|high|xhigh`, only where the provider supports it; `default` leaves
it to the provider). It checks the model is available in the workspace and the
effort is one the provider accepts *before* sending anything. Creating agents
needs an **admin-role** key; listing works with any key. Both kinds of key are
minted in the console under API keys.

After creating an agent, run `tlc install` again so your clients learn about it.

## Using turnlith inside coding clients

`tlc install` wires your turnlith agents into the coding clients you already
use. It first asks the server for your agents — which also proves the URL and
key work *before* anything is written.

```sh
tlc install                 # every client it detects
tlc install cursor pi       # just these (or "all")
tlc install --list          # what is detected, what tlc can write, what is installed
tlc uninstall               # remove exactly what install added, from every client that has it
```

| Client | What tlc writes | Where |
| ------ | --------------- | ----- |
| [opencode](https://opencode.ai) | a `tlith` provider (every agent as a model) + the `turnlith-controller` MCP server | `~/.config/opencode/opencode.json` (or `./opencode.json` with `--project`; an existing `opencode.jsonc` is preferred) |
| Pi | a `tlith` provider (every agent as a model); Pi has no MCP | `~/.pi/agent/models.json` (`$PI_CODING_AGENT_DIR` if set) |
| Continue | the agents as models + the MCP server, as files tlc owns | `~/.continue/models/tlith.yaml` and `~/.continue/mcpServers/turnlith-controller.yaml` (`$CONTINUE_GLOBAL_DIR` if set; `./.continue/…` with `--project`) |
| Cursor | the `turnlith-controller` MCP server; model settings are manual | `~/.cursor/mcp.json` (or `./.cursor/mcp.json` with `--project`) |
| Cline | the `turnlith-controller` MCP server; model settings are manual | `cline_mcp_settings.json` of every VS Code flavour that has Cline (VS Code, Insiders, VSCodium, Cursor, Windsurf) |

Cursor and Cline keep their model settings (a custom OpenAI base URL and model
names) in app state, not in a file tlc can edit, so `tlc install` prints the
steps: the base URL, where to paste your key, and the agent codenames to add.

JSON configs are edited in place: comments, formatting and other entries stay
untouched, and `tlc uninstall` removes exactly the turnlith entries. For
Continue, tlc writes whole files of its own, marked on the first line; it
refuses to overwrite a same-named file it did not write and only ever deletes
marked ones.

| Flag             | Meaning                                              |
| ---------------- | ---------------------------------------------------- |
| `--list`         | show each client, what tlc can write there and what is installed; changes nothing |
| `--if-detected`  | no client named: exit 0 quietly when none is detected (used by `install.sh`) |
| `--project`      | write the project-level config (opencode, Cursor, Continue) |
| `--provider-id`  | provider key in the config, default `tlith` (opencode, Pi, Continue) |
| `--no-mcp`       | skip the turnlith-controller MCP server              |
| `--use-env`      | reference `TLITH_API_KEY` instead of embedding the key (not Cline) |

A flag a client cannot honour skips that client with a note, so a global config
is never written when you asked for a project one, and the key is never
embedded when you asked for the environment. When it is the only client you
named, it is a usage error (exit 2). If one client's config cannot be written
(say, an `mcp.json` with a syntax error), the others are still installed and the
exit status is 1.

With `--use-env`, export the key in your shell (`export TLITH_API_KEY=sk-…`) and
it stays out of the config file. The reference differs per client: opencode
`{env:TLITH_API_KEY}`, Pi `$TLITH_API_KEY`, Cursor `${env:TLITH_API_KEY}`,
Continue `${{ secrets.TLITH_API_KEY }}` (Continue resolves secrets from its
`.env` files).

The model list written into a client is a snapshot of your agents at install
time; run `tlc install` again after creating, renaming or deleting an agent. It
is safe to repeat — the same entries are rewritten in place.

## Diagnosing problems

`tlc doctor` checks, in order: the saved connection (and that its URL is safe
for an API key), the server's health, the API key, whether each agent's provider
has credentials on the server, the connection file's permissions, and each
coding client found on the machine — whether turnlith is installed in it. Each
problem comes with a fix. It works even when nothing is configured, and exits
`1` when a check failed (warnings do not fail it).

`tlc completion bash|zsh|fish` prints a completion script generated from the
real flags and commands.

## Connection resilience

Requests the server rejected before doing any work (HTTP 429, 502, 503, or a
refused connection) are retried three times with backoff, honoring
`Retry-After`; each retry is announced on stderr. Other errors are reported once,
translated into what to do about them (rejected key, missing admin role,
certificate problem, unreachable host). `tlc doctor` never retries, so a broken
server is reported at once.

## Output and exit status

Results go to stdout; errors, warnings and retry notices go to stderr, so
`tlc agents | …` sees only the table. Colors are used only on a terminal and
`NO_COLOR` turns them off. Text that came from the server (agent labels, error
messages) has its terminal control characters removed before it is shown.

| Status | Meaning |
| --- | --- |
| `0` | success (for `doctor`: no failed check) |
| `1` | the command failed (server, key, a config that could not be written) |
| `2` | usage error (unknown command or flag, bad value, no terminal to prompt on) |
| `130` | `configure` was interrupted at a prompt |
