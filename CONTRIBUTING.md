# Contributing

Thanks for helping. `tlc` is a small Go program (~11k lines plus tests, four direct
dependencies), and we want it to stay one: a capable agent
without bloat. A change that adds a feature should also say what it costs in
context, complexity or dependencies.

## Setup

Go 1.26+.

```sh
git clone https://github.com/turnlith-AI/turnlith-CLI && cd turnlith-CLI
make check        # gofmt, vet, race tests, cross-compile vet — what CI runs
make build        # bin/tlc
```

Everything runs offline: the tests drive the real `cli.Main` against an in-process
fake of the turnlith server (`internal/cli/cli_test.go`). You never need a server
or a key to develop.

## How the code is organised

See [docs/architecture.md](docs/architecture.md). The short version: `internal/cli`
wires things, `internal/agent` runs the tool loop, `internal/tools` are the tools,
`internal/permissions` decides what may run, `internal/ui` draws, `internal/api`
talks to the server.

## Changing things

**Write the failing test first.** Reproduce a bug as a test, watch it fail, fix it.
For a feature, the test is the specification. The suite has no network or terminal
requirements; keep it that way.

Where things go:

- **A new tool** — implement `tools.Tool` in `internal/tools`; add `ConcurrencySafe()`
  if it is read-only and stateless, `NeedsPermission` (`tools.ArgGater`) if it
  touches paths, `Prepare`/`RunResult` if it should preview or show a diff;
  register it in `newApp`; teach `ui.ToolCallSummary` and `ui.summarizeResult`
  how to show it; add it to the README tool table.
- **A slash command** — add the entry to `ui.Commands` (the menu, `/help` and TAB
  completion) *and* the handler to `slashHandlers`; a test fails if they differ.
- **A flag** — add it to `boolFlags`/`valueFlags`, to `helpText` and to `flagHelp`;
  tests fail if the help or the shell completions miss it.
- **Permissions** — rules and modes live in `internal/permissions`. Anything that
  could widen what runs without asking needs a test showing it cannot be abused
  (chained commands, substitution, symlinks, paths outside the workspace).
- **Output** — keep stdout machine-readable in `-p` mode; everything human goes to
  stderr there. The streaming renderer must stay chunk-invariant (there is a fuzz
  test: `go test ./internal/ui -fuzz FuzzStylerChunkInvariance`).

Conventions: standard `gofmt`; errors say what to do next; comments explain *why*;
no new dependency without a discussion — the binary is built with `CGO_ENABLED=0`
for six platforms and must stay buildable that way (`make cross`).

## Trying the interface

Unit tests cannot see a terminal. For UI work, run the real binary against a
scripted fake server in a pseudo-terminal and look at the output; the test fake
in `internal/cli/cli_test.go` shows the wire format to emulate. Remember readline
asks the terminal for the cursor position (`ESC[6n`) and waits for the reply.

## Pull requests

- One logical change per PR, with its tests and docs (README, `CHANGELOG.md`
  under *Unreleased*, help text).
- `make check` must pass; CI also vets Windows and macOS builds.
- Describe what you saw (before/after for UI changes), not just what you changed.

## Releasing (maintainers)

Bump `internal/version/version.go`, move the *Unreleased* changelog section under
the new version, tag `vX.Y.Z`. The release workflow refuses a tag that does not
match `version.go`, builds six binaries, checksums them and publishes.
