# Security

## Reporting a vulnerability

Please report security problems privately, not in a public issue: use GitHub's
**"Report a vulnerability"** button on the repository's Security tab. If that is
unavailable, open an issue that says only that you have a security report and
asks for a private channel — do not include details.

We aim to acknowledge a report within a few days and to ship a fix, with credit if
you want it, before details are made public.

## What `tlc` is — and is not

`tlc` lets a language model read files, edit files and run commands on your
machine, with you approving the consequential steps. That is a powerful capability
with a real risk: the model can be steered by text it reads (a file in the repo, a
tool result, a web page it was shown) — **prompt injection**. `tlc` reduces what
that can do; it does not make it safe to approve things without reading them.
It is **not a sandbox**.

### What it enforces

| Protection | Where |
| --- | --- |
| Gated tools (`Bash`, `Write`, `Edit`) ask first, per the permission mode; `yolo` is never entered by accident (Tab cannot reach it) | `internal/permissions` |
| File tools ask before leaving the working directory (after symlink resolution); `auto` mode does not approve such edits | `internal/tools`, `internal/agent` |
| Credential stores (`~/.ssh`, `~/.aws`, `~/.gnupg`, `~/.kube`, cloud/registry tokens, tlc's own config holding the api key) are refused by the file tools in **every** mode | `internal/tools/util.go` |
| Allow rules never cover command substitution or redirection; a chained command must be allowed segment by segment | `internal/permissions/rules.go` |
| A repository can *deny* tools via `.tlc/settings.json` but can never *grant* itself permissions | `internal/config/project.go` |
| Deny rules beat every mode, `yolo` included | `internal/permissions` |
| The api key only travels over `https://` (loopback excepted, or `TLC_ALLOW_INSECURE=1`); the config file is mode 600; the key is masked in output | `internal/config` |
| Edits are refused if the file changed since it was read, and written atomically | `internal/tools` |

### What it does not do

- **`Bash` can do anything you approve.** The credential blocklist covers the file
  tools; a command you approve (`cat ~/.ssh/id_rsa`, `curl … | sh`) is not
  filtered. Read what a prompt asks to run. Prefix rules such as `Bash(go test:*)`
  are the safe way to stop being asked about commands you trust.
- The server sees your prompts, file contents the model reads, and tool results
  — that is how it works. Do not point `tlc` at a server you do not trust.
- Instruction files (`TLC.md`, `AGENTS.md`, `CLAUDE.md`) are repository content
  loaded into the model's prompt. They are labelled as repository guidance and sit
  below the safety rules, but a hostile repository can still try to steer the model:
  review unfamiliar repositories before running `tlc` with `auto` or `yolo`.
- There is no network sandbox and no resource limit beyond command timeouts.

## Supported versions

Security fixes go into the latest release.
