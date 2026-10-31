# tlc reference

Detailed behavior for `tlc`. For an overview and quick start, see the [README](../README.md).

- [Tools and permissions](#tools-and-permissions)
- [Project instructions](#project-instructions)
- [Configuration](#configuration)
- [Usage and flags](#usage-and-flags)
- [Files and REPL commands](#files-and-repl-commands)
- [Working with the agent's changes](#working-with-the-agents-changes)
- [Using tlc inside other coding clients](#using-tlc-inside-other-coding-clients)
- [Resilience and troubleshooting](#resilience-and-troubleshooting)

## Tools and permissions

Your turnlith agent's memory lives on the server, not in this process. The
CLI holds only the current task's wire history: each turn sends the fresh
prompt (plus any in-flight tool calls and their results), and the server
assembles the rest of the context from the agent's session memory.

Tools run **locally** — the server never sees your filesystem. The CLI offers
eight tools:

| Tool           | What it does                              | Asks first? |
| -------------- | ----------------------------------------- | :---------: |
| `Read`         | read a file with line numbers              |      —      |
| `Glob`         | find files by pattern                      |      —      |
| `Grep`         | search file contents (ripgrep if present)  |      —      |
| `SearchMemory` | ask the agent's server-side memory         |      —      |
| `TodoWrite`    | keep a checklist for multi-step work       |      —      |
| `Bash`         | run a shell command in the project         |     yes     |
| `Write`        | create or overwrite a file                 |     yes     |
| `Edit`         | replace a string in a file                 |     yes     |

Gated tools consult the session's **permission mode** — `normal`, `auto`,
`plan`, or `yolo`:

| Mode     | Behavior                                                  |
| -------- | --------------------------------------------------------- |
| `normal` | every gated call asks first (the default)                  |
| `auto`   | `Write`/`Edit` run without asking; `Bash` still asks       |
| `plan`   | read-only: the agent can look around and plan, but edits and commands are refused (read-only `git`/`ls` still run) |
| `yolo`   | everything runs without asking (`--yolo`, `/permissions yolo`) |

Switch with `/permissions [normal|auto|plan|yolo]` (TAB completes the word),
press `Tab` on an empty prompt to cycle `normal → auto → plan → normal` (Tab
never enters `yolo` — that takes `/permissions yolo` or `--yolo`), or use
`--permission-mode` up front. The prompt shows where you are: `❯` normal,
`⏵❯` auto, `⏸❯` plan, `⏵⏵❯` yolo.

**Answering a prompt** takes one keypress: `y` allow (Enter too), `n` deny —
then an optional one-line reason, which the model receives with the refusal
("use the helper in util.go instead") — `a` always allow *this kind of call*
for the session, `p` always allow it in this project, `x` never this session,
Ctrl-C aborts. "This kind of call" is precise: for `Bash` it is the command
prefix (`Bash(go test:*)`, not "every command"); a compound command
(`cd x && make`) has no honest single prefix, so `a`/`p` are not offered for it.

**Permission rules** make those answers durable and scriptable. A rule is
`Tool` or `Tool(pattern)`:

```
Bash(go test:*)     any command starting with "go test" (at a word boundary)
Bash(git status)    exactly that command
Edit(docs/**)       edits to files under docs/
Write               every Write
```

A chained command (`a && b; c | d`) is allowed only if *every* part is allowed,
and anything with command substitution or redirection (`$(…)`, backticks, `<`,
`>` other than `2>&1` and `/dev/null`) is never covered by an allow rule.
Rules come from `--allowed-tools` / `--disallowed-tools`, from your `p` answers
(stored per project in `~/.config/tlith/tlc.json`, never in the repository),
and from `.tlc/settings.json` in the repo:

```json
{ "permissions": { "deny": ["Bash(git push:*)", "Edit(migrations/**)"] } }
```

A repository can **forbid** things for everyone who works on it but can never
grant itself permissions: `allow` rules in that file are ignored (and
reported). Deny rules beat everything, even `--yolo`. A few read-only commands
(`git status|diff|log|show|rev-parse|ls-files`, `ls`, `pwd`, `which`, `cd`) never
ask; `cat` and `find` are deliberately not among them.

Files must be read before they can be edited — and re-read if they changed on
disk since — so the model can't blind-overwrite work it has never seen or
clobber your concurrent edits. Edits keep a file's line endings (CRLF stays
CRLF) and are written atomically.

**The workspace boundary.** `Read`, `Glob`, `Grep`, `Write` and `Edit` work
freely inside the directory you started `tlc` in. Touching anything outside it
(`../x`, `/etc/…`, `~/…`, or a symlink that leads out) asks first, in every
mode except `yolo`, and `auto` mode does not wave such edits through. Credential
stores — `~/.ssh`, `~/.aws`, `~/.gnupg`, `~/.kube`, cloud/registry tokens, and
tlc's own `~/.config/tlith` (your API key) — are blocked outright for these
tools, even in `yolo`. (`Bash` can still run any command you approve, so read
what it asks to run.)

Independent read-only tool calls from one model turn (`Read`, `Glob`, `Grep`,
`SearchMemory`) run in parallel; anything that changes state, or asks, runs one
at a time in the order requested.

## Project instructions

`tlc` loads the nearest `TLC.md`, `AGENTS.md` or `CLAUDE.md` (searched from the
directory you start in up to the git root, in that order) into the agent's
system prompt, plus your personal `~/.config/tlith/TLC.md`. It prints which
file it loaded. Each file is capped (4 KB; `TLC_INSTRUCTIONS_MAX`, `0` turns
loading off) because the prompt shares the server's context budget — keep them
terse. Run `/init` and the agent writes a first `TLC.md` for the repository
from what is actually in it. Instruction files are repository content: they
guide conventions and commands but can never override the safety rules or the
permission system.

## Configuration

```sh
tlc configure --base-url https://dash.turnlith.com
```

`tlc configure` asks for the key without echoing it, verifies the server's
health endpoint and the key, and only then saves. For scripts, pipe the key in
(`echo "$KEY" | tlc configure --base-url … --api-key-stdin`) instead of using
`--api-key`, which other users on the machine can see in the process list. The
server URL must be `https://`; plain `http://` is accepted only for
`localhost`/loopback unless you set `TLC_ALLOW_INSECURE=1` for a network you
trust. The connection lives in `~/.config/tlith/config.json` (mode 600) —
the same path the retired `tlith` setup helper used, so existing setups keep
working; one file now serves everything. Chatting needs an **agent-role**
key; creating agents or switching a model/effort needs an **admin-role** key
(both are minted in the turnlith web console, under API keys). Environment
overrides `TLITH_BASE_URL` / `TLITH_API_KEY` win over the file, for CI or
throwaway runs.

The installer script accepts the same values through the environment, for
one-paste setup (the key never touches a URL):

```sh
curl -fsSL https://raw.githubusercontent.com/turnlith-AI/turnlith-CLI/main/install.sh \
  | TLITH_API_KEY=sk-… TLITH_BASE_URL=https://dash.turnlith.com sh
```

The installer verifies the release checksum and installs to `~/.local/bin/tlc`
(`TLC_INSTALL_DIR` and `TLC_VERSION` override the destination and version).
On Windows the Bash tool uses `cmd.exe` unless Git Bash is installed.

## Usage and flags

```sh
tlc                          # interactive session in the current directory
tlc "fix the failing test"   # start with an initial prompt
tlc -p "explain main.go"     # one-shot: answer on stdout, activity on stderr
tlc -c                       # continue the previous conversation here
tlc --agent sigma            # pick the agent (model) explicitly
tlc agents                   # list the workspace's agents
tlc agents create            # create an agent (ask-first wizard on a terminal)
tlc install                  # wire the agents into every coding client it detects
```

`--agent` matters: memory, transcripts, and billing are per agent. The
default agent is remembered in `~/.config/tlith/tlc.json`, overridable with
`TLC_AGENT`.

Creating an agent works interactively (the codename, model, and — when the
provider supports it — reasoning effort are asked for) or entirely from flags:

```sh
tlc agents create --name bug-hunter --label "Bug hunter" \
                  --model openai:gpt-6-sol --effort high
```

`tlc agents create` takes `--name` (codename clients call; generated when
omitted), `--label`, `--model`, and `--effort`
(`none|minimal|low|medium|high|xhigh`).

| Flag              | Meaning                                                     |
| ----------------- | ----------------------------------------------------------- |
| `-p, --print`     | non-interactive; assistant text on stdout, everything else on stderr. Exit status `0` ok, `1` the run failed, `130` interrupted. Piped stdin is appended to the prompt: `git diff \| tlc -p "review this"` |
| `-c, --continue`  | replay the previous local transcript for this directory     |
| `--agent <name>`  | agent codename (default: last used, or the only agent)      |
| `--yolo`          | run gated tools without prompting                            |
| `--permission-mode <m>` | `normal`, `auto`, `plan` or `yolo`                     |
| `--allowed-tools <rules>` / `--disallowed-tools <rules>` | comma-separated permission rules, e.g. `"Bash(go test:*),Edit(docs/**)"` |
| `--max-turns <n>` | `-p`: stop after n model turns (exit 1 if the task is unfinished) |
| `--output-format <f>` | `-p`: `text` (default), `json` (one result object: `result`, `is_error`, `turns`, `usage`, …) or `stream-json` (one event per line: `text`, `tool_use`, `tool_result`, then `result`) |
| `--verbose`       | show token usage after each turn                             |
| `--base-url`, `--api-key` | connection overrides for this run                   |

## Files and REPL commands

**Files.** Type `@` in any prompt to mention a file — `@internal/ui/repl.go`
attaches its contents to your message, so the model sees it (and can edit it)
without a read round-trip. TAB completes paths while you type: `@cli.g<TAB>`
finds `internal/cli/cli.go`, directories complete with a trailing `/`.
`/files` opens an arrow-key browser over the workspace (heavyweight trees
like `node_modules` are skipped) with the files this session has read,
written, or edited pinned on top; picking one pre-fills the prompt with its
`@mention`. Mentions work in one-shot mode too: `tlc -p "explain @main.go"`.

**Commands.** In the REPL: `/help`, `/clear`, `/init`, `/undo`, `/diff`, `/copy`,
`/resume`, `/doctor`, `/edit`, `/files`, `/agent [name|new]`, `/agents`,
`/model [spec] [effort]`, `/effort [level]`, `/status`,
`/permissions [normal|auto|plan|yolo]`, `/verbose`, `/yolo`, `/exit`. Typing `/`
prints the command menu and `TAB` completes commands, their arguments (agent
codenames, model ids, effort levels, permission modes), and `@file` mentions.
`/model` and `/effort` persist on the agent server-side, so the change applies
to every client of that agent. `Ctrl-C` interrupts the run in progress;
another `Ctrl-C` at an empty prompt quits.

**Long prompts.** End a line with `\` to continue on the next, or run `/edit` to
write the prompt in `$VISUAL`/`$EDITOR`. Every prompt ends with a dim cost line
(`· 4.2k context · 87 out · 2 turns · 6.3s`).

## Working with the agent's changes

Edits and writes show up as numbered, colored diffs in the transcript, and the
permission prompt shows the same diff *before* you answer; an edit that cannot
apply (text not found, file never read) goes straight back to the model
instead of asking you. `/diff` shows what the session changed (via git,
including new files), `/undo` reverts the last change (repeatable; it refuses
if you edited the file since, unless you say `/undo force`, and the agent is
told so it re-reads the file), and `/copy` puts the last answer on the
clipboard (system tools, or OSC 52 over ssh).

**Sessions.** `/resume` lists earlier sessions in this directory and continues
one (switching to the agent it was with, since memory is per agent); `-c`
continues the latest one, preferring the current agent. Local transcripts
(display-only — the real memory is on the server) are JSONL files under
`~/.local/share/tlith/tlc/sessions/`, one per session.

## Using tlc inside other coding clients

`tlc install` wires your turnlith agents into the coding clients you already
use:

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

## Resilience and troubleshooting

Requests the server rejected before doing any work (HTTP 429, 502, 503, or a
refused connection) are retried three times with backoff, honoring
`Retry-After`, and the retry is announced. Nothing is re-sent once output has
started, and a stream that ends before the model finished is reported as an
error instead of being treated as an answer. A stream that goes silent is
abandoned after 5 minutes (`TLC_STREAM_IDLE_TIMEOUT`, e.g. `15m`; `0`
disables). Long `Bash` output keeps both its beginning and its end.

`tlc doctor` (or `/doctor`) checks the saved connection, TLS/URL safety,
server health, the key, the agent and whether its provider has credentials
server-side, then the terminal, shell, git, ripgrep, file permissions and the
project instructions it would load — with a fix for each problem.
`tlc completion bash|zsh|fish` prints a completion script generated from the
real flags; `tlc <command> --help` shows per-command help.
