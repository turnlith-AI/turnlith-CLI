# tlc

**An AI coding agent for your terminal, powered by [turnlith](https://turnlith.com).**

Describe what you want in plain language. `tlc` reads your code, makes the
changes, and runs your tests — asking before it touches anything.

```
$ tlc
tlc v0.2.0 · sigma (gemini:gemini-3.8-flash) · ~/code/my-app
❯ why is the login test flaky?

⏺ Read(internal/auth/login_test.go)
Looking at the test, the flake comes from a shared clock …
```

## Why tlc

- **Remembers your project.** The agent's memory lives on the turnlith server, so it picks up where you left off — even on another machine.
- **Your code stays local.** Files are read and edited on your computer; your filesystem is never uploaded.
- **You stay in control.** Edits and commands need your approval, with a clear diff first. Plan mode keeps the agent read-only while it explores.
- **Your choice of model.** Use any agent in your turnlith workspace and switch models mid-session.
- **Built for automation.** One-shot mode with JSON output fits scripts, pipes, and CI.
- **Works with opencode.** Use your turnlith agents there too with `tlc install opencode`.

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/turnlith-AI/turnlith-CLI/main/install.sh | sh
```

Supports Linux and macOS (amd64/arm64). Windows builds are on the
[releases page](https://github.com/turnlith-AI/turnlith-CLI/releases). With Go 1.26+:
`go install github.com/turnlith-AI/turnlith-CLI/cmd/tlc@latest`.

## Get started

```sh
tlc configure --base-url https://turnlith.com   # enter your API key when prompted
cd your-project
tlc
```

Create an API key in the turnlith web console. Type `/help` in a session to see
what you can do.

## Everyday use

```sh
tlc                          # interactive session in the current directory
tlc "fix the failing test"   # start with a task
tlc -p "explain main.go"     # one-shot answer for scripts and pipes
tlc -c                       # continue your last conversation here
tlc agents                   # list your agents
tlc doctor                   # diagnose setup problems
```

## Safe by default

`tlc` asks before writing files or running commands. Choose how much freedom to
give it with four permission modes — `normal`, `auto`, `plan` and `yolo` —
and set per-project rules in `.tlc/settings.json` to block actions entirely.
Credential folders such as `~/.ssh` and `~/.aws` are always off limits to its
file tools. See [SECURITY.md](SECURITY.md) for details.

## Documentation

- [Reference](docs/reference.md) — permissions, configuration, flags, commands, opencode
- [Architecture](docs/architecture.md) — how it works under the hood
- [Contributing](CONTRIBUTING.md) — build, test, and extend `tlc`
- [Security](SECURITY.md) — protections and how to report a vulnerability
- [Changelog](CHANGELOG.md) — what's new in each release

## License

MIT — see [LICENSE](LICENSE).
