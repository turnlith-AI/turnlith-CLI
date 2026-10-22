#!/bin/sh
# tlc installer: downloads a prebuilt binary from GitHub releases.
#
#   curl -fsSL https://raw.githubusercontent.com/turnlith-AI/turnlith-CLI/main/install.sh | sh
#
# Environment:
#   TLC_VERSION      install a specific tag (default: the latest release)
#   TLC_INSTALL_DIR  destination directory (default: ~/.local/bin)
#   TLITH_API_KEY    when set, configure the connection after installing
#                    (passed via the environment, never the download URL)
#   TLITH_BASE_URL   server URL for that configuration (default:
#                    https://turnlith.com)
set -eu

REPO="turnlith-AI/turnlith-CLI"
BIN="tlc"
INSTALL_DIR="${TLC_INSTALL_DIR:-$HOME/.local/bin}"

say() { printf '%s\n' "$*"; }
die() { printf 'error: %s\n' "$*" >&2; exit 1; }

command -v curl >/dev/null 2>&1 || die "curl is required"
command -v tar >/dev/null 2>&1 || die "tar is required"

case "$(uname -s)" in
  Linux)  goos="linux" ;;
  Darwin) goos="darwin" ;;
  *) die "unsupported OS $(uname -s) — download a binary from https://github.com/$REPO/releases" ;;
esac
case "$(uname -m)" in
  x86_64 | amd64) goarch="amd64" ;;
  arm64 | aarch64) goarch="arm64" ;;
  *) die "unsupported architecture $(uname -m) — download a binary from https://github.com/$REPO/releases" ;;
esac

version="${TLC_VERSION:-}"
if [ -z "$version" ]; then
  version="$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" |
    grep '"tag_name"' | head -n 1 |
    sed -E 's/.*"tag_name": *"([^"]+)".*/\1/')"
  [ -n "$version" ] || die "could not find the latest release — set TLC_VERSION to pick one"
fi
vernum="${version#v}"

name="${BIN}_${vernum}_${goos}_${goarch}"
asset="$name.tar.gz"
base="https://github.com/$REPO/releases/download/$version"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

say "downloading $asset ($version)"
curl -fsSL "$base/$asset" -o "$tmp/$asset" || die "download failed: $base/$asset"

if curl -fsSL "$base/sha256sums.txt" -o "$tmp/sums.txt" 2>/dev/null &&
  grep -q " $asset\$" "$tmp/sums.txt"; then
  grep " $asset\$" "$tmp/sums.txt" > "$tmp/check.txt"
  if command -v sha256sum >/dev/null 2>&1; then
    (cd "$tmp" && sha256sum -c --quiet check.txt) || die "checksum verification failed"
  elif command -v shasum >/dev/null 2>&1; then
    (cd "$tmp" && shasum -a 256 -c check.txt >/dev/null) || die "checksum verification failed"
  else
    say "warning: no sha256 tool found, skipping checksum verification"
  fi
else
  say "warning: no checksums published for this release, skipping verification"
fi

tar -xzf "$tmp/$asset" -C "$tmp"

mkdir -p "$INSTALL_DIR"
install -m 0755 "$tmp/$name/$BIN" "$INSTALL_DIR/$BIN" 2>/dev/null ||
  { cp "$tmp/$name/$BIN" "$INSTALL_DIR/$BIN" && chmod 0755 "$INSTALL_DIR/$BIN"; }

say "installed $INSTALL_DIR/$BIN (version $vernum)"
case ":$PATH:" in
  *":$INSTALL_DIR:"*) ;;
  *)
    say ""
    say "$INSTALL_DIR is not on your PATH; add it with:"
    case "${SHELL:-}" in
      */zsh) say "  echo 'export PATH=\"$INSTALL_DIR:\$PATH\"' >> ~/.zshrc && source ~/.zshrc" ;;
      *) say "  echo 'export PATH=\"$INSTALL_DIR:\$PATH\"' >> ~/.bashrc && source ~/.bashrc" ;;
    esac
    ;;
esac

# One-paste setup: the caller holds the api key in the environment only —
# it never appears in a URL or this script's output.
if [ -n "${TLITH_API_KEY:-}" ]; then
  base="${TLITH_BASE_URL:-https://turnlith.com}"
  say ""
  say "configuring tlc for $base"
  if ! "$INSTALL_DIR/$BIN" configure --base-url "$base" --api-key "$TLITH_API_KEY"; then
    die "configure failed — run \`tlc configure --base-url $base --api-key <key>\` yourself"
  fi
  if command -v opencode >/dev/null 2>&1; then
    "$INSTALL_DIR/$BIN" install opencode ||
      die "opencode wiring failed — run \`tlc install opencode\` yourself"
  fi
  say "next: run \`tlc\`, or open opencode and pick an agent under \"turnlith\" in /models"
else
  say "next: tlc configure --base-url <server> --api-key sk-…"
fi

if command -v tlith >/dev/null 2>&1; then
  say "note: the old tlith helper CLI is now part of tlc — remove it with \`npm rm -g tlith\`"
fi
