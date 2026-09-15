#!/usr/bin/env bash
# bump.sh <vX.Y.Z> [--allow-unsigned]
#
# Post-release pin bump: pulls sha256sums.txt from the published GitHub
# release, rewrites pkgver/pkgrel and per-arch sums in both PKGBUILDs,
# rewrites every pinned v<tag> literal in install.sh (the OMASEAL_VERSION
# default and the embedded-checksum guard) plus both EXPECTED_SHA256
# values, then regenerates both .SRCINFO files. Needs makepkg — run on
# an Arch host or in the release.yml bump-packaging job.
#
# Trust model: when the release ships sha256sums.txt.asc, the signature
# is verified against OMASEAL_SIGNING_FINGERPRINT (keyserver overridable
# via OMASEAL_KEYSERVER — same mechanism as install.sh). An unsigned
# release requires --allow-unsigned so that single-channel checksum trust
# is a deliberate act, never the default. Downloaded release content is
# untrusted input: parsed with grep/awk only, never sourced, eval'd, or
# expanded unquoted.
set -euo pipefail

die() { echo "bump.sh: $*" >&2; exit 1; }

ALLOW_UNSIGNED=0
TAG=""
while [ $# -gt 0 ]; do
  case "$1" in
    --allow-unsigned) ALLOW_UNSIGNED=1; shift ;;
    -h|--help) awk 'NR==1 && /^#!/ {next} /^#/ {sub(/^# ?/,""); print; next} {exit}' "$0"; exit 0 ;;
    v*) [ -z "$TAG" ] || die "multiple tags given"; TAG="$1"; shift ;;
    *) die "unknown argument: $1" ;;
  esac
done
[ -n "$TAG" ] || die "usage: bump.sh <vX.Y.Z> [--allow-unsigned]"
[[ "$TAG" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] || die "tag must look like vX.Y.Z: $TAG"
NEW_VER="${TAG#v}"

command -v makepkg >/dev/null || die "makepkg not found — run on an Arch host (or the archlinux CI container)"

REPO_ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
PKG_DIR="$REPO_ROOT/packaging/aur"
INSTALL_SH="$REPO_ROOT/install.sh"
REPO="duketopceo/OmaSeal"
DL_BASE="https://github.com/${REPO}/releases/download/${TAG}"

OLD_VER=$(awk -F= '/^pkgver=/{gsub(/[[:space:]]/,"",$2); print $2; exit}' "$PKG_DIR/PKGBUILD-bin")
[ -n "$OLD_VER" ] || die "could not read pkgver from PKGBUILD-bin"
OLD_TAG="v${OLD_VER}"

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

# --- Fetch and authenticate the checksum manifest --------------------------
curl -fsSL -o "$TMP/sha256sums.txt" "${DL_BASE}/sha256sums.txt" \
  || die "sha256sums.txt not found on release ${TAG} — is the release published?"

# A 404 means "unsigned"; any other failure is a fetch error, not consent.
asc_code=$(curl -fsSL -o "$TMP/sha256sums.txt.asc" -w '%{http_code}' \
  "${DL_BASE}/sha256sums.txt.asc" 2>/dev/null || true)
case "$asc_code" in
  200)
    [ -n "${OMASEAL_SIGNING_FINGERPRINT:-}" ] \
      || die "release ${TAG} is signed but OMASEAL_SIGNING_FINGERPRINT is unset — cannot verify"
    command -v gpg >/dev/null || die "gpg not found — needed to verify the signed release"
    export GNUPGHOME="$TMP/gnupg"
    mkdir -p "$GNUPGHOME" && chmod 700 "$GNUPGHOME"
    KEYRING="$TMP/keyring.gpg"
    KEYSERVER="${OMASEAL_KEYSERVER:-keyserver.ubuntu.com}"
    gpg --batch --yes --no-default-keyring --keyring "$KEYRING" \
        --keyserver "$KEYSERVER" --recv-keys "$OMASEAL_SIGNING_FINGERPRINT" >/dev/null 2>&1 \
      || die "could not fetch signing key ${OMASEAL_SIGNING_FINGERPRINT} from ${KEYSERVER}"
    gpg --batch --yes --no-default-keyring --keyring "$KEYRING" \
        --verify "$TMP/sha256sums.txt.asc" "$TMP/sha256sums.txt" >/dev/null 2>&1 \
      || die "GPG signature verification failed for ${TAG}"
    echo "GPG signature verified (fingerprint: ${OMASEAL_SIGNING_FINGERPRINT})."
    ;;
  404)
    [ "$ALLOW_UNSIGNED" -eq 1 ] \
      || die "release ${TAG} is unsigned (no sha256sums.txt.asc). Re-run with --allow-unsigned to accept checksum-only trust."
    echo "warning: unsigned release — trusting checksums alone (--allow-unsigned)" >&2
    ;;
  *) die "failed to fetch sha256sums.txt.asc (HTTP ${asc_code:-curl error})" ;;
esac

# --- Extract sums (untrusted input: parse fields only, never evaluate) ------
sum_for() {
  local name="$1" sum
  sum=$(awk -v n="$name" '{ f=$2; sub(/^\*/, "", f); if (f == n) print $1 }' "$TMP/sha256sums.txt")
  [[ "$sum" =~ ^[0-9a-f]{64}$ ]] || die "no valid sha256 for ${name} in sha256sums.txt"
  printf '%s' "$sum"
}
SUM_X86=$(sum_for "omaseal-linux-x86_64.tar.gz")
SUM_ARM=$(sum_for "omaseal-linux-aarch64.tar.gz")

curl -fsSL -o "$TMP/src.tar.gz" "https://github.com/${REPO}/archive/refs/tags/${TAG}.tar.gz" \
  || die "source archive for ${TAG} not found"
SUM_SRC=$(sha256sum "$TMP/src.tar.gz")
SUM_SRC="${SUM_SRC%% *}"

# --- Rewrite the pins -------------------------------------------------------
sed -i \
  -e "s/^pkgver=.*/pkgver=${NEW_VER}/" \
  -e "s/^pkgrel=.*/pkgrel=1/" \
  -e "s/^sha256sums_x86_64=('.*')/sha256sums_x86_64=('${SUM_X86}')/" \
  -e "s/^sha256sums_aarch64=('.*')/sha256sums_aarch64=('${SUM_ARM}')/" \
  "$PKG_DIR/PKGBUILD-bin"

sed -i \
  -e "s/^pkgver=.*/pkgver=${NEW_VER}/" \
  -e "s/^pkgrel=.*/pkgrel=1/" \
  -e "s/^sha256sums=('.*')/sha256sums=('${SUM_SRC}')/" \
  "$PKG_DIR/PKGBUILD"

# Every v<old-tag> literal in install.sh — the OMASEAL_VERSION default AND
# the `!= "vX.Y.Z"` embedded-checksum guard, which blanks EXPECTED_SHA256
# on a mismatch. A missed guard silently disables embedded verification.
# \b keeps a strict-prefix tag (v0.2.2 vs v0.2.20) from partial-rewriting.
OLD_RE="${OLD_TAG//./\\.}"
sed -i "s/\\b${OLD_RE}\\b/${TAG}/g" "$INSTALL_SH"

# EXPECTED_SHA256 is per-arch inside a case block: x86_64 first, aarch64
# second. Key on the case labels so order can't silently swap them.
awk -v x="$SUM_X86" -v a="$SUM_ARM" '
  /^[[:space:]]*x86_64\)/          { inx=1; ina=0 }
  /^[[:space:]]*(aarch64|arm64)\)/ { inx=0; ina=1 }
  /^[[:space:]]*\*\)/              { inx=0; ina=0 }
  inx && /EXPECTED_SHA256="/ { sub(/"[0-9a-f]*"/, "\"" x "\""); inx=0 }
  ina && /EXPECTED_SHA256="/ { sub(/"[0-9a-f]*"/, "\"" a "\""); ina=0 }
  { print }
' "$INSTALL_SH" > "$TMP/install.sh" && cat "$TMP/install.sh" > "$INSTALL_SH"

# --- Regenerate .SRCINFO (makepkg -p accepts the non-PKGBUILD filename; -----
# --- write via $TMP so a failed run never truncates the committed file) -----
(cd "$PKG_DIR" && makepkg --printsrcinfo) > "$TMP/.SRCINFO" \
  && cp "$TMP/.SRCINFO" "$PKG_DIR/.SRCINFO"
(cd "$PKG_DIR" && makepkg --printsrcinfo -p PKGBUILD-bin) > "$TMP/omaseal-bin.SRCINFO" \
  && cp "$TMP/omaseal-bin.SRCINFO" "$PKG_DIR/omaseal-bin.SRCINFO"

# --- Self-assertions: pins must have landed, not merely kept their shape ----
if [ "$OLD_TAG" != "$TAG" ] && grep -qw "$OLD_RE" "$INSTALL_SH"; then
  die "stale ${OLD_TAG} literal remains in install.sh"
fi
grep -q "EXPECTED_SHA256=\"${SUM_X86}\"" "$INSTALL_SH" \
  && grep -q "EXPECTED_SHA256=\"${SUM_ARM}\"" "$INSTALL_SH" \
  || die "EXPECTED_SHA256 pins not updated to ${TAG}"

echo "Bumped ${OLD_TAG} -> ${TAG}:"
echo "  packaging/aur/PKGBUILD      pkgver=${NEW_VER} + source sum"
echo "  packaging/aur/PKGBUILD-bin  pkgver=${NEW_VER} + per-arch sums"
echo "  install.sh                  version literals + EXPECTED_SHA256"
echo "  .SRCINFO / omaseal-bin.SRCINFO regenerated"
