#!/usr/bin/env bash
#
# install.sh — download the prebuilt compliance-operator-importer binary for
# this host's OS/architecture and drop it into the current directory.
#
# USAGE:
#   curl -fsSL https://raw.githubusercontent.com/guzalv/stackrox-co-importer/main/install.sh | bash
#
# It detects the OS and architecture, resolves the latest GitHub release,
# downloads the matching release archive, verifies its SHA-256 checksum, and
# extracts the binary. It installs nothing system-wide and needs no root.
#
# ENV OVERRIDES:
#   VERSION      Install this release instead of the latest (e.g. v0.0.13 or
#                0.0.13). When set, the latest-release lookup is skipped.
#   INSTALL_DIR  Directory to place the binary in (default: current directory).
#   BASE_URL     Base URL for both the release redirect and the asset download
#                (default: https://github.com). Used by tests against a stub.
#
# Supported platforms: linux/darwin on amd64/arm64. On other platforms it exits
# with an error and a pointer to the manual download page.

set -euo pipefail

OWNER_REPO="guzalv/stackrox-co-importer"
BINARY="compliance-operator-importer"
BASE_URL="${BASE_URL:-https://github.com}"
INSTALL_DIR="${INSTALL_DIR:-.}"

log()  { printf '%s\n' "$*" >&2; }
fail() { printf 'error: %s\n' "$*" >&2; exit 1; }

# --- preflight: required tools ----------------------------------------------
command -v curl >/dev/null 2>&1 || fail "curl is required but not found"
command -v tar  >/dev/null 2>&1 || fail "tar is required but not found"

if command -v sha256sum >/dev/null 2>&1; then
  sha256_of() { sha256sum "$1" | awk '{print $1}'; }
elif command -v shasum >/dev/null 2>&1; then
  sha256_of() { shasum -a 256 "$1" | awk '{print $1}'; }
else
  fail "a SHA-256 tool is required but not found (install coreutils for sha256sum, or use shasum)"
fi

# --- detect OS --------------------------------------------------------------
case "$(uname -s)" in
  Linux)  os=linux ;;
  Darwin) os=darwin ;;
  *)      fail "unsupported OS '$(uname -s)'. On Windows use WSL, or download a binary manually from https://github.com/${OWNER_REPO}/releases/latest" ;;
esac

# --- detect architecture ----------------------------------------------------
case "$(uname -m)" in
  x86_64|amd64)  arch=amd64 ;;
  aarch64|arm64) arch=arm64 ;;
  *)             fail "unsupported architecture '$(uname -m)'. Download a binary manually from https://github.com/${OWNER_REPO}/releases/latest" ;;
esac

# --- resolve version --------------------------------------------------------
if [ -n "${VERSION:-}" ]; then
  tag="$VERSION"
  case "$tag" in v*) ;; *) tag="v${tag}" ;; esac   # accept 0.0.13 or v0.0.13
  log "Using requested version ${tag}"
else
  log "Resolving latest release..."
  # GitHub redirects /releases/latest to /releases/tag/<tag>. Follow it and
  # take the last path segment. This avoids the rate-limited API and any JSON
  # parsing.
  effective_url="$(curl -fsSLI -o /dev/null -w '%{url_effective}' "${BASE_URL}/${OWNER_REPO}/releases/latest")" \
    || fail "could not reach ${BASE_URL} to resolve the latest release"
  tag="${effective_url##*/}"
  case "$tag" in
    v*) ;;
    *)  fail "could not parse a version tag from '${effective_url}'" ;;
  esac
  log "Latest release is ${tag}"
fi

version="${tag#v}"
asset="stackrox-co-importer_${version}_${os}_${arch}.tar.gz"
download_base="${BASE_URL}/${OWNER_REPO}/releases/download/${tag}"

log "Detected platform: ${os}/${arch}"

# --- download archive + checksums into a temp dir ---------------------------
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

log "Downloading ${asset}..."
curl -fsSL -o "${tmp}/${asset}" "${download_base}/${asset}" \
  || fail "failed to download ${download_base}/${asset}"
curl -fsSL -o "${tmp}/checksums.txt" "${download_base}/checksums.txt" \
  || fail "failed to download ${download_base}/checksums.txt"

# --- verify checksum --------------------------------------------------------
expected="$(awk -v f="$asset" '$2 == f {print $1}' "${tmp}/checksums.txt")"
[ -n "$expected" ] || fail "no checksum entry for ${asset} in checksums.txt"
actual="$(sha256_of "${tmp}/${asset}")"
if [ "$expected" != "$actual" ]; then
  fail "checksum mismatch for ${asset}: expected ${expected}, got ${actual}"
fi
log "Checksum verified."

# --- extract binary ---------------------------------------------------------
tar xzf "${tmp}/${asset}" -C "$tmp" "$BINARY" \
  || fail "archive did not contain expected binary '${BINARY}'"
mkdir -p "$INSTALL_DIR"
install -m 755 "${tmp}/${BINARY}" "${INSTALL_DIR}/${BINARY}"

# Resolve a friendly path for the run hint (absolute dir + ./binary if cwd).
dest="${INSTALL_DIR%/}/${BINARY}"
case "$INSTALL_DIR" in
  .) run_hint="./${BINARY}" ;;
  *) run_hint="${dest}" ;;
esac

log ""
log "Installed ${BINARY} ${tag} to ${dest}"
log "Run it with:"
log "  ${run_hint} --help"
log ""
log "Example dry run:"
log "  ${run_hint} --endpoint https://central.example.com:443 --dry-run"
# macOS note: a binary fetched with curl carries no com.apple.quarantine
# attribute, so Gatekeeper does not block it (unlike a browser download).
