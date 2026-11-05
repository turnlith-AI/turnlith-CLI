# tlc

**Connect your [turnlith](https://turnlith.com) workspace to the coding tools you already use.**

`tlc` saves your connection, manages your agents, and wires them into opencode,
Pi, Continue, Cursor and Cline in one command — then checks that it all works.

```
$ tlc install
opencode config: ~/.config/opencode/opencode.json (updated)
provider: tlith → https://turnlith.com/v1
mcp server: turnlith-controller → https://turnlith.com/v1/mcp (memory search tools)
agents: 2
  - sigma (Sigma)
  - bug-hunter (Bug hunter)
```

## Why tlc

- **One command per client.** `tlc install` detects what you have and writes the right config for each — agents as models, plus the `turnlith-controller` MCP server for memory search.
- **Edits your configs politely.** JSON configs are changed in place: comments, formatting and your other entries stay untouched. `tlc uninstall` removes exactly what was added, and nothing else.
- **Safe with your key.** The key is stored mode 600, written atomically, masked in output, and only ever sent over `https://` (or to localhost).
- **Self-diagnosing.** `tlc doctor` checks the connection, the key, each agent's provider and every coding client, with a fix for each problem.
- **Scriptable.** Plain output, stdout for data and stderr for errors, meaningful exit codes, shell completions.

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/turnlith-AI/turnlith-CLI/main/install.sh | sh
```

Supports Linux and macOS (amd64/arm64). Windows builds are on the
[releases page](https://github.com/turnlith-AI/turnlith-CLI/releases). With Go 1.26+:
`go install github.com/turnlith-AI/turnlith-CLI/cmd/tlc@latest`.

## Get started

```sh
tlc configure --base-url https://turnlith.com   # asks for your API key, verifies it, saves it
tlc agents                                      # what your workspace has
tlc install                                     # wire the agents into every client it detects
tlc doctor                                      # check everything
```

Create an API key in the turnlith web console. Listing agents and `install` work
with any key; `tlc agents create` needs an admin-role key.

## Commands

| Command | What it does |
| --- | --- |
| `tlc configure` | Save the server URL and API key after verifying them. |
| `tlc agents` | List the workspace's agents. |
| `tlc agents create` | Create an agent (asks for what you leave out on a terminal). |
| `tlc install [client…]` | Wire the agents into coding clients; no name = every detected client. |
| `tlc uninstall [client…]` | Remove the turnlith entries from those clients. |
| `tlc doctor` | Diagnose setup problems. |
| `tlc completion bash\|zsh\|fish` | Print a shell completion script. |

`tlc help <command>` shows the options of one command.

## Upgrading from 0.2

`tlc` no longer includes the built-in terminal coding agent (the interactive
session, `-p`, permission modes, and the file and shell tools). Use one of the
clients above with your turnlith agents instead — `tlc install` sets that up. See
the [changelog](CHANGELOG.md).

## Documentation

- [Reference](docs/reference.md) — configuration, commands, flags, supported clients
- [Architecture](docs/architecture.md) — how it is built
- [Contributing](CONTRIBUTING.md) — build, test, and extend `tlc`
- [Security](SECURITY.md) — protections and how to report a vulnerability
- [Changelog](CHANGELOG.md) — what's new in each release

## License

MIT — see [LICENSE](LICENSE).
