#!/usr/bin/env bash
# bump.sh <vX.Y.Z> [--allow-unsigned]
#
# Post-release pin bump: pulls sha256sums.txt from the published GitHub
# release, rewrites pkgver/pkgrel and per-arch sums in both PKGBUILDs,
# rewrites every pinned v<tag> literal in install.sh (the OMASEAL_VERSION
# default, the embedded-checksum guard, and both EXPECTED_SHA256 values),
# then regenerates both .SRCINFO files. Needs makepkg — run on an Arch
# host or in the release.yml bump-packaging job.
#
# Trust model: when the release ships sha256sums.txt.asc, the signature is
# verified against OMASEAL_SIGNING_FINGERPRINT (same keyserver mechanism
# as install.sh). An unsigned release requires --allow-unsigned so that
# single-channel checksum trust is a deliberate act, never the default.
# Downloaded release content is untrusted input: it is parsed with
# grep/awk only and never sourced, eval'd, or expanded unquoted.
set -euo pipefail

die() { echo "bump.sh: $*" >&2; exit 1; }

ALLOW_UNSIGNED=0
TAG=""
while [ $# -gt 0 ]; do
  case "$1" in
    --allow-unsigned) ALLOW_UNSIGNED=1; shift ;;
    -h|--help) grep '^#' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    v*) [ -z "$TAG" ] || die "multiple tags given"; TAG="$1"; shift ;;
    *) die "unknown argument: $1" ;;
  esac
done
[ -n "$TAG" ] || die "usage: bump.sh <vX.Y.Z> [--allow-unsigned]"
[[ "$TAG" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] || die "tag must look like vX.Y.Z: $TAG"
NEW_VER="${TAG#v}"

command -v makepkg >/dev/null || die "makepkg not found — run on an Arch host (or the archlinux CI container)"
command -v gpg >/dev/null || die "gpg not found — needed to verify signed releases"

REPO_ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
PKG_DIR="$REPO_ROOT/packaging/aur"
INSTALL_SH="$REPO_ROOT/install.sh"
REPO="duketopceo/OmaSeal"
DL_BASE="https://github.com/${REPO}/releases/download/${TAG}"

OLD_VER=$(grep -m1 '^pkgver=' "$PKG_DIR/PKGBUILD-bin" | cut -d= -f2 | tr -d '[:space:]')
[ -n "$OLD_VER" ] || die "could not read pkgver from PKGBUILD-bin"
OLD_TAG="v${OLD_VER}"

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

# --- Fetch and authenticate the checksum manifest --------------------------
curl -fsSL -o "$TMP/sha256sums.txt" "${DL_BASE}/sha256sums.txt" \
  || die "sha256sums.txt not found on release ${TAG} — is the release published?"

if curl -fsSL -o "$TMP/sha256sums.txt.asc" "${DL_BASE}/sha256sums.txt.asc" 2>/dev/null; then
  [ -n "${OMASEAL_SIGNING_FINGERPRINT:-}" ] \
    || die "release ${TAG} is signed but OMASEAL_SIGNING_FINGERPRINT is unset — cannot verify"
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
else
  [ "$ALLOW_UNSIGNED" -eq 1 ] \
    || die "release ${TAG} is unsigned (no sha256sums.txt.asc). Re-run with --allow-unsigned to accept checksum-only trust."
  echo "warning: unsigned release — trusting checksums alone (--allow-unsigned)" >&2
fi

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
SUM_SRC=$(sha256sum "$TMP/src.tar.gz" | awk '{print $1}')

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
OLD_RE=$(printf '%s' "$OLD_TAG" | sed 's/\./\\./g')
sed -i "s/${OLD_RE}/${TAG}/g" "$INSTALL_SH"

# EXPECTED_SHA256 is per-arch inside a case block: x86_64 first, aarch64
# second. Key on the case labels so order can't silently swap them.
awk -v x="$SUM_X86" -v a="$SUM_ARM" '
  /^[[:space:]]*x86_64\)/         { inx=1; ina=0 }
  /^[[:space:]]*aarch64\|arm64\)/ { inx=0; ina=1 }
  /^[[:space:]]*\*\)/             { inx=0; ina=0 }
  inx && /EXPECTED_SHA256="/ { sub(/"[0-9a-f]*"/, "\"" x "\""); inx=0 }
  ina && /EXPECTED_SHA256="/ { sub(/"[0-9a-f]*"/, "\"" a "\""); ina=0 }
  { print }
' "$INSTALL_SH" > "$TMP/install.sh" && cat "$TMP/install.sh" > "$INSTALL_SH"

# --- Regenerate .SRCINFO (makepkg keys off the literal name PKGBUILD) -------
gen_srcinfo() { # $1 pkgbuild file, $2 install file, $3 output
  local scratch
  scratch=$(mktemp -d)
  cp "$PKG_DIR/$1" "$scratch/PKGBUILD"
  cp "$PKG_DIR/$2" "$scratch/"
  (cd "$scratch" && makepkg --printsrcinfo) > "$PKG_DIR/$3"
  rm -rf "$scratch"
}
gen_srcinfo PKGBUILD omaseal.install .SRCINFO
gen_srcinfo PKGBUILD-bin omaseal-bin.install omaseal-bin.SRCINFO

# --- Self-assertions --------------------------------------------------------
if [ "$OLD_TAG" != "$TAG" ] && grep -q "$OLD_RE" "$INSTALL_SH"; then
  die "stale ${OLD_TAG} literal remains in install.sh"
fi
[ "$(grep -c 'EXPECTED_SHA256="[0-9a-f]\{64\}"' "$INSTALL_SH")" -ge 2 ] \
  || die "EXPECTED_SHA256 pins missing or empty after bump"

echo "Bumped ${OLD_TAG} -> ${TAG}:"
echo "  packaging/aur/PKGBUILD      pkgver=${NEW_VER} + source sum"
echo "  packaging/aur/PKGBUILD-bin  pkgver=${NEW_VER} + per-arch sums"
echo "  install.sh                  version literals + EXPECTED_SHA256"
echo "  .SRCINFO / omaseal-bin.SRCINFO regenerated"
