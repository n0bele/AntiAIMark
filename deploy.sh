#!/usr/bin/env bash
# deploy.sh — build, package and deploy antiaimark (Go edition).
#
# Works in Git Bash / WSL / Linux. Linux + root is required only for the
# systemd commands; everything else runs unprivileged.
#
# Usage: ./deploy.sh <command> [args]
#   build                  build all binaries for the host platform into bin/
#   build-linux [arch]     cross-compile linux binaries (amd64|arm64|386)
#   package [arch]         build a self-contained linux tarball into dist/
#   docker-build [tag]     build the Docker image (default antiaimark:latest)
#   docker-run [name]      run the image with production defaults
#                          (loopback port, read-only rootfs, tmpfs /tmp, restart policy)
#   install-systemd        install binaries + user + env file + systemd unit (Linux, root)
#   uninstall-systemd      stop, disable and remove the systemd unit (Linux, root)
#   status                 show the systemd service status (Linux)
#   help                   this help
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BIN_DIR="$REPO_DIR/bin"
DIST_DIR="$REPO_DIR/dist"
IMAGE_TAG="antiaimark:latest"
CONTAINER_NAME="antiaimark"
SERVICE_USER="antiaimark"
SERVICE_ENV=/etc/antiaimark.env
SERVICE_UNIT=/etc/systemd/system/antiaimark.service

log()  { printf '\033[1;32m==>\033[0m %s\n' "$*"; }
die()  { printf '\033[1;31merror:\033[0m %s\n' "$*" >&2; exit 1; }

require_go()   { command -v go >/dev/null || die "Go toolchain not found (https://go.dev/dl/)"; }
require_docker() { command -v docker >/dev/null || die "docker not found"; }
require_linux_root() {
  [ "$(uname -s)" = "Linux" ] || die "this command is Linux-only (use docker-* commands elsewhere)"
  [ "$(id -u)" = "0" ] || die "this command needs root (sudo ./deploy.sh $1)"
}

all_cmds=(clean-text inspect-text clean-image inspect-image clean-file inspect-file
          rewrite-text audit-dir audit-website antiaimark-server antiaimark-mcp healthcheck)

cmd_build() {
  require_go
  log "building $(go env GOOS)/$(go env GOARCH) binaries into bin/"
  mkdir -p "$BIN_DIR"
  go build -trimpath -o "$BIN_DIR" ./cmd/...
  ls -1 "$BIN_DIR"
}

cmd_build_linux() {
  require_go
  local arch="${1:-amd64}"
  case "$arch" in amd64|arm64|386) ;; *) die "arch must be amd64|arm64|386 (got $arch)";; esac
  log "cross-compiling linux/$arch into bin/linux-$arch/"
  mkdir -p "$BIN_DIR/linux-$arch"
  # Windows-hosted Go toolchains occasionally ICE under parallel compilation;
  # retry once at reduced parallelism before giving up.
  if ! CGO_ENABLED=0 GOOS=linux GOARCH=$arch go build -trimpath -ldflags="-s -w" \
        -o "$BIN_DIR/linux-$arch" ./cmd/...; then
    log "transient toolchain failure — retrying with -p=4"
    if ! GOFLAGS=-p=4 CGO_ENABLED=0 GOOS=linux GOARCH=$arch go build \
          -trimpath -ldflags="-s -w" -o "$BIN_DIR/linux-$arch" ./cmd/...; then
      log "retrying with -p=1"
      GOFLAGS=-p=1 CGO_ENABLED=0 GOOS=linux GOARCH=$arch go build \
        -trimpath -ldflags="-s -w" -o "$BIN_DIR/linux-$arch" ./cmd/...
    fi
  fi
  ls -1 "$BIN_DIR/linux-$arch"
}

cmd_package() {
  local arch="${1:-amd64}"
  cmd_build_linux "$arch"
  local version
  version="$(date +%Y%m%d)-linux-$arch"
  local out="$DIST_DIR/antiaimark-$version.tar.gz"
  mkdir -p "$DIST_DIR"
  log "packaging $out"
  tar -czf "$out" -C "$REPO_DIR" \
    --transform "s|^bin/linux-$arch|antiaimark/bin|" \
    --transform "s|^README|antiaimark/README|" \
    --transform "s|^LICENSE|antiaimark/LICENSE|" \
    --transform "s|^deploy/|antiaimark/deploy/|" \
    "bin/linux-$arch" README.md README-ARCHITECTURE.md README.zh-CN.md README.es.md README.fr.md README.ru.md LICENSE deploy/
  log "done: $out ($(du -h "$out" | cut -f1)) — scp it, then: sudo ./deploy.sh install-systemd"
}

cmd_docker_build() {
  require_docker
  local tag="${1:-$IMAGE_TAG}"
  log "building Docker image $tag"
  docker build -t "$tag" "$REPO_DIR"
}

cmd_docker_run() {
  require_docker
  local name="${1:-$CONTAINER_NAME}"
  local port="${ANTIAIMARK_PORT:-8765}"
  docker rm -f "$name" >/dev/null 2>&1 || true
  log "running $name on 127.0.0.1:$port (read-only rootfs, tmpfs /tmp)"
  docker run -d --name "$name" \
    --restart unless-stopped \
    -p "127.0.0.1:$port:8765" \
    -e ANTIAIMARK_SERVER_API_KEY="${ANTIAIMARK_SERVER_API_KEY:-}" \
    -e ANTIAIMARK_AUTO_CLEAN="${ANTIAIMARK_AUTO_CLEAN:-0}" \
    -e ANTIAIMARK_AUTO_CLEAN_INTERVAL="${ANTIAIMARK_AUTO_CLEAN_INTERVAL:-15m}" \
    -e ANTIAIMARK_AUTO_CLEAN_THRESHOLD="${ANTIAIMARK_AUTO_CLEAN_THRESHOLD:-11}" \
    -e ANTIAIMARK_AUTO_CLEAN_TTL="${ANTIAIMARK_AUTO_CLEAN_TTL:-24h}" \
    -e ANTIAIMARK_LANG="${ANTIAIMARK_LANG:-}" \
    --read-only --tmpfs /tmp \
    --security-opt no-new-privileges \
    --cap-drop ALL \
    "$IMAGE_TAG"
  log "health: curl -s http://127.0.0.1:$port/health"
}

cmd_install_systemd() {
  require_linux_root install-systemd
  local arch
  case "$(uname -m)" in
    x86_64)        arch=amd64 ;;
    aarch64|arm64) arch=arm64 ;;
    i686|i386)     arch=386 ;;
    *) die "unsupported machine $(uname -m) — build first: ./deploy.sh build-linux <arch>, then copy bin/linux-<arch>/ manually";;
  esac

  # 1. binaries (prefer a cross-built tree; fall back to a native build)
  if [ -d "$BIN_DIR/linux-$arch" ]; then
    log "installing bin/linux-$arch -> /usr/local/bin"
    install -m 0755 "$BIN_DIR/linux-$arch/"* /usr/local/bin/
  else
    log "no prebuilt linux/$arch tree; building natively"
    cmd_build
    install -m 0755 "$BIN_DIR/"* /usr/local/bin/
  fi

  # 2. dedicated service user (no login, no home)
  if ! id "$SERVICE_USER" >/dev/null 2>&1; then
    log "creating system user $SERVICE_USER"
    useradd --system --no-create-home --shell /usr/sbin/nologin "$SERVICE_USER"
  fi

  # 3. env file (never overwrite an existing configuration)
  if [ ! -f "$SERVICE_ENV" ]; then
    log "installing $SERVICE_ENV (edit it to configure port/API key/auto-clean)"
    install -m 0640 -o root -g "$SERVICE_USER" \
      "$REPO_DIR/deploy/antiaimark.env.example" "$SERVICE_ENV"
  else
    log "keeping existing $SERVICE_ENV"
  fi

  # 4. docs + unit
  install -d -m 0755 /usr/local/share/antiaimark
  install -m 0644 "$REPO_DIR/README.md" /usr/local/share/antiaimark/ 2>/dev/null || true
  install -m 0644 "$REPO_DIR/deploy/antiaimark.service" "$SERVICE_UNIT"

  # 5. enable + start
  systemctl daemon-reload
  systemctl enable --now antiaimark.service
  sleep 1
  systemctl --no-pager --lines 5 status antiaimark.service || true
  log "service installed: systemctl status antiaimark"
}

cmd_uninstall_systemd() {
  require_linux_root uninstall-systemd
  systemctl disable --now antiaimark.service || true
  rm -f "$SERVICE_UNIT"
  systemctl daemon-reload
  log "unit removed (binaries, user and $SERVICE_ENV kept; remove manually if desired)"
}

cmd_status() {
  require_linux_root status
  systemctl --no-pager status antiaimark.service
}

cmd_help() { sed -n '2,16p' "$0" | sed 's/^# \{0,1\}//'; }

case "${1:-help}" in
  build)             shift; cmd_build "$@" ;;
  build-linux)       shift; cmd_build_linux "$@" ;;
  package)           shift; cmd_package "$@" ;;
  docker-build)      shift; cmd_docker_build "$@" ;;
  docker-run)        shift; cmd_docker_run "$@" ;;
  install-systemd)   shift; cmd_install_systemd "$@" ;;
  uninstall-systemd) shift; cmd_uninstall_systemd "$@" ;;
  status)            shift; cmd_status "$@" ;;
  help|-h|--help)    cmd_help ;;
  *) die "unknown command '$1' — try ./deploy.sh help" ;;
esac
