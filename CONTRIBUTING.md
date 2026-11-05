# Contributing

Thanks for helping. `tlc` is a small Go program (two direct dependencies) that
connects a turnlith workspace to the coding tools people already use, and we want
it to stay small and dependable. A change that adds a feature should also say what
it costs in complexity or dependencies.

## Setup

Go 1.26+.

```sh
git clone https://github.com/turnlith-AI/turnlith-CLI && cd turnlith-CLI
make check        # gofmt, vet, race tests, cross-compile vet — what CI runs
make build        # bin/tlc
```

Everything runs offline: the tests drive the real `cli.Main` against an in-process
fake of the turnlith server (`internal/cli/cli_test.go`) and temp-dir homes for
the coding clients. You never need a server or a key to develop.

## How the code is organised

See [docs/architecture.md](docs/architecture.md). The short version: `internal/cli`
parses arguments and runs the commands, `internal/api` talks to the server,
`internal/harness` knows how to configure each coding client, `internal/config`
owns the saved connection, `internal/ui` draws.

## Changing things

**Write the failing test first.** Reproduce a bug as a test, watch it fail, fix it.
For a feature, the test is the specification. The suite has no network or terminal
requirements; keep it that way.

Where things go:

- **A new coding client** — add one entry to the registry in `internal/harness`
  (`Registry()`): its config paths, `Detect`, `Install`, `Uninstall`, `Inspect`,
  and `ManualSteps` for anything it keeps in app state. Cover install, re-install
  (byte-stable), uninstall (removes exactly what was added) and a pre-existing
  config with comments and other entries. Then check `docs/reference.md`, the
  console's client list and the docs site mention it.
- **A command or flag** — add it to `commands` and `topCommands`, or to
  `boolFlags`/`valueFlags`, then to `helpText` and `flagHelp`; tests fail if the
  help or the shell completions miss it.
- **Anything that writes a file** — write atomically, use the least permissive mode
  that works, and never overwrite something tlc did not create. Add a test with a
  pre-existing file.
- **Anything that prints server text** — pass it through `ui.SanitizeInline` or
  `ui.SanitizeBlock`.
- **Output** — stdout is data, stderr is for errors, warnings and notices.

Conventions: standard `gofmt`; errors say what to do next; comments explain *why*;
no new dependency without a discussion — the binary is built with `CGO_ENABLED=0`
for six platforms and must stay buildable that way (`make cross`).

## Trying the interface

Unit tests cannot see a terminal. For the interactive prompts (`configure`,
`agents create`), run the real binary against a scripted fake server in a
pseudo-terminal and look at the output; the test fake in
`internal/cli/cli_test.go` shows the wire format to emulate. Remember readline
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
