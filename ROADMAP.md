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
- [x] Fail-closed user-presence gate for `reveal`/agent unlock — fprintd,
  else GUI confirm, else deny; `agent mode ask --ungated` opt-out (v0.4.0)
- [x] Shared `service/account` namespace with other writers
  (`omarchy-secrets-*`, keytar, seahorse): `owned` provenance flag,
  duplicate-safe set/delete
- [x] Enforced AI manifest — `omaseal manifest` robots.txt-style
  ALLOW/ASK/DENY per secret on the agent (MCP) channel; DENY also hides the
  credential from `omaseal_list`/`omaseal_stats`; quoted patterns address
  names with whitespace (`DENY "svc/acct name"`)
- [x] Usage analytics — every read path (CLI, IPC, MCP) counted;
  `omaseal stats`, sort by `used|recent|name`, `access_count`/`last_accessed`
  in list output
- [x] Organized secrets panel — expand/collapse, vault sidebar with
  per-service counts, search, sort modes, usage badges, `external` badges,
  30s conditional clipboard clear (`omaseal clipclear`)
- [x] Bounded D-Bus calls, pooled metadata reads (~0.7s at ~700 items),
  capped access log, atomic config writes
- [x] `omaseal keyring migrate` + doctor `keyring-encryption` check —
  plaintext keyrings detected and re-encrypted into `login`
- [x] Fail-closed agent policy — missing `agent.json` defaults to `ask`
- [x] Provider negative caching — repeated misses memoized (~60s) so pollers
  don't spawn `op`/`bw` per tick (#23)
- [x] Harness integration — `omaseal run -e NAME=svc/acct -- <cmd>`
  materializes secrets into a spawned MCP server's env via `exec(3)`;
  per-harness fit hints in `mcp status`/`doctor`; opt-in `apiKeyHelper`
  wiring for Claude Code (#24)
- [x] Pentest hardening — weakening mode changes fail closed without a
  presence mechanism; confirm-prompters resolve from fixed absolute paths
  only (no PATH shim adoption); presence gate extended to
  `manifest init --force`, `manifest apply` expansions, `jev enable`;
  shared `parseRuleLine` across scanner/generator/applier; audit flags
  dead tokens, shadowed rules, unquoted names (#25)

## v0.6.0 — Marketplace stable

- Marketplace revalidation: HEAD is `d911b29` — a 4-agent pentest found and
  this tree fixed real bypasses (env-suppressed presence gate, PATH
  prompter shims, dead DENY rules on spaced names). Residual design limits
  are documented in README: manifest/presence govern the MCP+IPC agent
  channel; same-uid shell processes are outside confinement by design.
- Issue #12: AUR `omaseal-bin` — registration portal closed; blocked on a
  human browser step when registration reopens.
- First-party Omarchy integration: `omarchy-secrets-*` commands +
  `omarchy.secrets` panel + menu entry → Discussion + PR to
  `omacom/omarchy-mac` (base `quattro`); phased fallback (commands + menu
  only) if the panel is the sticking point.
- Release automation hardened (`release.yml`).

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

## Confinement (research track)

The pentest's deepest finding is architectural, not a bug: every control
lives in same-uid files and env, so a shell-capable process can write
`agent.json`/`session`/manifest or call Secret Service directly. The gates
deter agents using OmaSeal's interfaces; they cannot confine arbitrary
same-uid code. True confinement needs a different-uid policy daemon (a
setuid/polkit-brokered resolver, or sandboxed agent processes). Worth a
design spike before v1.0 if agent isolation becomes a requirement rather
than a convenience.

Partially mitigated: the audit log is hash-chained (`logs verify` /
`logs seal`), so tampered *history* is provable once a head is anchored
off the state dir — detection of rewriting, not prevention of same-uid
writes.
