# turnlith-CLI (`tlc`)

A Claude Code-style coding agent for the terminal, backed by a
[turnlith](https://turnlith.com) server. You type in your project directory;
the model streams back an answer and runs local tools — reading and editing
files, running commands, searching the codebase — behind per-tool permission
prompts.

```
$ tlc
tlc v0.2.0 · sigma (gemini:gemini-3.8-flash) · ~/code/my-app
type /help for commands · Tab cycles the permission mode · /exit to quit
❯ why is the login test flaky?

⏺ Read(internal/auth/login_test.go)
  ⎿  1→package auth
     2→
     …
Looking at the test, the flake comes from a shared clock …
```

## How it works

Your turnlith agent's memory lives on the server, not in this process. The
CLI holds only the current task's wire history: each turn sends the fresh
prompt (plus any in-flight tool calls and their results), and the server
assembles the rest of the context from the agent's session memory. Close the
laptop, reopen it, and the agent still remembers the project.

Tools run **locally** — the server never sees your filesystem. The CLI offers
seven tools, matching the harness vocabulary:

| Tool           | What it does                              | Asks first? |
| -------------- | ----------------------------------------- | :---------: |
| `Read`         | read a file with line numbers              |      —      |
| `Glob`         | find files by pattern                      |      —      |
| `Grep`         | search file contents (ripgrep if present)  |      —      |
| `SearchMemory` | ask the agent's server-side memory         |      —      |
| `Bash`         | run a shell command in the project         |     yes     |
| `Write`        | create or overwrite a file                 |     yes     |
| `Edit`         | replace a string in a file                 |     yes     |

Gated tools consult the session's **permission mode** — `normal`, `auto`, or
`yolo`:

| Mode     | Behavior                                                  |
| -------- | --------------------------------------------------------- |
| `normal` | every gated call asks first (the default)                  |
| `auto`   | `Write`/`Edit` run without asking; `Bash` still asks       |
| `yolo`   | everything runs without asking (`--yolo`, `/permissions yolo`) |

Switch with `/permissions [normal|auto|yolo]` (TAB completes the word), press
`Tab` on an empty prompt to toggle `normal ⇄ auto` (Tab never enters `yolo` —
that takes `/permissions yolo` or `--yolo`). The prompt shows where you are: `❯` normal, `⏵❯` auto,
`⏵⏵❯` yolo. When a call does ask, one keypress answers — `y` allow, `a`
allow this tool for the session, `n` deny, `x` never this session; Enter
allows, Ctrl-C aborts. A session rule (always/never) survives mode switches.
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

## Install

**Prebuilt binary** — Linux and macOS (amd64/arm64):

```sh
curl -fsSL https://raw.githubusercontent.com/turnlith-AI/turnlith-CLI/main/install.sh | sh
```

The script verifies the release checksum and installs to `~/.local/bin/tlc`
(`TLC_INSTALL_DIR` and `TLC_VERSION` override the destination and version).
Windows builds and manual downloads live on the
[releases page](https://github.com/turnlith-AI/turnlith-CLI/releases); on
Windows the Bash tool uses `cmd.exe` unless Git Bash is installed.

**With Go 1.26+:**

```sh
go install github.com/turnlith-AI/turnlith-CLI/cmd/tlc@latest
```

**From source:**

```sh
git clone https://github.com/turnlith-AI/turnlith-CLI
cd turnlith-CLI
make install           # builds to ~/.local/bin/tlc (override with PREFIX=…)
```

or run it without installing:

```sh
go run ./cmd/tlc
```

## Configure

```sh
tlc configure --base-url https://turnlith.com
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
  | TLITH_API_KEY=sk-… TLITH_BASE_URL=https://turnlith.com sh
```

## Use it inside opencode

`tlc` also wires your turnlith agents into other harnesses. For
[opencode](https://opencode.ai):

```sh
tlc install opencode
```

This writes a `turnlith` provider into `~/.config/opencode/opencode.json`
(or the project's `./opencode.json` with `--project`, preferring an existing
`opencode.jsonc`) exposing every agent as a model under its codename, plus
the `turnlith-controller` MCP server for interactive memory search. The edit
is JSONC-aware: comments and other providers stay untouched, and
`tlc uninstall opencode` removes exactly the turnlith blocks.

| Flag             | Meaning                                              |
| ---------------- | ---------------------------------------------------- |
| `--project`      | target `./opencode.json` in the current directory    |
| `--provider-id`  | provider key in the config (default `tlith`)         |
| `--no-mcp`       | skip the turnlith-controller MCP server block        |
| `--use-env`      | write `{env:TLITH_API_KEY}` instead of embedding the key |

With `--use-env`, export the key in your shell
(`export TLITH_API_KEY=sk-…`) and it stays out of the config file.

## Usage

```sh
tlc                          # interactive session in the current directory
tlc "fix the failing test"   # start with an initial prompt
tlc -p "explain main.go"     # one-shot: answer on stdout, activity on stderr
tlc -c                       # continue the previous conversation here
tlc --agent sigma            # pick the agent (model) explicitly
tlc agents                   # list the workspace's agents
tlc agents create            # create an agent (ask-first wizard on a terminal)
tlc install opencode         # wire the agents into opencode (see below)
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

| Flag              | Meaning                                                     |
| ----------------- | ----------------------------------------------------------- |
| `-p, --print`     | non-interactive; assistant text on stdout, everything else on stderr. Exit status `0` ok, `1` the run failed, `130` interrupted. Piped stdin is appended to the prompt: `git diff \| tlc -p "review this"` |
| `-c, --continue`  | replay the previous local transcript for this directory     |
| `--agent <name>`  | agent codename (default: last used, or the only agent)      |
| `--yolo`          | run gated tools without prompting                            |
| `--verbose`       | show token usage after each turn                             |
| `--base-url`, `--api-key` | connection overrides for this run                   |

`tlc agents create` takes `--name` (codename clients call; generated when
omitted), `--label`, `--model`, and `--effort`
(`none|minimal|low|medium|high|xhigh`).

**Files.** Type `@` in any prompt to mention a file — `@internal/ui/repl.go`
attaches its contents to your message, so the model sees it (and can edit it)
without a read round-trip. TAB completes paths while you type: `@cli.g<TAB>`
finds `internal/cli/cli.go`, directories complete with a trailing `/`.
`/files` opens an arrow-key browser over the workspace (heavyweight trees
like `node_modules` are skipped) with the files this session has read,
written, or edited pinned on top; picking one pre-fills the prompt with its
`@mention`. Mentions work in one-shot mode too: `tlc -p "explain @main.go"`.

In the REPL: `/help`, `/clear`, `/files`, `/agent [name|new]`, `/agents`,
`/model [spec] [effort]`, `/effort [level]`, `/status`, `/permissions
[normal|auto|yolo]`, `/verbose`, `/yolo`, `/exit`; typing `/` prints the
command menu and `TAB` completes commands, their arguments (agent codenames,
model ids, effort levels, permission modes), and `@file` mentions. `/model`
and `/effort` persist on the agent server-side, so the change applies to
every client of that agent. `Ctrl-C` interrupts the run in progress; another
`Ctrl-C` at an empty prompt quits.

**Resilience.** Requests the server rejected before doing any work (HTTP 429,
502, 503, or a refused connection) are retried three times with backoff,
honoring `Retry-After`, and the retry is announced. Nothing is re-sent once
output has started, and a stream that ends before the model finished is
reported as an error instead of being treated as an answer. A stream that goes
silent is abandoned after 5 minutes (`TLC_STREAM_IDLE_TIMEOUT`, e.g. `15m`;
`0` disables). Long `Bash` output keeps both its beginning and its end.

Local transcripts (display-only — the real memory is on the server) are
JSONL files under `~/.local/share/tlith/tlc/sessions/`, one per session, so
`tlc -c` can show you where you left off in this directory.

## Development

```sh
make build     # bin/tlc
make test      # unit + end-to-end tests against a mock server (no network)
make vet
make fmt
```

The test suite includes `internal/cli/cli_test.go`, which drives the real
`Main` against an httptest server that emulates the facade (`/v1/models`,
streamed `/v1/chat/completions`, `/v1/mcp`), covering the tool round trip,
permission denials, `--yolo`, MCP memory search, and error mapping.

### Layout

```
cmd/tlc/            entry point
internal/api/       facade SSE client, MCP client, wire types
internal/agent/     tool loop + system prompt
internal/tools/     Read, Write, Edit, Bash, Glob, Grep, SearchMemory
internal/permissions/  gated-tool approval policy
internal/ui/        theme, streaming markdown renderer, readline REPL
internal/cli/       argument handling, REPL, print mode, install/uninstall
internal/harness/   opencode wiring (JSONC-aware config merge)
internal/history/   local JSONL transcripts
internal/config/    connection file + tlc preferences
```

## License

MIT — see [LICENSE](LICENSE).
