# OmaSeal Roadmap

## Done

- [x] `set/get/del/list/reveal` CLI on `gnome-keyring`
- [x] Quickshell `BarWidget` and `Panel` with click-to-open Logs page
- [x] MCP server with `open/ask/lock` agent trust modes
- [x] JSON IPC for other plugins
- [x] 1Password and Bitwarden import/fallback
- [x] Persistent, user-visible `~/.local/state/omaseal/omaseal.log`
- [x] Marketplace-ready packaging and manifest
- [x] Agent auto-detect + `omaseal mcp install-detected` — one command wires
  every installed agent (Claude, Cursor, Codex, Devin, OpenCode, …)
- [x] `omaseal setup` with self-verifying keyring round-trip (30s bounded)
- [x] `omaseal://service/account` references accepted by every command
- [x] Masked GUI prompt for `resolve` without a TTY (pinentry, `OMASEAL_GUI_PROMPT`)
- [x] fprintd biometric gate for `reveal`/agent unlock; panel shows
  mode/unlock state; session keep-alive
- [x] Shared `service/account` namespace with other writers
  (`omarchy-secrets-*`, keytar, seahorse): `owned` provenance flag,
  duplicate-safe set/delete
- [x] Enforced AI manifest — `omaseal manifest` robots.txt-style
  ALLOW/ASK/DENY per secret on the agent (MCP) channel; DENY also hides the
  credential from `omaseal_list`/`omaseal_stats`
- [x] Usage analytics — every read path (CLI, IPC, MCP) counted;
  `omaseal stats`, sort by `used|recent|name`, `access_count`/`last_accessed`
  in list output
- [x] Organized secrets panel — expand/collapse, vault sidebar with
  per-service counts, search, sort modes, usage badges, `external` badges,
  30s conditional clipboard clear (`omaseal clipclear`)
- [x] Bounded D-Bus calls, pooled metadata reads (~0.7s at ~700 items),
  capped access log, atomic config writes

## v0.6.0 — Marketplace stable

- Close repository issues #2, #3, #4.
- First-party Omarchy integration: `omarchy-secrets-*` commands +
  `omarchy.secrets` panel + menu entry → Discussion + PR to
  `omacom/omarchy-mac` (base `quattro`); phased fallback (commands + menu
  only) if the panel is the sticking point.
- AUR package (`omaseal-bin`) and release automation hardened.
- Land the PR stack: #6 (refs/GUI prompt/hardening) → #8 (organized panel,
  analytics, enforced manifest).

## v0.7.0 — BrowserOS and plugin ecosystem

- BrowserOS resolves the real secret at request time, stores only the
  `omaseal://` reference. (Deferred: pending `omarchy-browser` checkout.)
- `docs/namespaces.md` conventions exercised by a real consumer.

## v1.0.0 — First-party

- Shipped as an official Omarchy keyring option (port to `omacom/omarchy`
  x86 once the omarchy-mac surface lands — the code is platform-agnostic).
- Stable API, documented `service/account` conventions, and third-party
  plugin adoption.
- Panel component extraction (the single-file panel has outgrown its
  structure) and first-run `manifest init` prompt.
