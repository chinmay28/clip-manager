#!/usr/bin/env bash
# build-release.sh — Build Clip Manager release binaries for all platforms.
#
# Usage:
#   ./scripts/build-release.sh [version]
#
# Produces dist/ with:
#   clip-<version>-linux-amd64
#   clip-<version>-linux-arm64
#   clip-<version>-darwin-amd64
#   clip-<version>-darwin-arm64
#   clip-<version>-windows-amd64.exe
#
# Requirements: Go 1.25+, Node.js 18+

set -euo pipefail

# vYEAR.MONTH.PATCH with PATCH = the repo's commit count; scripts/version.mjs
# is the single place that is assembled, so the binary, the embedded web client
# and the release filenames can never disagree. Pass a version to override.
VERSION="${1:-$(node "$(dirname "$0")/version.mjs")}"
PATCH="$(node "$(dirname "$0")/version.mjs" --patch)"
DIST="dist"
LDFLAGS="-s -w -X github.com/chinmay28/clip-manager/internal/version.Patch=${PATCH}"

if [[ "${PATCH}" == "0" ]]; then
    echo "warn: patch number is 0 — this is an unstamped build, not a release." >&2
    echo "      A shallow clone does this; fetch --unshallow for the real count." >&2
fi

echo "==> Clip Manager release build  version=${VERSION}"

# ── 1. Frontend ──────────────────────────────────────────────────────────────
echo "==> Building React frontend…"
(cd web && npm ci && npm run build)
echo "    frontend built → internal/server/dist/"

# ── 2. Create dist dir ───────────────────────────────────────────────────────
rm -rf "${DIST}"
mkdir -p "${DIST}"

# ── 3. Cross-compile ─────────────────────────────────────────────────────────
build() {
    local GOOS="$1" GOARCH="$2"
    local OUT="${DIST}/clip-${VERSION}-${GOOS}-${GOARCH}"
    [[ "${GOOS}" == "windows" ]] && OUT="${OUT}.exe"

    echo "    compiling ${GOOS}/${GOARCH}…"
    CGO_ENABLED=0 GOOS="${GOOS}" GOARCH="${GOARCH}" \
        go build -trimpath -ldflags="${LDFLAGS}" -o "${OUT}" ./cmd/clip
}

build linux   amd64
build linux   arm64
build darwin  amd64
build darwin  arm64
build windows amd64

# ── 4. Checksums ─────────────────────────────────────────────────────────────
echo "==> Generating checksums…"
(cd "${DIST}" && sha256sum clip-* > SHA256SUMS)

echo ""
echo "==> Done. Artifacts in ${DIST}/:"
ls -lh "${DIST}/"
