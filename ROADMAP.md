# OmaSeal Roadmap

## Done

- [x] `set/get/del/list/reveal` CLI on `gnome-keyring`
- [x] Quickshell `BarWidget` and `Panel` with click-to-open Logs page
- [x] MCP server with `open/ask/lock` agent trust modes
- [x] JSON IPC for other plugins
- [x] 1Password and Bitwarden import/fallback
- [x] Persistent, user-visible `~/.local/state/omaseal/omaseal.log`
- [x] Marketplace-ready packaging and manifest

## v0.3.0 — Agent setup that just works

- Auto-detect installed agents (Claude, Cursor, Codex, Devin, OpenCode, etc.).
- `omaseal mcp install-detected` to wire the MCP server into every detected
  agent without overwriting unrelated servers.
- `omaseal setup` reports detected/installed/missing agents and next steps.
- Goal: one command after install, every agent can use OmaSeal.

## v0.4.0 — Biometric and session polish

- Panel status for current `agent mode`, `unlock until` time, and unlock action.
- `fprintd` availability notice in the panel and `doctor` output.
- Optional session keep-alive with inactivity timeout.

## v0.5.0 — BrowserOS and plugin ecosystem

- `omaseal://service/account` reference format for BrowserOS providers.
- BrowserOS resolves the real secret at request time, stores only the reference.
- Shared `service/account` namespace guide for Omarchy plugin authors.

## v0.6.0 — Marketplace stable

- Close repository issues #2, #3, #4.
- CI build/lint workflow.
- First-party Omarchy integration PR.
- AUR package and release automation hardened.

## v1.0.0 — First-party

- Shipped as an official Omarchy keyring option.
- Stable API, documented `service/account` conventions, and third-party plugin
  adoption.
