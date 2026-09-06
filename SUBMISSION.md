# Omarchy Plugin Marketplace Submission — Oma Ring

## Issue Title

[Plugin]: Oma Ring

## Repository URL

https://github.com/duketopceo/oma-ring

## Category

System

## Tags

keyring, secrets, security, 1password, bitwarden, mcp

## Maintainer notes

**What it does:**
Oma Ring is a first-party Omarchy keyring that gives the desktop a macOS
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
- MCP stdio server for agents: `oma_ring_get`, `oma_ring_resolve`,
  `oma_ring_set`, `oma_ring_delete`, `oma_ring_list`.
- JSON IPC surface for other Quickshell/Omarchy plugins.

**Installation:**

```sh
cd engine
go build -o oma-ring .
install -Dm755 oma-ring ~/.local/bin/oma-ring
omarchy plugin add https://github.com/duketopceo/oma-ring.git --enable
```

**Removal:**

```sh
omarchy plugin disable io.github.duketopceo.oma-ring
omarchy plugin remove io.github.duketopceo.oma-ring
rm -f ~/.local/bin/oma-ring
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
