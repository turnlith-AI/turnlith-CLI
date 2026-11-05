# Architecture

`tlc` is a small client for a turnlith server's control plane. It does not talk
to a model; it stores the connection, manages agents, and writes the
configuration that lets your coding clients talk to those agents. This page is
the map.

```
 you ──▶ internal/cli ──▶ internal/api ──▶ turnlith server
          parse, dispatch   HTTP client:     /health, /v1/models,
          configure,        auth, retry,     /v1/agents
          agents, doctor,   errors
          install, ...
              │
              ├──▶ internal/harness   one registry entry per coding client:
              │                       where its config lives, what to write,
              │                       how to take it out again
              ├──▶ internal/config    the saved connection, URL safety rules
              └──▶ internal/ui        colors, line output, sanitizing server
                                      text, readline prompts
```

## One command, end to end

1. `cli.Main` parses arguments (`args.go`: two flag tables, the command word,
   per-command positionals). Usage errors exit `2`; removed coding-agent flags get
   their own explanation.
2. Commands that only touch the machine (`configure`'s prompts, `install --list`,
   `uninstall`, `completion`, `help`) do their work directly. Commands that talk
   to the server resolve the connection (`resolveConnection`: saved file or
   environment, `--base-url`/`--api-key` overrides, URL safety check) and build a
   client with `newClient`, which announces retries on stderr.
3. `agents create` is a wizard over `api.Client.ListModels` / `CreateAgent`;
   `install` lists the agents and hands them to each selected `harness.Client`.
4. Results go to stdout through `ui.Renderer`; errors go to stderr after
   `friendlyErr` has translated them and removed control characters.

## Packages

| Package | Responsibility |
| --- | --- |
| `cmd/tlc` | `main`: `os.Exit(cli.Main(os.Args[1:]))` |
| `internal/cli` | argument parsing, the commands (`configure`, `agents`, `install`/`uninstall`, `doctor`, `completion`, help topics), error translation |
| `internal/api` | HTTP client: Bearer auth, retry of requests the server provably did not process, `/health`, `/v1/models`, `POST /v1/agents` |
| `internal/harness` | the coding-client registry (opencode, Pi, Continue, Cursor, Cline): per-client config paths and blocks, JSONC-aware merge, uninstall, detection |
| `internal/config` | the connection file (atomic, mode 600), environment overrides, server URL validation |
| `internal/ui` | theme (colors, `NO_COLOR`), line renderer, `SanitizeInline/Block`, readline prompter |
| `internal/version` | the version string the release workflow checks against the tag |

## Where state lives

| What | Where |
| --- | --- |
| Agents, models, memory | the server |
| Connection (URL, key) | `~/.config/tlith/config.json` (mode 600) or `TLITH_BASE_URL`/`TLITH_API_KEY` |
| What `install` wrote | each client's own config (see the reference); `tlc uninstall` removes exactly that |

`tlc` keeps no other files: no history, cache or logs.

## Invariants worth protecting

- **stdout is data.** Errors, warnings and retry notices go to stderr.
- **The key never leaves over plain http** (loopback excepted, or the explicit
  `TLC_ALLOW_INSECURE=1`), for every command, including when the URL comes from
  `--base-url`.
- **Nothing is written before it is verified.** `configure` saves only after a
  health check and an authenticated call succeed; `install` lists the agents
  before touching any client config.
- **Client configs are edited in place and removed precisely.** Comments and
  other entries survive an install; uninstall removes only the entries tlc added,
  and tlc-owned files are marked and never overwrite files tlc did not write.
- **Server text is untrusted.** Anything from the server that reaches the screen
  passes through `ui.Sanitize*`.
- **Tables over switches** where drift is a risk: flags (`boolFlags`/`valueFlags`
  ↔ help ↔ completions) and commands (`commands` ↔ `topCommands`) are checked by
  tests.
- **Nothing is retried once it may have been processed.** Only 429, 502, 503 and
  refused connections are retried; 504 and other errors are not.

## Known limits

- zsh and fish completion scripts are generated from the same tables as bash but
  only the bash script is syntax-checked in CI.
- Cursor and Cline keep model settings in app state; tlc can only print the steps.
