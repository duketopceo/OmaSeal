# OmaSeal launch tease

## X / 280-character

Your API keys are about to get the macOS Keychain treatment on Omarchy. First-party, local, fingerprint-gated, and ready for x86_64 + ARM. Meet OmaSeal — the Omarchy-native keyring.

`#Omarchy` `#OmaSeal` `#Linux` `#Keychain` `#OpenSource`

## LinkedIn

macOS users have had Keychain for decades. On Linux, API keys still end up in plain-text dotfiles, shared env files, or shell history.

OmaSeal fixes that for Omarchy.

It is a first-party, local-only keyring built on the existing `gnome-keyring` / Secret Service stack. It comes with:
- a clean `omaseal` CLI and JSON IPC surface,
- a Quickshell panel for browsing, adding, and copying credentials,
- optional `fprintd` biometric gating via `omaseal reveal`,
- automatic provider-key resolution with fallback to 1Password or Bitwarden,
- an MCP stdio server for Claude, Codex, Cursor, Devin, Agy, and Hermes,
- multi-arch `x86_64` and `aarch64` releases,
- signed tarballs and AUR packages,
- an atomic install/upgrade path that keeps the previous binary for rollback.

No new crypto primitive. No cloud dependency. Just a standard namespace and interface so Omarchy apps, agents, browsers, and plugins can stop inventing their own secret storage.

## Changelog / release blurb

OmaSeal: first-party Omarchy keyring plugin and CLI built on `gnome-keyring` / Secret Service, with a Quickshell panel, biometric `reveal`, MCP stdio server, and 1Password/Bitwarden fallback resolution.

## Mastodon / Bluesky

OmaSeal is the Omarchy-native keyring: local-first, Secret Service-backed, fingerprint-gated `reveal`, MCP support for every major agent, and x86_64 + aarch64 releases. No more API keys in dotfiles.

`#Omarchy` `#OmaSeal` `#Linux` `#Privacy`
