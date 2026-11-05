# Security

## Reporting a vulnerability

Please report security problems privately, not in a public issue: use GitHub's
**"Report a vulnerability"** button on the repository's Security tab. If that is
unavailable, open an issue that says only that you have a security report and
asks for a private channel — do not include details.

We aim to acknowledge a report within a few days and to ship a fix, with credit if
you want it, before details are made public.

## What `tlc` is — and is not

`tlc` holds your turnlith API key and writes configuration files for other
programs (opencode, Pi, Continue, Cursor, Cline). It does not run models, read
your code, execute commands on a model's behalf or open a listening socket. Its
risks are therefore about **protecting the key**, **not damaging configs it
does not own**, and **not trusting the server's text**.

### What it enforces

| Protection | Where |
| --- | --- |
| The API key only travels over `https://` (loopback excepted, or the explicit `TLC_ALLOW_INSECURE=1`); `localhost.evil.com` does not count as localhost; the rule applies to `--base-url` too, in every command | `internal/config` |
| The connection file is written mode 600, atomically (temp file + rename), and an older, looser file is tightened rather than reused | `internal/config` |
| A key or URL is saved only after the server's health check and an authenticated call succeed; `install` lists the agents before writing any client config, so a mistyped key is never spread into other tools | `internal/cli` |
| `--api-key-stdin` and the environment keep the key out of the process list; `--api-key` warns that it is visible; the installer pipes the key on stdin; the key is masked wherever it is shown | `internal/cli`, `install.sh` |
| Client JSON configs are edited in place (comments and other entries preserved); uninstall removes only the entries tlc added; files tlc owns (Continue) carry a marker, are never overwritten if tlc did not write them, and are only deleted if marked | `internal/harness` |
| Text from the server (agent labels, error messages) has terminal control characters removed before display, so a hostile server cannot rewrite the screen or set the window title | `internal/ui` (`Sanitize*`), `internal/cli/errors.go` |
| Requests are retried only when the server provably did not process them (429, 502, 503, refused connection) | `internal/api` |
| Error output never echoes credentials or URL userinfo | `internal/config` |

### What it does not do

- **Client configs hold your key.** Unless you pass `--use-env`, `tlc install`
  writes the API key into each client's config file (Cline always, since it has
  no environment reference), so those files are as sensitive as the key itself:
  keep them out of version control (`--project` configs especially) and use
  `--use-env` where the client supports it. Use an **agent-role** key for
  clients; keep admin keys for `tlc agents create`.
- tlc cannot protect a key from other software already running as you, or from
  a client that logs its own configuration.
- `TLC_ALLOW_INSECURE=1` sends the key in clear text; use it only on networks you
  trust.
- The server sees everything your clients send it. Do not point `tlc` at a server
  you do not trust.
- `tlc` does not verify the server's identity beyond normal TLS certificate
  validation.

## Supported versions

Security fixes go into the latest release.
