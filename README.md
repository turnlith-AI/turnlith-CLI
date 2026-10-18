# turnlith-CLI (`tlc`)

A Claude Code-style coding agent for the terminal, backed by a
[turnlith](https://turnlith.com) server. You type in your project directory;
the model streams back an answer and runs local tools — reading and editing
files, running commands, searching the codebase — behind per-tool permission
prompts.

```
$ tlc
tlc v0.1.0 · sigma (gemini:gemini-3.8-flash) · ~/code/my-app
type /help for commands · /exit to quit
> why is the login test flaky?

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

Gated tools ask once per call: `enter` allows this one, `a` allows that tool
for the rest of the session, `n` denies, `x` never allows it this session.
`--yolo` skips prompting entirely. Files must be read before they can be
edited, so the model can't blind-overwrite work it has never seen.

## Install

Requires Go 1.24+.

```sh
git clone https://github.com/turnlith-AI/turnlith-CLI
cd turnlith-CLI
make install           # builds to ~/.local/bin/tlc
```

or run it without installing:

```sh
go run ./cmd/tlc
```

## Configure

`tlc` shares its connection file with the `tlith` setup CLI
(`~/.config/tlith/config.json`), so if you already ran `tlith configure` you
are done. Otherwise:

```sh
tlc configure --base-url http://turnlith.com --api-key sk-…
```

`tlc configure` verifies the server's health endpoint and your key before
saving. The api key needs a **agent role** (create keys in the turnlith web
console). Environment overrides `TLITH_BASE_URL` / `TLITH_API_KEY` win over
the file, for CI or throwaway runs.

## Usage

```sh
tlc                          # interactive session in the current directory
tlc "fix the failing test"   # start with an initial prompt
tlc -p "explain main.go"     # one-shot: answer on stdout, activity on stderr
tlc -c                       # continue the previous conversation here
tlc --agent sigma            # pick the agent (model) explicitly
tlc agents                   # list the workspace's agents
```

`--agent` matters: memory, transcripts, and billing are per agent. The
default agent is remembered in `~/.config/tlith/tlc.json`, overridable with
`TLC_AGENT`.

| Flag              | Meaning                                                     |
| ----------------- | ----------------------------------------------------------- |
| `-p, --print`     | non-interactive; assistant text on stdout, everything else on stderr |
| `-c, --continue`  | replay the previous local transcript for this directory     |
| `--agent <name>`  | agent codename (default: last used, or the only agent)      |
| `--yolo`          | run gated tools without prompting                            |
| `--verbose`       | show token usage after each turn                             |
| `--base-url`, `--api-key` | connection overrides for this run                   |

In the REPL: `/help`, `/clear`, `/agent [name]`, `/agents`, `/status`,
`/verbose`, `/yolo`, `/exit`. `Ctrl-C` interrupts the run in progress;
another `Ctrl-C` at an empty prompt quits.

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
internal/cli/       argument handling, REPL, print mode
internal/history/   local JSONL transcripts
internal/config/    shared connection file + tlc preferences
```

## License

MIT
