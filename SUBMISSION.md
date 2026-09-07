# Omarchy Plugin Marketplace Submission — OmaSeal

## Issue Title

[Plugin]: OmaSeal

## Repository URL

https://github.com/duketopceo/OmaSeal

## Category

System

## Tags

keyring, secrets, security, 1password, bitwarden, mcp

## Maintainer notes

**What it does:**
OmaSeal is a first-party Omarchy keyring that gives the desktop a macOS
Keychain-style secret store. It uses the existing `gnome-keyring` / Secret
Service stack and adds an Omarchy-native CLI, Quickshell panel, `omarchy-shell`
JSON IPC, and MCP server so any plugin or agent can store and request secrets
without inventing its own storage.

**Key features:**
- `get / set / delete / list` over `gnome-keyring`.
- `resolve` with automatic fallback to 1Password (`op`) and Bitwarden (`bw`),
  caching locally.
- `reveal` with a best-effort `fprintd` fingerprint gate.
- Quickshell bar widget and panel for browse/add/copy/delete.
- MCP stdio server for agents: `omaseal_get`, `omaseal_resolve`,
  `omaseal_set`, `omaseal_delete`, `omaseal_list`.
- JSON IPC surface for other Quickshell/Omarchy plugins.

**Installation:**

From AUR (recommended on Omarchy/Arch):

```sh
yay -S omaseal
# or for the prebuilt binary
yay -S omaseal-bin
```

From release:

```sh
ARCH=$(uname -m)
case "$ARCH" in
  x86_64)  TAR=omaseal-linux-x86_64.tar.gz ;;
  aarch64) TAR=omaseal-linux-aarch64.tar.gz ;;
  *) echo "Unsupported arch: $ARCH" >&2; exit 1 ;;
esac
curl -fsSL -O "https://github.com/duketopceo/OmaSeal/releases/latest/download/$TAR"
curl -fsSL -O "https://github.com/duketopceo/OmaSeal/releases/latest/download/sha256sums.txt"
curl -fsSL -O "https://github.com/duketopceo/OmaSeal/releases/latest/download/sha256sums.txt.asc"
sha256sum -c --ignore-missing sha256sums.txt
gpg --verify sha256sums.txt.asc sha256sums.txt
tar -xzf "$TAR"
install -Dm755 omaseal-linux-"$ARCH"/omaseal ~/.local/bin/omaseal
omarchy plugin add https://github.com/duketopceo/OmaSeal.git --enable
```

Build from source:

```sh
cd engine
go build -o omaseal .
install -Dm755 omaseal ~/.local/bin/omaseal
omarchy plugin add https://github.com/duketopceo/OmaSeal.git --enable
```

**Removal:**

```sh
omarchy plugin disable io.github.duketopceo.omaseal
omarchy plugin remove io.github.duketopceo.omaseal
rm -f ~/.local/bin/omaseal
```

**Permissions / dependencies:**
- Requires `gnome-keyring-daemon` (Secret Service provider). Already on Omarchy.
- Optional 1Password CLI (`op`) or Bitwarden CLI (`bw`) for import/resolve.
- Optional `fprintd` for the biometric `reveal` gate.
- Uses `wl-copy` for the panel's copy-to-clipboard action.

**Privacy / consent:**
- Secrets are stored only in the local Secret Service collection; no cloud or
  network is used by the core keyring.
- 1Password/Bitwarden calls are local CLI invocations; the bridge caches the
  secret in `gnome-keyring` after the first resolution.
- `list` returns metadata only; secret values are never printed by default.

**License:**

MIT
