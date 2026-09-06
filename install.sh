#!/usr/bin/env bash
set -euo pipefail

DRY_RUN=false
PREFIX="${HOME}/.local"
BIN_DIR="${PREFIX}/bin"

usage() {
  echo "Usage: $0 [--dry-run] [--prefix <dir>]"
  exit 1
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --dry-run)
      DRY_RUN=true
      shift
      ;;
    --prefix)
      PREFIX="$2"
      BIN_DIR="${PREFIX}/bin"
      shift 2
      ;;
    -h|--help)
      usage
      ;;
    *)
      echo "Unknown option: $1"
      usage
      ;;
  esac
done

ARCH=$(uname -m)
case "$ARCH" in
  x86_64)
    ARCH_NAME=x86_64
    ;;
  aarch64|arm64)
    ARCH_NAME=aarch64
    ;;
  *)
    echo "Unsupported architecture: $ARCH" >&2
    exit 1
    ;;
esac

TARBALL="omaseal-linux-${ARCH_NAME}.tar.gz"
DOWNLOAD_URL="https://github.com/duketopceo/OmaSeal/releases/latest/download/${TARBALL}"
SUMS_URL="https://github.com/duketopceo/OmaSeal/releases/latest/download/sha256sums.txt"
SIG_URL="https://github.com/duketopceo/OmaSeal/releases/latest/download/sha256sums.txt.asc"

echo "Downloading OmaSeal for ${ARCH_NAME}..."
echo "  URL: ${DOWNLOAD_URL}"

if [[ "$DRY_RUN" == true ]]; then
  echo "[dry-run] Would download: ${TARBALL} and sha256sums.txt"
  echo "[dry-run] Would extract ${TARBALL} -> ${BIN_DIR}/omaseal.new"
  echo "[dry-run] Would run ${BIN_DIR}/omaseal.new --version"
  echo "[dry-run] Would rename ${BIN_DIR}/omaseal -> ${BIN_DIR}/omaseal.previous"
  echo "[dry-run] Would move ${BIN_DIR}/omaseal.new -> ${BIN_DIR}/omaseal"
  exit 0
fi

TMPDIR=$(mktemp -d)
trap 'rm -rf "$TMPDIR"' EXIT

curl -fsSL -o "${TMPDIR}/${TARBALL}" "$DOWNLOAD_URL"
curl -fsSL -o "${TMPDIR}/sha256sums.txt" "$SUMS_URL" || true

if [[ -f "${TMPDIR}/sha256sums.txt" ]]; then
  (cd "$TMPDIR" && grep "$TARBALL" sha256sums.txt | sha256sum -c -) || {
    echo "Checksum verification failed." >&2
    exit 1
  }
fi

if command -v gpg >/dev/null 2>&1; then
  curl -fsSL -o "${TMPDIR}/sha256sums.txt.asc" "$SIG_URL" || true
  if [[ -f "${TMPDIR}/sha256sums.txt.asc" ]]; then
    if gpg --verify "${TMPDIR}/sha256sums.txt.asc" "${TMPDIR}/sha256sums.txt" 2>/dev/null; then
      echo "GPG signature verified."
    else
      echo "GPG signature check skipped or failed; falling back to checksum." >&2
    fi
  fi
fi

mkdir -p "$BIN_DIR"
tar -xzf "${TMPDIR}/${TARBALL}" -C "$TMPDIR"

NEW_BIN="${BIN_DIR}/omaseal.new"
PREV_BIN="${BIN_DIR}/omaseal.previous"
OLD_BIN="${BIN_DIR}/omaseal"

cp "${TMPDIR}/omaseal-linux-${ARCH_NAME}/omaseal" "$NEW_BIN"
chmod +x "$NEW_BIN"

if ! "$NEW_BIN" --version >/dev/null; then
  echo "New binary failed --version check. Aborting upgrade." >&2
  rm -f "$NEW_BIN"
  exit 1
fi

if [[ -f "$OLD_BIN" ]]; then
  cp -f "$OLD_BIN" "$PREV_BIN"
fi

mv -f "$NEW_BIN" "$OLD_BIN"
echo "OmaSeal installed to ${OLD_BIN}"
echo "Previous binary kept at ${PREV_BIN} for rollback."

if ! echo "$PATH" | tr ':' '\n' | grep -qx "$BIN_DIR"; then
  echo ""
  echo "NOTE: ${BIN_DIR} is not on your PATH. Add it to your shell profile:"
  echo "  export PATH=\"${BIN_DIR}:\$PATH\""
fi
