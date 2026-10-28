# Architecture

`tlc` is a thin client. The turnlith server owns the conversation's memory and
context; `tlc` owns the terminal, the tools, and the decision of what is allowed
to run. This page is the map.

```
 you ──▶ internal/ui ──▶ internal/cli ──▶ internal/agent ──▶ internal/api ──▶ turnlith server
          prompt, render    wiring, REPL,    tool loop,          SSE chat,         (context, memory,
          readline          print mode,      system prompt       MCP search        model routing)
                            slash commands        │
                                                  ├──▶ internal/permissions   may this run?
                                                  └──▶ internal/tools         do it, locally
```

## One prompt, end to end

1. `cli.Main` parses arguments (`args.go`: two flag tables, positional commands)
   and builds the `app` (`app.go`): API client, tools, permission policy, project
   root, instruction files, saved rules.
2. The REPL (`interactive.go`) or print mode (`print.go`) hands the text to
   `app.runPrompt` (`run.go`), which wires UI callbacks (`agent.Events`) and
   calls `agent.Runner.Run`.
3. `Runner.Run` sends `[system prompt, task history]` to `POST /v1/chat/completions`
   (`api.Client.StreamChat`) and streams the reply. **Only the current task's wire
   history is sent**: the fresh prompt plus any tool calls and results so far. The
   server assembles everything older from the agent's session memory.
4. If the model asked for tools, `executeCalls` runs them (below), appends the
   results to the task history and loops, up to 40 turns (`--max-turns`).
5. When a turn ends with no tool calls the run is done; `runPrompt` prints the cost
   line, and print mode maps the outcome to an exit status.

### Running a tool call (`agent/loop.go`)

```
 call ─▶ known tool? ─▶ Prepare(args)        validate + preview (diff); an error is the
                         │                    result — the user is never asked about a call
                         │                    that cannot work
                         ▼
                  ArgGater.NeedsPermission    outside the workspace? → "external" request
                         │
                         ▼
          gated or external? ─ no ─────────────────────────────────┐
                 │ yes                                              │
                 ▼                                                  │
        Policy.Approve(request)                                     │
          1. deny rules ............... always win (even yolo)      │
          2. plan mode ................ read-only builtins only     │
          3. yolo ..................... allow                       │
          4. allow rules + builtins ... allow                       │
          5. auto mode + inside edit .. allow                       │
          6. ask the user (one key) ...                             │
                 │ approved                                         │
                 ▼                                                  ▼
             RunResult(args) ─▶ Result{Text → model, Display → user}
```

Adjacent calls that are `tools.Concurrent`, ungated and inside the workspace run
in parallel (≤4); results are returned to the model and shown to the user in the
order asked. Anything else runs one at a time.

Credential paths never reach `Approve`: `tools.resolvePath` refuses them in every
mode, so even `yolo` cannot read `~/.ssh`.

## Packages

| Package | Responsibility |
| --- | --- |
| `cmd/tlc` | `main`: `os.Exit(cli.Main(os.Args[1:]))` |
| `internal/cli` | argument parsing, `app` wiring, REPL loop, print mode (+ JSON output), slash commands (registry in `slash.go`), `doctor`, `completion`, `configure`, `install`, `/resume`, `/undo`, `/diff` |
| `internal/agent` | `Runner` (the tool loop), system prompt, project-instruction loading |
| `internal/api` | HTTP client: Bearer auth, SSE streaming with retry/idle/truncation handling, `/v1/models`, `/v1/agents`, minimal MCP client for `search_memory` |
| `internal/tools` | `Read` `Write` `Edit` `Bash` `Glob` `Grep` `SearchMemory` `TodoWrite`; the file `Tracker` (read-before-edit, staleness, undo history); the line diff |
| `internal/permissions` | `Policy`, modes (normal/auto/plan/yolo), rules (`Bash(go test:*)`), command splitting |
| `internal/ui` | theme, streaming markdown `Styler`, `Renderer` (transcript, diffs, prompts), readline REPL, one-key prompts, `/files` browser, command table |
| `internal/config` | connection file, per-project saved rules, `.tlc/settings.json` (deny only), URL validation |
| `internal/history` | local JSONL transcripts for `-c` and `/resume` (display only; real memory is server-side) |
| `internal/harness` | opencode config merge (JSONC-aware) |

## Where state lives

| What | Where |
| --- | --- |
| Conversation memory, context assembly | the server (per agent) |
| Connection (URL, key) | `~/.config/tlith/config.json` (mode 600) or `TLITH_BASE_URL`/`TLITH_API_KEY` |
| Default agent, per-project always-allow rules | `~/.config/tlith/tlc.json` |
| Personal instructions | `~/.config/tlith/TLC.md` |
| Project instructions | `TLC.md` / `AGENTS.md` / `CLAUDE.md` in the repo |
| Repo-wide deny rules | `.tlc/settings.json` (allow rules in it are ignored) |
| Transcripts | `~/.local/share/tlith/tlc/sessions/*.jsonl` |
| Input history | `~/.local/share/tlith/tlc/history` |
| Undo snapshots, todo list, permission answers | memory, this session only |

## Invariants worth protecting

- **stdout is data in `-p` mode.** Human output goes to stderr there; with
  `--output-format json|stream-json` stdout carries only JSON.
- **The model never sees more than it should, the user never sees less than they
  need.** `Result.Text` is what the model is told; `Result.Display` is what the
  person sees. They differ on purpose (diffs).
- **Permission widening needs a test.** Rules, modes and the workspace boundary have
  adversarial tests (chains, substitution, symlinks, `..`); keep adding to them.
- **The renderer is chunk-invariant.** However the stream is split into deltas, the
  output is byte-identical (property + fuzz test).
- **Tables over switches** where drift is a risk: flags (`boolFlags`/`valueFlags`
  ↔ help ↔ completions), slash commands (`ui.Commands` ↔ `slashHandlers`),
  tool names in permission rules.
- **Nothing is re-sent once output has started.** Only requests the server provably
  did not process (429, 502, 503, refused connection) are retried.

## Known limits

- One conversation per agent: see [server-session-scoping.md](server-session-scoping.md).
- `Bash` is not sandboxed; see [SECURITY.md](../SECURITY.md).
- zsh and fish completion scripts are generated from the same tables as bash but
  only the bash script is syntax-checked in CI.
