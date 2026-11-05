# Changelog

All notable changes to `tlc`. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/);
versions follow [semantic versioning](https://semver.org/) (pre-1.0: minor versions may change behavior).

## [Unreleased]

`tlc` is now a **setup and control CLI** for a turnlith workspace. The built-in
terminal coding agent is gone; use your turnlith agents from opencode, Pi,
Continue, Cursor or Cline instead (`tlc install` wires them up). Because 0.2.0 is
the last release that includes the agent, the next release should be 0.3.0.

### Removed (breaking)
- **The coding agent**: the interactive session, `tlc "prompt"`, print mode (`-p`, `--output-format`, `--max-turns`), `-c` / `/resume`, the `Read`/`Write`/`Edit`/`Bash`/`Glob`/`Grep`/`SearchMemory`/`TodoWrite` tools, permission modes and rules (`--yolo`, `--permission-mode`, `--allowed-tools`, `--disallowed-tools`, `.tlc/settings.json`), project instruction files (`TLC.md`), slash commands, `@file` mentions, local transcripts and the default-agent preference (`--agent`, `TLC_AGENT`, `tlc.json`).
- Their leftovers on disk are no longer used and can be deleted: `~/.config/tlith/tlc.json`, `~/.local/share/tlith/tlc/`.
- Passing any of the removed flags now fails with an explanation (exit 2) rather than a generic "unknown flag".

### Changed
- `tlc` with no arguments prints help (it used to start a session). A word that is not a command is an error with a "did you mean" hint (it used to be sent as a prompt).
- `tlc install` with no client name wires **every detected client** (it used to look only for opencode), and `tlc uninstall` with no name removes the turnlith entries from every client that has them. A flag a client cannot honour (e.g. `--project` for Pi) skips that client with a note, or is a usage error when it is the only one named.
- `tlc install` always asks the server for the agents first — even for MCP-only clients — so a wrong URL or key is caught before any client config is written.
- `tlc doctor` now checks what `tlc` is for: connection, server, key, each agent's provider credentials, the connection file's permissions, and every detected coding client (installed or not). The terminal/shell/git/ripgrep/instructions checks are gone.
- Errors, warnings and retry notices go to stderr; stdout carries only results.
- Requests to `agents`, `configure` and `install` are now retried on 429/502/503 and refused connections (announced on stderr); `doctor` still fails fast.

### Added
- **More coding clients**: `tlc install` now wires the turnlith agents into **Pi**, **Continue**, **Cursor** and **Cline**, not just opencode. Pi and Continue get the agents as models; Cursor and Cline get the `turnlith-controller` MCP server and print the model settings they keep in the app. `tlc install cursor pi` takes several names (or `all`), `tlc install --list` shows what is detected and installed, `--if-detected` makes the call a quiet no-op when nothing is found, and the one-paste `install.sh` now wires every detected client.
- `tlc doctor`, `tlc completion bash|zsh|fish`, per-command help (`tlc agents --help`).
- `tlc agents create` checks that the model is available in the workspace and that the effort suits its provider before sending anything.
- CI: race detector, Windows/macOS/arm64 vet+build, `go mod tidy` check, Dependabot.

### Security
- Plain `http://` to a non-loopback server is refused unless `TLC_ALLOW_INSECURE=1`, in every command including when the URL comes from `--base-url`; `localhost.evil.com` no longer counts as localhost. `--api-key-stdin` for `tlc configure`; `--api-key` warns that it is visible in the process list.
- The connection file is replaced atomically and forced to mode 600, so an older world-readable `config.json` no longer keeps a new key readable by other users.
- `install.sh` pipes the key to `tlc configure` on stdin instead of a command-line argument.
- Text from the server (agent labels, error messages) has terminal control characters removed before it is printed.

### Fixed
- `tlc install cursor` / `cline` printed "no agents on the server yet" in the model steps even when there were agents.
- `tlc configure` exited silently when the key prompt failed; it now says what is wrong, and refuses to wait for input when there is no terminal.
- `/dev/null` is no longer mistaken for a terminal.

## [0.2.0] - 2026-10-22
### Added
- `tlc install opencode` / `uninstall`: wires the turnlith agents into opencode (JSONC-aware, comment-preserving); the retired `tlith` helper merges into `tlc`.
- Permission modes (`normal`, `auto`, `yolo`) with `/permissions`, Tab cycling and a mode-aware prompt.
- Agent creation (`tlc agents create`), model/effort switching, REPL completions, `@file` mentions and the `/files` browser.

## [0.1.0] - 2026-10-18
### Added
- First release: a terminal coding agent backed by a turnlith server — streaming answers, local `Read`/`Write`/`Edit`/`Bash`/`Glob`/`Grep` tools behind permission prompts, server-side `SearchMemory`, local transcripts.
- Prebuilt binaries for Linux, macOS and Windows with an installer script; the config path matches the `tlith` CLI's.

[Unreleased]: https://github.com/turnlith-AI/turnlith-CLI/compare/v0.2.0...HEAD
[0.2.0]: https://github.com/turnlith-AI/turnlith-CLI/releases/tag/v0.2.0
[0.1.0]: https://github.com/turnlith-AI/turnlith-CLI/releases/tag/v0.1.0
