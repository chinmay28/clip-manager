#!/usr/bin/env bash
#
# Clip Manager — Linux quick-start installer (Ubuntu / Debian / Raspberry Pi OS).
#
# One command, run as root, installs Clip Manager as a hardened systemd service:
#
#   curl -fsSL https://raw.githubusercontent.com/chinmay28/clip-manager/main/scripts/quickstart.sh | sudo bash
#
# Two ways to get the binary — CLIP_INSTALL picks one:
#
#   source   (default) clone the repo and build it here. Needs Node and Go at
#            build time (installed automatically if missing); works on any
#            architecture and can track any branch/tag/commit.
#   release  download the prebuilt static binary from a GitHub release. No
#            toolchain, no source tree, no compile — seconds instead of minutes
#            on a Raspberry Pi.
#
#            curl -fsSL …/quickstart.sh | sudo CLIP_INSTALL=release bash
#
# Both modes produce the same thing: one static binary with the web client
# embedded, run by the same systemd unit, against the same clips directory.
# You can switch between them by re-running with a different CLIP_INSTALL.
#
# It is deliberately *non-disruptive* and *data-safe* — re-run it any time to
# upgrade in place:
#
#   * Idempotent. Re-running only swaps in newer code; it never deletes a clip
#     or resets a quota you have drawn.
#   * "Newer code" means origin/$CLIP_REF — main unless you say otherwise. The
#     command at the top of this file deploys main from whatever directory you
#     happen to be standing in, including a clone of this repo.
#   * The one exception is EXECUTING this file from a checkout (sudo ./scripts/
#     quickstart.sh), which builds that checkout exactly as it stands and never
#     pulls — that is how you deploy work in progress. Every run prints which
#     of the two it is doing, and says so if the checkout is behind.
#   * The clips directory and the quota config live at stable paths OUTSIDE
#     the source tree, so cloning, rebuilding, or pulling can never clobber
#     them. THE INSTALLER NEVER WRITES INTO THE CLIPS DIRECTORY AT ALL — the
#     footage is the data this app exists to protect, and quota enforcement
#     (which does delete, by design) belongs to the service, not to a deploy.
#   * Every upgrade STOPS the service and snapshots the quota config BEFORE
#     swapping code in, so the backup is always taken against a quiesced
#     service.
#   * The new build is compiled (or the new binary downloaded) while the old
#     version keeps serving. If that fails, the running service is untouched.
#   * After restart we poll /api/health; if the new version is unhealthy we
#     ROLL BACK to the previous commit (source mode) or the previous binary
#     (release mode), restore the pre-upgrade config snapshot, and restart —
#     so a bad upgrade self-heals to the last good state.
#
# The deployed artifact is a single static Go binary that embeds the built web
# client. Node is only needed at BUILD time (to compile the client with Vite);
# the running service has no Node, npm, or JS runtime dependency.
#
# Configure via environment variables (all optional):
#
#   CLIP_INSTALL     source | release        where the binary comes from (default: source)
#   CLIP_REPO        git URL to clone        (default: https://github.com/chinmay28/clip-manager.git)
#   CLIP_REF         branch/tag/commit       (default: main; source mode)
#   CLIP_RELEASE     latest | <tag>          release to install (default: latest; release mode)
#   CLIP_USER        service system user     (default: clip)
#   CLIP_PREFIX      install prefix          (default: /opt/clip; source → $PREFIX/src)
#   CLIP_DATA_DIR    config + backups dir    (default: /var/lib/clip)
#   CLIP_CLIPS_DIR   where the cameras record (default: $CLIP_DATA_DIR/clips).
#                    Point this at the directory your NVR or cameras already
#                    write into — one subdirectory per camera — or at SEVERAL,
#                    colon-separated (the PATH convention):
#
#                      CLIP_CLIPS_DIR=/mnt/nvr/footage:/media/usb/overflow
#
#                    Each becomes a pinned source the app cannot remove; more
#                    can be added and removed in the app itself. The service
#                    gets write access to every source, because deleting old
#                    footage against a quota is the job; everything else on
#                    the filesystem stays read-only to it.
#   CLIP_MOUNT_ROOTS mount roots the clips directory may live under,
#                    colon-separated (default: /media:/run/media:/mnt:/srv).
#                    The unit is sandboxed (ProtectSystem=strict) and grants
#                    these, so footage on an external disk or NAS mounted in
#                    the usual place is reachable; everything else stays
#                    read-only. Set empty to grant nothing beyond the data and
#                    clips directories.
#   PORT             port to listen on       (default: 8124)
#   HOST             bind address            (default: 0.0.0.0 — see the warning below)
#
# PORT, HOST and CLIP_CLIPS_DIR are remembered. On an upgrade, leaving one
# unset keeps whatever the service is already running with rather than
# resetting it to the default — so re-running this script to pick up a new
# version cannot quietly move a loopback-only install back onto every
# interface, or point the service away from your footage.
#
#   INSTALL_NODE     auto | never            install Node 22 if missing/old (default: auto; build-time only)
#   INSTALL_GO       auto | never            install Go if missing/old (default: auto; build-time only)
#   BACKUP_KEEP      pre-upgrade backups kept (default: 10)
#
# A NOTE ON HOST.  This defaults to 0.0.0.0, so the service is reachable from
# the rest of your network as soon as it is installed. Understand what you are
# exposing: there is no authentication yet, so anyone who can reach the port
# can watch your cameras' footage, download it, redraw quotas, and trigger a
# cleanup that deletes recordings. On a home LAN behind NAT that may be a
# trade you accept; anywhere else, set HOST=127.0.0.1 and put TLS with auth in
# front (a reverse proxy, or Tailscale Serve).

set -euo pipefail

# ---------------------------------------------------------------------------
# Logging
# ---------------------------------------------------------------------------
if [ -t 1 ]; then
  C_BLUE=$'\033[1;34m'; C_GREEN=$'\033[1;32m'; C_YELLOW=$'\033[1;33m'
  C_RED=$'\033[1;31m'; C_DIM=$'\033[2m'; C_OFF=$'\033[0m'
else
  C_BLUE=''; C_GREEN=''; C_YELLOW=''; C_RED=''; C_DIM=''; C_OFF=''
fi
log()  { printf '%s==>%s %s\n' "$C_BLUE" "$C_OFF" "$*"; }
ok()   { printf '%s ok %s %s\n' "$C_GREEN" "$C_OFF" "$*"; }
warn() { printf '%swarn%s %s\n' "$C_YELLOW" "$C_OFF" "$*" >&2; }
die()  { printf '%serr %s %s\n' "$C_RED" "$C_OFF" "$*" >&2; exit 1; }
step() { printf '\n%s%s%s\n' "$C_DIM" "$*" "$C_OFF"; }

# ---------------------------------------------------------------------------
# Must be root (system-wide service + dedicated user)
# ---------------------------------------------------------------------------
if [ "$(id -u)" -ne 0 ]; then
  die "Run as root: curl -fsSL .../quickstart.sh | sudo bash   (or: sudo ./scripts/quickstart.sh)"
fi
command -v systemctl >/dev/null 2>&1 || die "systemd is required (no systemctl found)."

# ---------------------------------------------------------------------------
# Configuration
# ---------------------------------------------------------------------------
INSTALL_MODE="${CLIP_INSTALL:-source}"
case "$INSTALL_MODE" in
  source | release) ;;
  *) die "CLIP_INSTALL must be 'source' or 'release' (got '$INSTALL_MODE')." ;;
esac
CLIP_REPO="${CLIP_REPO:-https://github.com/chinmay28/clip-manager.git}"
CLIP_REF="${CLIP_REF:-main}"
RELEASE_TAG="${CLIP_RELEASE:-latest}"
SVC_USER="${CLIP_USER:-clip}"
PREFIX="${CLIP_PREFIX:-/opt/clip}"
DATA_DIR="${CLIP_DATA_DIR:-/var/lib/clip}"
SERVICE_NAME="clip"
UNIT_PATH="/etc/systemd/system/${SERVICE_NAME}.service"

# How the service is already running, if it is. The unit is rewritten from
# scratch on every run, so without reading it back an upgrade would silently
# reset anything the env did not name again — re-running this script to pick up
# a new version would move a loopback-only install back onto every interface,
# or repoint the service away from the directory holding your footage. An unset
# variable therefore means "keep what it runs with now", and only a fresh
# install falls through to the defaults below.
PRIOR_EXEC=""
[ -f "$UNIT_PATH" ] && PRIOR_EXEC="$(sed -n 's/^ExecStart=//p' "$UNIT_PATH" | head -n 1)"

# The value of --flag in the running unit, or nothing.
prior_flag() {
  printf '%s' "$PRIOR_EXEC" | sed -n "s/.*--$1[= ]\([^ ]*\).*/\1/p" | head -n 1
}

# Every value of --clips in the running unit, colon-joined — the flag repeats,
# one occurrence per source, so a single-match sed would silently drop all but
# the first source on an upgrade.
prior_clips() {
  local prev="" out="" w
  for w in $PRIOR_EXEC; do
    case "$w" in
      --clips=*) out="$out:${w#--clips=}" ;;
      *) [ "$prev" = "--clips" ] && out="$out:$w" ;;
    esac
    prev="$w"
  done
  printf '%s' "${out#:}"
}

PORT="${PORT:-$(prior_flag port)}"
PORT="${PORT:-8124}"
HOST="${HOST:-$(prior_flag bind)}"
HOST="${HOST:-0.0.0.0}"
CLIPS_DIRS="${CLIP_CLIPS_DIR:-$(prior_clips)}"
CLIPS_DIRS="${CLIPS_DIRS:-$DATA_DIR/clips}"

INSTALL_NODE="${INSTALL_NODE:-auto}"
INSTALL_GO="${INSTALL_GO:-auto}"
BACKUP_KEEP="${BACKUP_KEEP:-10}"

SRC_DIR="$PREFIX/src"
# The service user is created with --no-create-home, so the home directory in
# its passwd entry does not exist and it has no way to create one. npm needs a
# writable HOME for its cache and logs — without it the install dies with
# `EACCES: permission denied, mkdir '/home/clip'` — and Go wants one for
# GOCACHE. Everything run as the service user therefore gets a HOME it owns.
BUILD_HOME="$PREFIX/.build-home"
CONFIG_PATH="$DATA_DIR/config.json"
BACKUP_DIR="$DATA_DIR/backups"

# Minimum Go release that can bootstrap the build; the go directive in go.mod
# pins the real toolchain, which Go fetches automatically.
GO_MIN_MINOR=25
GO_INSTALL_VERSION="1.25.0"
NODE_MIN_MAJOR=18

# Executed from inside a checkout (sudo ./scripts/quickstart.sh), build that
# checkout in place; piped from curl, deploy $CLIP_REF like any other install.
# Release mode never builds, so it ignores the surrounding checkout entirely.
#
# Which of the two is happening has to be read off BASH_SOURCE, not $0. Piped,
# bash sets $0 to "bash" and leaves BASH_SOURCE unset, so a naive
# `dirname ${BASH_SOURCE[0]:-$0}` collapses to "." — the CURRENT DIRECTORY —
# and stops detecting "ran from a checkout" at all, only "stood in one".
# BASH_SOURCE is only a readable file when bash was given a script to run,
# which is exactly the distinction being drawn.
SELF_FILE="${BASH_SOURCE[0]:-}"
LOCAL_CHECKOUT=""
if [ "$INSTALL_MODE" = source ] && [ -f "$SELF_FILE" ]; then
  SELF_DIR="$(cd "$(dirname "$SELF_FILE")" >/dev/null 2>&1 && pwd)"
  if top="$(git -C "$SELF_DIR" rev-parse --show-toplevel 2>/dev/null)" \
     && [ -f "$top/go.mod" ] \
     && grep -q 'module github.com/chinmay28/clip-manager' "$top/go.mod" 2>/dev/null; then
    LOCAL_CHECKOUT="$top"
    SRC_DIR="$top"   # build & serve from where the user already cloned
  fi
fi

if [ "$INSTALL_MODE" = release ]; then
  # No source tree at all: the binary is the whole install.
  SERVER_BIN="$PREFIX/bin/clip"
  WORK_DIR="$PREFIX"
else
  SERVER_BIN="$SRC_DIR/clip"
  WORK_DIR="$SRC_DIR"
fi
# Kept for rollback: the binary the service was running before this run.
PREV_BIN="${SERVER_BIN}.prev"
STAGED_BIN="${SERVER_BIN}.new"

log "Clip Manager quick start"
printf '  %-10s %s\n' "install"  "$INSTALL_MODE$( [ "$INSTALL_MODE" = release ] && echo " ($RELEASE_TAG)" )"
if [ "$INSTALL_MODE" = release ]; then
  printf '  %-10s %s\n' "binary"  "$SERVER_BIN"
else
  printf '  %-10s %s\n' "source"  "$SRC_DIR"
  # What is about to be built, stated before anything is built. "Nothing
  # changed after a deploy" is a much harder thing to sit and wonder about
  # than a line saying which commit the deploy was made from.
  if [ -n "$LOCAL_CHECKOUT" ]; then
    printf '  %-10s %s\n' "ref"    "this checkout, built as it stands (not updated)"
  else
    printf '  %-10s %s\n' "ref"    "origin/$CLIP_REF"
  fi
fi
printf '  %-10s %s\n' "data"     "$DATA_DIR"
printf '  %-10s %s\n' "clips"    "$(printf '%s' "$CLIPS_DIRS" | sed 's/:/, /g')"
printf '  %-10s %s\n' "service"  "${SERVICE_NAME}.service (user: $SVC_USER)"
printf '  %-10s %s\n' "listen"   "http://$HOST:$PORT"

# Run npm/git/go as the service user so the tree stays owned by them, and so the
# build matches the runtime account. Falls back to plain exec before the user exists.
as_svc() {
  # sudo scrubs the environment, so everything the build genuinely needs has to
  # be handed over explicitly. PATH matters because a `Defaults secure_path` in
  # sudoers overrides --preserve-env=PATH and would hide a freshly installed Go;
  # the proxy and CA variables matter because without them npm and go cannot
  # reach the network at all on a host behind a corporate proxy.
  local -a passthru=("HOME=$BUILD_HOME" "PATH=$PATH")
  local v val
  for v in HTTP_PROXY HTTPS_PROXY NO_PROXY http_proxy https_proxy no_proxy \
           GOPROXY GOPRIVATE; do
    val="${!v-}"
    [ -n "$val" ] && passthru+=("$v=$val")
  done

  # A custom CA bundle is only useful if the service user can read it. Handing
  # over a path it cannot open makes things worse than saying nothing: node
  # warns, then fails TLS anyway, and the reason is buried.
  if [ -n "${NODE_EXTRA_CA_CERTS-}" ]; then
    if sudo -u "$SVC_USER" test -r "$NODE_EXTRA_CA_CERTS" 2>/dev/null; then
      passthru+=("NODE_EXTRA_CA_CERTS=$NODE_EXTRA_CA_CERTS")
    else
      warn "NODE_EXTRA_CA_CERTS ($NODE_EXTRA_CA_CERTS) is not readable by $SVC_USER — ignoring it."
      warn "If the build cannot reach the registry, make that file world-readable and re-run."
    fi
  fi

  if id -u "$SVC_USER" >/dev/null 2>&1; then
    # Build needs devDependencies → make sure NODE_ENV isn't 'production'.
    sudo -u "$SVC_USER" --preserve-env=PATH env -u NODE_ENV "${passthru[@]}" "$@"
  else
    env -u NODE_ENV "${passthru[@]}" "$@"
  fi
}

# ---------------------------------------------------------------------------
# 1. Prerequisites
# ---------------------------------------------------------------------------
step "[1/7] Prerequisites"

APT=0; command -v apt-get >/dev/null 2>&1 && APT=1
ensure_pkg() {
  command -v "$1" >/dev/null 2>&1 && return 0
  [ "$APT" -eq 1 ] || die "'$1' missing and no apt-get to install it. Install it and re-run."
  log "installing $1…"
  apt-get update -y >/dev/null
  apt-get install -y "$1" >/dev/null
}

ensure_pkg curl
ensure_pkg ca-certificates
[ "$INSTALL_MODE" = source ] && ensure_pkg git
ok "curl, ca-certificates$( [ "$INSTALL_MODE" = source ] && echo ", git" ) present"

# Node and Go are build-time only — release mode needs neither.
if [ "$INSTALL_MODE" = source ]; then
  node_major() { node -v 2>/dev/null | sed 's/^v\([0-9]*\).*/\1/'; }
  if [ "$(node_major || echo 0)" -lt "$NODE_MIN_MAJOR" ] 2>/dev/null; then
    [ "$INSTALL_NODE" = auto ] || die "Node >= $NODE_MIN_MAJOR required and INSTALL_NODE=never."
    [ "$APT" -eq 1 ] || die "Node >= $NODE_MIN_MAJOR required; install it and re-run."
    log "installing Node 22 (build-time only)…"
    curl -fsSL https://deb.nodesource.com/setup_22.x | bash - >/dev/null
    apt-get install -y nodejs >/dev/null
  fi
  ok "node $(node -v)"

  go_minor() { go version 2>/dev/null | sed -n 's/.*go1\.\([0-9]*\).*/\1/p'; }
  if [ "$(go_minor || echo 0)" -lt "$GO_MIN_MINOR" ] 2>/dev/null; then
    [ "$INSTALL_GO" = auto ] || die "Go >= 1.$GO_MIN_MINOR required and INSTALL_GO=never."
    log "installing Go $GO_INSTALL_VERSION (build-time only)…"
    go_arch="$(dpkg --print-architecture 2>/dev/null || uname -m)"
    case "$go_arch" in
      amd64 | x86_64) go_arch=amd64 ;;
      arm64 | aarch64) go_arch=arm64 ;;
      armhf | armv7l) go_arch=armv6l ;;
      *) die "unsupported architecture '$go_arch' for an automatic Go install." ;;
    esac
    tmp="$(mktemp -d)"
    curl -fsSL "https://go.dev/dl/go${GO_INSTALL_VERSION}.linux-${go_arch}.tar.gz" -o "$tmp/go.tgz"
    rm -rf /usr/local/go
    tar -C /usr/local -xzf "$tmp/go.tgz"
    rm -rf "$tmp"
    export PATH="/usr/local/go/bin:$PATH"
    # Persist for the service user's later invocations and for interactive shells.
    printf 'export PATH=/usr/local/go/bin:$PATH\n' > /etc/profile.d/go.sh
  fi
  export PATH="/usr/local/go/bin:$PATH"
  ok "$(go version)"
fi

# ---------------------------------------------------------------------------
# 2. Service user
# ---------------------------------------------------------------------------
step "[2/7] Service user"
if id -u "$SVC_USER" >/dev/null 2>&1; then
  ok "user '$SVC_USER' exists"
else
  useradd --system --no-create-home --shell /usr/sbin/nologin "$SVC_USER"
  ok "created system user '$SVC_USER'"
fi

# Must exist before the first as_svc call (the clone in step 3 is one).
install -d -o "$SVC_USER" -g "$SVC_USER" -m 700 "$BUILD_HOME"

# Is there already a service here? That makes this run an upgrade rather than
# a fresh install, which is what turns on snapshots and rollback.
UPGRADE=0
[ -f "$UNIT_PATH" ] && UPGRADE=1
PREV_SHA=""

# ---------------------------------------------------------------------------
# 3. Source or release
# ---------------------------------------------------------------------------
step "[3/7] Fetch"

release_arch() {
  case "$(uname -m)" in
    x86_64 | amd64) echo "amd64" ;;
    aarch64 | arm64) echo "arm64" ;;
    *) die "no prebuilt binary for $(uname -m) — use the default source install." ;;
  esac
}

# git in someone else's checkout, run as root. Without this, git refuses to
# touch a tree it does not own and every read below fails identically to "no
# repository here", which is the one answer that must not be guessed at.
src_git() { git -C "$SRC_DIR" -c safe.directory="$SRC_DIR" "$@"; }

# What the build is about to be made from, appended to the line announcing it.
describe_head() {
  [ -d "$SRC_DIR/.git" ] || return 0
  sha="$(src_git rev-parse --short HEAD 2>/dev/null)" || return 0
  printf ' (%s)' "$sha"
}

# Building a checkout in place is the whole point of this mode — it is how you
# deploy something you are still working on. But it also means a tree that was
# cloned once and never pulled rebuilds the same commit forever, and the only
# outward sign is a version number that will not move: the deploy succeeds, the
# service restarts, and nothing whatsoever changes. Say it out loud instead.
warn_if_behind() {
  [ -d "$SRC_DIR/.git" ] || return 0
  # Offline, or a repo with no origin — nothing to compare against, so this
  # tells us nothing either way and stays quiet.
  src_git fetch --quiet --prune origin 2>/dev/null || return 0
  src_git rev-parse --verify --quiet "origin/$CLIP_REF" >/dev/null 2>&1 || return 0

  behind="$(src_git rev-list --count "HEAD..origin/$CLIP_REF" 2>/dev/null || echo 0)"
  [ "${behind:-0}" -gt 0 ] || return 0

  warn "this checkout is $behind commit(s) behind origin/$CLIP_REF."
  warn "quickstart builds what is here, so none of them will be installed."
  warn "to deploy them:  git -C '$SRC_DIR' pull --ff-only   (then re-run this)"
}

# Move the managed clone onto a commit, whatever the last build left lying in
# it.
#
# `git checkout` is the wrong verb here and would fail outright: the web build
# writes into internal/server/dist, which is TRACKED, so the second and every
# later deploy meets
#
#   error: Your local changes to the following files would be overwritten by
#   checkout: internal/server/dist/index.html
#
# and set -e ends the run — including the rollback path, where the whole point
# is to get back to something that works. This tree is a build artifact the
# script owns, not a working copy anyone edits, so a reset is honest about it.
#
# The clean is scoped to the build's output directory rather than the tree,
# because the running binary, the staged one and the rollback copy all sit in
# $SRC_DIR too, and a blanket `git clean -fd` would take the rollback copy with
# it. Stale hashed assets are worth removing: nothing references them, but
# go:embed puts every one of them in the binary.
deploy_to() {
  target="$1"
  as_svc git -C "$SRC_DIR" rev-parse --verify --quiet "${target}^{commit}" >/dev/null 2>&1 || return 1

  # Get onto the deploy branch without moving the tree — that always succeeds —
  # then move branch and tree together.
  as_svc git -C "$SRC_DIR" checkout -q -B deploy
  as_svc git -C "$SRC_DIR" reset -q --hard "$target"
  as_svc git -C "$SRC_DIR" clean -qfd -- internal/server/dist
}

RELEASE_VERSION=""
if [ "$INSTALL_MODE" = release ]; then
  arch="$(release_arch)"
  api="https://api.github.com/repos/chinmay28/clip-manager/releases"
  if [ "$RELEASE_TAG" = latest ]; then
    api="$api/latest"
  else
    api="$api/tags/$RELEASE_TAG"
  fi

  meta="$(curl -fsSL "$api")" || die "could not reach the GitHub releases API."
  RELEASE_VERSION="$(printf '%s' "$meta" | sed -n 's/.*"tag_name" *: *"\([^"]*\)".*/\1/p' | head -1)"
  [ -n "$RELEASE_VERSION" ] || die "could not determine the release tag."

  asset="clip-${RELEASE_VERSION}-linux-${arch}"
  base="https://github.com/chinmay28/clip-manager/releases/download/${RELEASE_VERSION}"

  tmp="$(mktemp -d)"; trap 'rm -rf "$tmp"' EXIT
  log "downloading $asset ($RELEASE_VERSION)…"
  curl -fsSL "$base/$asset" -o "$tmp/clip" \
    || die "no $asset in release $RELEASE_VERSION — that architecture may not be published; use the source install."

  # Verify before anything is swapped in. A corrupted or tampered download must
  # never reach the point where it can replace a working binary.
  if curl -fsSL "$base/SHA256SUMS" -o "$tmp/SHA256SUMS" 2>/dev/null; then
    want="$(grep " $asset\$" "$tmp/SHA256SUMS" | awk '{print $1}' | head -1)"
    if [ -n "$want" ]; then
      got="$(sha256sum "$tmp/clip" | awk '{print $1}')"
      [ "$want" = "$got" ] || die "checksum mismatch for $asset (expected $want, got $got). Refusing to install."
      ok "checksum verified"
    else
      warn "SHA256SUMS has no entry for $asset — installing unverified."
    fi
  else
    warn "no SHA256SUMS published for $RELEASE_VERSION — installing unverified."
  fi

  install -d -m 755 "$(dirname "$SERVER_BIN")"
  install -m 755 "$tmp/clip" "$STAGED_BIN"
  ok "staged $RELEASE_VERSION → $STAGED_BIN"
else
  if [ -n "$LOCAL_CHECKOUT" ]; then
    ok "building the checkout at $SRC_DIR$(describe_head)"
    [ -d "$SRC_DIR/.git" ] && PREV_SHA="$(src_git rev-parse HEAD 2>/dev/null || true)"
    warn_if_behind
  elif [ -d "$SRC_DIR/.git" ]; then
    PREV_SHA="$(as_svc git -C "$SRC_DIR" rev-parse HEAD)"
    log "updating $SRC_DIR to $CLIP_REF…"
    as_svc git -C "$SRC_DIR" fetch --prune origin
    deploy_to "origin/$CLIP_REF" || deploy_to "$CLIP_REF"
    ok "at $(as_svc git -C "$SRC_DIR" rev-parse --short HEAD)"
  else
    install -d -o "$SVC_USER" -g "$SVC_USER" -m 755 "$PREFIX"
    log "cloning $CLIP_REPO…"
    # NOT --depth 1: the version's patch number is the commit count, so a
    # shallow clone would build something calling itself v2026.8.1 forever.
    # blob:none keeps it cheap while still carrying the full commit graph.
    as_svc git clone --filter=blob:none --branch "$CLIP_REF" "$CLIP_REPO" "$SRC_DIR"
    ok "cloned to $SRC_DIR"
  fi
fi

# ---------------------------------------------------------------------------
# 4. Build (source mode only)
# ---------------------------------------------------------------------------
step "[4/7] Build"

build_src() {
  # Build the web client first — the Go binary embeds it, so the order matters.
  #
  # `cd` rather than `npm --prefix`: --prefix is not honoured consistently for
  # `npm ci` across npm versions — some read package-lock.json from the working
  # directory regardless — and the resulting EUSAGE is a baffling way to fail.
  #
  # node_modules is cleared first so a re-run after a failed install starts from
  # a known state instead of a half-extracted tree.
  #
  # --no-audit --no-fund: an unattended installer must not fail because a
  # non-essential advisory lookup could not reach the registry.
  as_svc sh -c "cd '$SRC_DIR/web' && rm -rf node_modules && npm ci --no-audit --no-fund"
  as_svc sh -c "cd '$SRC_DIR/web' && npm run build"

  # Stamp the version: the patch number is the commit count, which only exists
  # here at build time. `make build-go` does the same thing.
  patch="$(as_svc node "$SRC_DIR/scripts/version.mjs" --patch 2>/dev/null || echo 0)"
  as_svc go -C "$SRC_DIR" build -trimpath \
      -ldflags "-s -w -X github.com/chinmay28/clip-manager/internal/version.Patch=${patch}" \
      -o "$STAGED_BIN" ./cmd/clip
}

if [ "$INSTALL_MODE" = source ]; then
  chown -R "$SVC_USER":"$SVC_USER" "$SRC_DIR" 2>/dev/null || true
  # The build runs while the OLD binary keeps serving. A failure here leaves
  # the running service completely untouched.
  build_src
  ok "built $("$STAGED_BIN" version 2>/dev/null || echo "clip")"
else
  ok "no build needed (prebuilt release)"
fi

# ---------------------------------------------------------------------------
# 5. Data dir + pre-upgrade config snapshot
# ---------------------------------------------------------------------------
step "[5/7] Data directory + backup"
install -d -o "$SVC_USER" -g "$SVC_USER" -m 750 "$DATA_DIR" "$BACKUP_DIR"
# A clips directory is created only when it is the default under $DATA_DIR.
# Sources you pointed at existing footage are expected to be there already —
# inventing one would paper over a typo'd path with an empty directory, and
# the app would then report "no clips" instead of the mistake.
printf '%s:' "$CLIPS_DIRS" | tr ':' '\n' | while IFS= read -r src; do
  [ -n "$src" ] || continue
  if [ "$src" = "$DATA_DIR/clips" ]; then
    install -d -o "$SVC_USER" -g "$SVC_USER" -m 750 "$src"
  elif [ ! -d "$src" ]; then
    die "clips source $src does not exist. Point CLIP_CLIPS_DIR at the directories your cameras record into (colon-separated), or leave it unset for $DATA_DIR/clips."
  fi
done
ok "data dir ready ($DATA_DIR, owned by $SVC_USER)"

stop_service()  { systemctl stop  "${SERVICE_NAME}.service" 2>/dev/null || true; }
start_service() { systemctl start "${SERVICE_NAME}.service"; }

SNAP=""
if [ "$UPGRADE" -eq 1 ] && [ -f "$CONFIG_PATH" ]; then
  # Quiesce first so the snapshot can't catch a half-written config. The server
  # writes it atomically, but stopping costs nothing and removes the question.
  stop_service
  ts="$(date +%Y%m%d-%H%M%S)"
  SNAP="$BACKUP_DIR/config-$ts.json"
  cp "$CONFIG_PATH" "$SNAP"
  chown "$SVC_USER":"$SVC_USER" "$SNAP" 2>/dev/null || true
  chmod 600 "$SNAP"
  ok "quota config backed up → $SNAP"
  # Prune, keeping the newest $BACKUP_KEEP.
  if [ "$BACKUP_KEEP" -gt 0 ]; then
    ls -1t "$BACKUP_DIR"/config-*.json 2>/dev/null | tail -n +"$((BACKUP_KEEP + 1))" | while read -r old; do
      rm -f "$old"
    done
  fi
fi

# ---------------------------------------------------------------------------
# 6. systemd unit + (re)start
# ---------------------------------------------------------------------------
step "[6/7] systemd service"

# The service is quiesced by now on an upgrade, so this is where the staged
# binary replaces the running one (keeping the old one for rollback).
install_staged() {
  [ -f "$STAGED_BIN" ] || return 0
  stop_service
  [ -f "$SERVER_BIN" ] && cp -f "$SERVER_BIN" "$PREV_BIN"
  mv -f "$STAGED_BIN" "$SERVER_BIN"
  chown "$SVC_USER":"$SVC_USER" "$SERVER_BIN" 2>/dev/null || true
  chmod 755 "$SERVER_BIN"
}
install_staged

# ProtectSystem=strict in the unit makes the whole filesystem read-only to the
# service. The clips directory has to be writable — deleting old footage
# against a quota is the job — and removable disks and network shares are
# mounted under a handful of well-known roots, so the unit grants those
# outright: footage on an external drive is reachable with no extra step,
# while /etc, /usr, /home and everything else stay read-only.
# CLIP_MOUNT_ROOTS overrides the list; set it empty for a unit that grants
# nothing but $DATA_DIR and $CLIPS_DIR.
MOUNT_ROOTS="${CLIP_MOUNT_ROOTS-/media:/run/media:/mnt:/srv}"
mount_root_lines() {
  # The trailing ':' gives the last root a newline of its own, so `read` sees
  # it. The leading '-' lets the service start on a host where a root does not
  # exist; the quotes keep a path with a space in it as one path.
  printf '%s:' "$MOUNT_ROOTS" | tr ':' '\n' | while IFS= read -r root; do
    [ -n "$root" ] || continue
    printf 'ReadWritePaths=-"%s"\n' "${root%/}"
  done
}
MOUNT_ROOT_LINES="$(mount_root_lines)"

# One --clips per source on the command line, and a write grant per source —
# the '-' on the grant lets the service start with a source's drive unplugged,
# which is exactly when its footage should merely be missing, not the whole
# app down. Sources added later IN the app get no grant of their own and rely
# on the mount roots above (or on living under $DATA_DIR); a source outside
# both connects read-only and enforcement says so rather than deleting.
CLIPS_ARGS=""
clips_grant_lines() {
  printf '%s:' "$CLIPS_DIRS" | tr ':' '\n' | while IFS= read -r src; do
    [ -n "$src" ] || continue
    printf 'ReadWritePaths=-"%s"\n' "${src%/}"
  done
}
CLIPS_GRANT_LINES="$(clips_grant_lines)"
while IFS= read -r src; do
  [ -n "$src" ] || continue
  CLIPS_ARGS="$CLIPS_ARGS --clips $src"
done <<CLIPSEOF
$(printf '%s:' "$CLIPS_DIRS" | tr ':' '\n')
CLIPSEOF

write_unit() {
  cat > "$UNIT_PATH" <<UNIT
[Unit]
Description=Clip Manager — view security-camera clips and keep their directory in budget
Documentation=https://github.com/chinmay28/clip-manager
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=$SVC_USER
Group=$SVC_USER
WorkingDirectory=$WORK_DIR
ExecStart=$SERVER_BIN serve --port $PORT --bind $HOST$CLIPS_ARGS --data $DATA_DIR
Restart=on-failure
RestartSec=3

# A ceiling, so that whatever the service does it does to itself rather than
# to the machine. A percentage rather than a number, so it tracks whatever it
# lands on: 800 MB on a 1 GB Pi, 12.8 GB on a 16 GB one, without editing
# anything here. MemorySwapMax=0 is the half that keeps the box responsive: a
# limit met by swapping to an SD card is precisely the unresponsiveness the
# limit exists to prevent — better to be killed and restarted than to take
# ssh down with you.
MemoryMax=80%
MemorySwapMax=0

# Hardening. The service can watch and DELETE camera footage, so it gets write
# access to its data directory, the clips directory, and the mount roots
# footage usually lives under — and nothing else. Note ProtectHome, which is
# also why the defaults live in /var/lib rather than anybody's home.
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
PrivateDevices=true
ProtectKernelTunables=true
ProtectControlGroups=true
RestrictSUIDSGID=true
ReadWritePaths=$DATA_DIR
$CLIPS_GRANT_LINES
$MOUNT_ROOT_LINES

[Install]
WantedBy=multi-user.target
UNIT
}
write_unit

systemctl daemon-reload
systemctl enable "${SERVICE_NAME}.service" >/dev/null 2>&1 || true
start_service
ok "service enabled and started"

# ---------------------------------------------------------------------------
# 7. Health check (with rollback on a failed upgrade)
# ---------------------------------------------------------------------------
step "[7/7] Health check"
health_url="http://127.0.0.1:$PORT/api/health"
check_health() {
  for _ in $(seq 1 30); do
    curl -fsS "$health_url" >/dev/null 2>&1 && return 0
    sleep 0.5
  done
  return 1
}

# Restore the pre-upgrade config snapshot, so the version we roll back to sees
# a policy it understands.
restore_snapshot() {
  if [ -n "$SNAP" ] && [ -f "$SNAP" ]; then
    cp "$SNAP" "$CONFIG_PATH"
    chown "$SVC_USER":"$SVC_USER" "$CONFIG_PATH" 2>/dev/null || true
    chmod 600 "$CONFIG_PATH"
  fi
}

if check_health; then
  ok "healthy ($health_url) — $(curl -fsS "$health_url" 2>/dev/null | sed -n 's/.*"version" *: *"\([^"]*\)".*/\1/p')"
elif [ "$INSTALL_MODE" = release ] && [ "$UPGRADE" -eq 1 ] && [ -f "$PREV_BIN" ]; then
  # Release-mode rollback: the previous binary is right there, so put it back
  # with the pre-upgrade config and restart.
  warn "$RELEASE_VERSION failed its health check."
  warn "rolling back to the previous binary and restoring the pre-upgrade config…"
  stop_service
  restore_snapshot
  mv -f "$PREV_BIN" "$SERVER_BIN"
  chown "$SVC_USER":"$SVC_USER" "$SERVER_BIN" 2>/dev/null || true
  start_service
  if check_health; then
    die "Upgrade to $RELEASE_VERSION failed its health check — rolled back to $("$SERVER_BIN" version 2>/dev/null || echo "the previous binary") with your config intact. Check: journalctl -u ${SERVICE_NAME} -n 80"
  fi
  die "Upgrade AND rollback both failed health checks. Your config snapshot is safe at $SNAP. Inspect: journalctl -u ${SERVICE_NAME} -n 80"
else
  warn "new version failed its health check."
  if [ "$UPGRADE" -eq 1 ] && [ -n "$PREV_SHA" ] && [ -z "$LOCAL_CHECKOUT" ]; then
    warn "rolling back to ${PREV_SHA:0:12} and restoring the pre-upgrade config…"
    stop_service
    restore_snapshot
    deploy_to "$PREV_SHA"
    build_src
    install_staged
    start_service
    if check_health; then
      die "Upgrade failed health check — rolled back to ${PREV_SHA:0:12} with your config intact. Check: journalctl -u ${SERVICE_NAME} -n 80"
    fi
    die "Upgrade AND rollback both failed health checks. Your config snapshot is safe at $SNAP. Inspect: journalctl -u ${SERVICE_NAME} -n 80"
  fi
  die "Service is not healthy. Inspect logs: journalctl -u ${SERVICE_NAME} -n 80 --no-pager"
fi

# ---------------------------------------------------------------------------
# Done
# ---------------------------------------------------------------------------
lan_ip="$(hostname -I 2>/dev/null | awk '{print $1}')"; [ -n "$lan_ip" ] || lan_ip="<this-host>"
verb="installed"; [ "$UPGRADE" -eq 1 ] && verb="upgraded"

if [ "$INSTALL_MODE" = release ]; then
  origin_line="Installed:   $RELEASE_VERSION, prebuilt from the $RELEASE_TAG release (no toolchain needed)"
  upgrade_line="Upgrade:     re-run with CLIP_INSTALL=release for the next release."
else
  origin_line="Source:      $SRC_DIR (built here)"
  upgrade_line="Upgrade:     re-run this script — it swaps code in, backs up your config, self-heals."
fi

if [ "$HOST" = "127.0.0.1" ]; then
  reach_line="Open it:     http://localhost:$PORT   (loopback only — see below)"
else
  reach_line="Open it:     http://$lan_ip:$PORT"
fi

cat <<DONE

${C_GREEN}Clip Manager $verb and running.${C_OFF}

  $reach_line
  Clips:       $(printf '%s' "$CLIPS_DIRS" | sed 's/:/, /g')   (one subdirectory per camera)
  Config:      $CONFIG_PATH
  Backups:     $BACKUP_DIR
  Binary:      $SERVER_BIN (static; embeds the web client)
  $origin_line
  $upgrade_line

  Point your cameras (or their NVR) at a clips directory, or re-run with
  CLIP_CLIPS_DIR=/path/one:/path/two to adopt directories they already write
  to — more sources can also be added and removed in the app itself. Then
  draw quotas — old footage is retired oldest-first, across every source, to
  stay under them. Preview any cleanup with:
    $SERVER_BIN prune$CLIPS_ARGS --data $DATA_DIR --dry-run

  Manage the service:
    systemctl status  ${SERVICE_NAME}
    systemctl restart ${SERVICE_NAME}
    journalctl -u ${SERVICE_NAME} -f
${C_DIM}
DONE

if [ "$HOST" = "127.0.0.1" ]; then
  cat <<NOTE
  Bound to loopback. To reach it from your network, put TLS with auth in front
  (Tailscale Serve, or a reverse proxy) and re-run with HOST=0.0.0.0.${C_OFF}
NOTE
else
  cat <<NOTE
  Bound to $HOST with no authentication: anyone who can reach the port can
  watch, download and DELETE footage (via quotas and cleanup). Fine behind a
  home NAT you trust; anywhere else, re-run with HOST=127.0.0.1 and put TLS
  with auth in front.${C_OFF}
NOTE
fi
