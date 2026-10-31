# Changelog

All notable changes to `tlc`. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/);
versions follow [semantic versioning](https://semver.org/) (pre-1.0: minor versions may change behavior).

## [Unreleased]

## [0.3.0] - 2026-10-31

### Security
- Client configs `tlc install` writes (they hold the api key) and tlc's own connection file are now owner-only (`0600`) even when they already existed with wider permissions.
- `install.sh` refuses to install when the release's checksums are missing or no sha256 tool is available, instead of warning and continuing.
- File tools ask before touching anything outside the working directory (judged after symlink resolution), and `auto` mode no longer waves such edits through.
- Credential stores (`~/.ssh`, `~/.aws`, `~/.gnupg`, `~/.kube`, cloud/registry tokens and tlc's own `~/.config/tlith`, which holds the api key) are refused by the file tools in every mode, `yolo` included.
- Plain `http://` to a non-loopback server is refused unless `TLC_ALLOW_INSECURE=1`; `localhost.evil.com` no longer counts as localhost. `--api-key-stdin` for `tlc configure`; `--api-key` warns that it is visible in the process list.
- Tab on an empty prompt can no longer reach `yolo`.
- A repository's `.tlc/settings.json` can add deny rules but never allow rules.
- `/status` masks short api keys too.

### Added
- **More coding clients**: `tlc install` now wires the turnlith agents into **Pi**, **Continue**, **Cursor** and **Cline**, not just opencode. Pi and Continue get the agents as models; Cursor and Cline get the `turnlith-controller` MCP server and print the model settings they keep in the app. `tlc install cursor pi` takes several names (or `all`), `tlc install --list` shows what is detected and installed, `--if-detected` makes the call a quiet no-op when nothing is found, and the one-paste `install.sh` now wires every detected client.
- **Permission rules**: `Bash(go test:*)`, `Edit(docs/**)`, … via `--allowed-tools` / `--disallowed-tools`, `p` ("always in this project", stored in the user's config) and the repo's deny list. An "always" answer now remembers the command prefix, not all of Bash. Chained commands must match segment by segment; substitution and redirection are never covered by an allow rule. Deny rules win over everything, `--yolo` included. Read-only builtins (`git status|diff|log|show`, `ls`, `pwd`, …) no longer prompt.
- **Project instructions**: `TLC.md` / `AGENTS.md` / `CLAUDE.md` (plus `~/.config/tlith/TLC.md`) are loaded into the system prompt, capped and announced; `/init` has the agent write `TLC.md`.
- **Plan mode** (`--permission-mode plan`): read-only; refusals explain themselves to the model.
- `TodoWrite` checklist tool; adjacent read-only tool calls run in parallel.
- Numbered, colored **diffs** for Edit/Write in the transcript and in the permission prompt; an edit that cannot apply returns to the model without prompting.
- `/undo`, `/diff`, `/copy`, `/resume`, `/edit`, `/doctor`; `\` line continuation; denial reasons passed to the model; a cost line after every prompt.
- `tlc doctor`, `tlc completion bash|zsh|fish`, per-command help (`tlc agents --help`).
- Print mode: `--output-format json|stream-json`, `--max-turns`, `--permission-mode`; exit status `0`/`1`/`130`; piped stdin is appended to the prompt.
- Streaming markdown: headings, bullets, block quotes.
- CI: race detector, Windows/macOS/arm64 vet+build, `go mod tidy` check, Dependabot.

### Changed
- `tlc configure` and `install.sh` default to the hosted service at `https://dash.turnlith.com` (was `http://localhost:8080` / `https://turnlith.com`).
- `tlc install` with no client name now wires **every detected client** (it used to look only for opencode), and `tlc uninstall` with no name removes the turnlith entries from every client that has them (it used to assume opencode). A flag a client cannot honour (e.g. `--project` for Pi) skips that client with a note, or is a usage error when it is the only one named.
- `-p` exits `1` when the run fails (it always exited `0`).
- Tool results are summarized in the transcript ("Read 120 lines"); long `Bash` output keeps its head *and* tail.
- `-c` prefers the latest session with the current agent.
- `cli.go` split into focused files; slash commands dispatch through a registry tied to the menu by a test; flag parsing is table-driven.

### Fixed
- The spinner ticked every 100 *nanoseconds* (≈200k terminal writes per 350 ms); it now ticks at 10 Hz and also covers waits between turns and slow tools.
- Edit failed on every multi-line match in CRLF files; CRLF is now preserved by Edit and Write.
- Edit/Write overwrote files that changed on disk after being read; files are now written atomically, keeping mode and symlinks.
- A stream cut off mid-answer counted as success; it is now an error. Requests the server rejected before doing work (429, 502, 503, refused connections) are retried with backoff; a silent stream is abandoned after 5 minutes (`TLC_STREAM_IDLE_TIMEOUT`).
- A leading backtick at the start of a line did not open an inline code span.
- Tool timings included the time spent waiting at a permission prompt.
- Sessions started in the same second shared a transcript file.
- Startup announced "agent switched" for the initial choice.

## [0.2.0] - 2026-10-22
### Added
- `tlc install opencode` / `uninstall`: wires the turnlith agents into opencode (JSONC-aware, comment-preserving); the retired `tlith` helper merges into `tlc`.
- Permission modes (`normal`, `auto`, `yolo`) with `/permissions`, Tab cycling and a mode-aware prompt.
- Agent creation (`tlc agents create`), model/effort switching, REPL completions, `@file` mentions and the `/files` browser.

## [0.1.0] - 2026-10-18
### Added
- First release: a terminal coding agent backed by a turnlith server — streaming answers, local `Read`/`Write`/`Edit`/`Bash`/`Glob`/`Grep` tools behind permission prompts, server-side `SearchMemory`, local transcripts.
- Prebuilt binaries for Linux, macOS and Windows with an installer script; the config path matches the `tlith` CLI's.

[Unreleased]: https://github.com/turnlith-AI/turnlith-CLI/compare/v0.3.0...HEAD
[0.3.0]: https://github.com/turnlith-AI/turnlith-CLI/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/turnlith-AI/turnlith-CLI/releases/tag/v0.2.0
[0.1.0]: https://github.com/turnlith-AI/turnlith-CLI/releases/tag/v0.1.0
