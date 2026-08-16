#!/usr/bin/env bash
# install.sh — download the antiaimark prebuilt binaries from the latest
# GitHub release and install them into ANTIAIMARK_INSTALL_DIR (default
# $HOME/.local/bin), then print the registration commands for every AI IDE.
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/n0bele/AntiAIMark/main/scripts/install.sh | bash
#   # or clone it:  ./scripts/install.sh [version]
#   # pick a specific release:  ANTIAIMARK_VERSION=1.2.3 ./scripts/install.sh
#
# Windows (Git Bash / MSYS / Cygwin): use install.ps1 instead.
set -euo pipefail

REPO="n0bele/AntiAIMark"
VERSION="${ANTIAIMARK_VERSION:-latest}"
DEST="${ANTIAIMARK_INSTALL_DIR:-$HOME/.local/bin}"

log() { printf '\033[1;32m==>\033[0m %s\n' "$*"; }
die() { printf '\033[1;31merror:\033[0m %s\n' "$*" >&2; exit 1; }

require_cmd() { command -v "$1" >/dev/null 2>&1 || die "required tool not found: $1"; }
require_cmd curl

# -- detect OS/arch ----------------------------------------------------------
case "$(uname -s)" in
  Linux*) os=linux ;;
  Darwin*) os=darwin ;;
  MINGW*|MSYS*|CYGWIN*)
    die "Git Bash / MSYS detected. On Windows please run install.ps1 instead:
  powershell -ExecutionPolicy Bypass -Command \"irm https://raw.githubusercontent.com/$REPO/main/scripts/install.ps1 | iex\"" ;;
  *) die "unsupported OS: $(uname -s)" ;;
esac

case "$(uname -m)" in
  x86_64|amd64) arch=amd64 ;;
  aarch64|arm64) arch=arm64 ;;
  i386|i686|x86) arch=386 ;;
  *) die "unsupported architecture: $(uname -m)" ;;
esac

case "$os-$arch" in
  linux-*|darwin-*) ;;
  *) die "unsupported platform: $os/$arch" ;;
esac

# -- download ----------------------------------------------------------------
if [ "$VERSION" = "latest" ]; then
  base_url="https://github.com/$REPO/releases/latest/download"
else
  case "$VERSION" in
    v*) tag="$VERSION" ;;
    *)  tag="v$VERSION" ;;
  esac
  base_url="https://github.com/$REPO/releases/download/$tag"
fi
url="$base_url/antiaimark-$os-$arch.tar.gz"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

log "downloading $url"
curl -fSL --retry 3 -o "$tmp/antiaimark.tar.gz" "$url" || die "download failed (is the release published? see .github/workflows/release.yml)"

log "installing binaries into $DEST"
mkdir -p "$DEST"
tar -xzf "$tmp/antiaimark.tar.gz" -C "$tmp"
bin_dir="$tmp/antiaimark-$os-$arch"
[ -d "$bin_dir" ] || die "archive layout unexpected: no antiaimark-$os-$arch directory"
install -m 0755 "$bin_dir"/* "$DEST/"

log "done: $DEST/antiaimark"

# -- PATH --------------------------------------------------------------------
if ! command -v antiaimark >/dev/null 2>&1 && ! printf '%s' "$PATH" | grep -q "$DEST"; then
  printf '\n\033[1;33mAdd %s to your PATH:\033[0m\n  export PATH="$HOME/.local/bin:$PATH"\n' "$DEST"
fi

# -- IDE registration --------------------------------------------------------
EXE="$DEST/antiaimark"
SERVER_URL="http://127.0.0.1:8765/mcp"
cat <<EOF

============================================================
  antiaimark MCP is ready — register it in any AI IDE
  (stdio) : $EXE mcp
  (HTTP)  : $SERVER_URL   (run "antiaimark server" first)
============================================================

Claude Code:
  claude mcp add antiaimark -- $EXE mcp
  # HTTP:  claude mcp add antiaimark --transport http $SERVER_URL

Cursor / Windsurf / Cline / Zed — project root .mcp.json:
  { "mcpServers": { "antiaimark": { "command": "$EXE", "args": ["mcp"] } } }
  # HTTP:  { "mcpServers": { "antiaimark": { "type": "http", "url": "$SERVER_URL" } } }

Claude Desktop — claude_desktop_config.json:
  { "mcpServers": { "antiaimark": { "command": "$EXE", "args": ["mcp"] } } }

Codex CLI / WorkBuddy / DeepSeek Harness:
  codex mcp add antiaimark -- $EXE mcp
  WorkBuddy: ~/.workbuddy/mcp.json { "mcpServers": { "antiaimark": { "command": "$EXE", "args": ["mcp"] } } }
  dsh: paste the integration prompt from docs/MCP.md

Cline (VS Code) / Continue / VS Code Copilot / JetBrains AI:
  add "antiaimark" in their MCP settings, same command or $SERVER_URL

Full per-IDE steps: docs/MCP.md
EOF
