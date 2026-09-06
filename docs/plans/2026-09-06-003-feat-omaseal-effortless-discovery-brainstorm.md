---
artifact_contract: ce-unified-plan/v1
artifact_readiness: requirements-only
execution: code
product_contract_source: ce-brainstorm
title: "OmaSeal: effortless discovery and onboarding"
date: 2026-09-06
plan_type: feat
---

# OmaSeal: effortless discovery and onboarding

## Summary

OmaSeal is already a working keyring. The next problem is **adoption**: agents, plugins, browsers, and users need to know it exists, trust it, and connect to it without reading a README. This work turns OmaSeal from a CLI into a first-class platform feature that Omarchy apps and agents can discover, prompt for, and use safely.

## Requirements

### R1. Agent auto-detection
An agent (Claude, Codex, or any MCP client) must be able to detect whether `omaseal` is installed and healthy before it tries to store or retrieve a secret.

### R2. One-command onboarding
A user must be able to run a single command that wires OmaSeal into the agent or tool they are using (MCP config, PATH, plugin enablement) with clear yes/no prompts and no manual JSON editing.

### R3. Plugin handshake
Any Omarchy plugin or app must be able to ask "is OmaSeal here?" and get a stable, machine-readable yes/no answer plus version/health. It must never silently fail or leak secrets while probing.

### R4. User-facing setup prompts
When a plugin or app needs a secret and OmaSeal is not connected, the user must see an actionable prompt that offers to store the secret in OmaSeal rather than a dotfile, env var, or plain-text config.

### R5. Friendly, actionable errors
Every OmaSeal error must tell the user or agent exactly what to do next: run `omaseal doctor`, run `omaseal setup`, sign in to a vault, or enable a missing service.

### R6. Browser and app integration
BrowserOS and other Omarchy apps must auto-detect OmaSeal and offer it as the default/preferred secret store when adding credentials, with graceful fallback if it is missing.

### R7. Marketplace discoverability
The Omarchy marketplace and plugin metadata must make it obvious that an app is "OmaSeal-ready" and what the user gains by using it.

## Actors

- **A1. Omarchy user** — wants to add a secret to a plugin/app without thinking about storage.
- **A2. Agent (Claude, Codex, MCP client)** — wants to read or write secrets through a standard interface.
- **A3. Plugin / app developer** — wants to request secrets from a known, safe keyring.
- **A4. OmaSeal maintainer** — ships the binary, plugin, and MCP server.
- **A5. Omarchy shell / marketplace** — hosts and activates plugins.

## Key Flows

### F1. Agent discovers and uses OmaSeal
1. Agent starts and calls a probe (e.g. `omaseal ping --json` or checks `PATH` for `omaseal`).
2. If present and healthy, the agent registers `omaseal` as the canonical secret store.
3. When the agent needs a secret, it uses `omaseal_resolve` / `omaseal_set` via MCP or CLI.
4. If OmaSeal is missing, the agent tells the user: "Run `omaseal setup` to connect the keyring."

### F2. User runs first-time setup
1. User installs `omaseal` (AUR, release tarball, or source).
2. User runs `omaseal setup`.
3. `setup` checks health, then asks which agents/plugins to configure.
4. `setup` writes MCP JSON for the selected agents and appends `PATH` if needed.

### F3. Plugin asks to connect on first run
1. Plugin loads and calls `omaseal ping --json`.
2. If not found, the plugin shows a setup card: "OmaSeal is the Omarchy keyring. Connect it?"
3. If the user agrees, the plugin runs `omaseal setup --quiet` or opens the QML panel.
4. The plugin stores any new secret through OmaSeal.

### F4. BrowserOS stores a provider key
1. User opens AI provider settings in BrowserOS.
2. BrowserOS detects `omaseal` on the server host.
3. The checkbox "Store credentials in OmaSeal" is enabled and, if healthy, pre-checked.
4. On save, BrowserOS stores the key and keeps only the `omaseal://` reference.

### F5. Error leads to a fix
1. A call to `omaseal get` fails because `gnome-keyring-daemon` is not running.
2. The error message says: "keyring unreachable. Run `omaseal doctor` to diagnose."
3. The user runs `omaseal doctor` and sees the exact missing piece.

## Acceptance Examples

### AE1. Agent on a clean Omarchy install
A user opens Claude Code for the first time. Claude detects `omaseal` and can already call `omaseal_set` to store an OpenRouter key. The user did not edit a config file.

### AE2. Plugin without OmaSeal
A user installs a new Omarchy plugin that needs a secret. The plugin shows: "Connect OmaSeal?" The user presses yes; 10 seconds later the plugin is storing its secret safely.

### AE3. BrowserOS provider save
BrowserOS detects `omaseal` is installed and the "Store credentials in OmaSeal" checkbox is pre-checked. The user un-checks it if they prefer plaintext, but they have to confirm.

### AE4. Broken environment
A user types `omaseal get openrouter default` and sees an error that ends with "Run `omaseal doctor`". `omaseal doctor` prints a checklist with one red item and a one-line fix.

## Key Technical Decisions

### KTD1. Discovery is read-only and non-blocking
Plugins and agents must be able to probe OmaSeal without triggering a keyring unlock, secret read, or user prompt. The canonical probe is `omaseal ping --json` or `omaseal doctor --json`.

### KTD2. Auto-wiring is opt-in per agent
`omaseal setup` can suggest or write MCP configs, but it must ask before editing `~/.claude/mcp.json`, `~/.codex/mcp.json`, or any agent file. No silent modification of a user’s personal agent config.

### KTD3. Errors carry a machine-readable code plus a human hint
CLI and IPC errors should expose an `error` field (e.g. `keyring_unreachable`, `fprintd_missing`, `op_not_authenticated`) and a `help` field (e.g. `omaseal doctor`). The same message works for users and agents.

### KTD4. No plugin gets secret access by default
A plugin can ask if OmaSeal is available, but it cannot list or read secrets without user authorization. This preserves the macOS Keychain trust model: easy to connect, gated per secret.

## Scope Boundaries

### In scope
- A stable, read-only `omaseal ping` / `omaseal doctor --json` probe.
- `omaseal setup` as an interactive installer for agents and shell PATH.
- MCP install helpers for Claude and Codex.
- Better error messages with codes and `omaseal doctor` hints.
- BrowserOS auto-detection and pre-checked "Store in OmaSeal" flow.
- Plugin handshake spec and docs.
- Marketplace / README discoverability updates.

### Out of scope
- A new storage backend or encryption system.
- Cloud sync or shared team vaults.
- Silent agent take-over (all auto-wiring is opt-in).
- macOS/Windows ports.
- Premium license enforcement.

## Risks and Dependencies

### Risks
1. **Agent config paths vary.** Claude, Codex, Cursor, and others keep MCP configs in different places. Mitigation: ship per-agent commands (`omaseal mcp install claude`) and document the paths.
2. **Plugins probing too often.** A plugin could call `omaseal ping` on every load and slow startup. Mitigation: cache the probe result in the plugin and re-check only on error.
3. **False confidence from a healthy probe.** `omaseal ping` returning `ok` does not mean the keyring is unlocked. Mitigation: distinguish `available` from `unlocked` and surface `keyring-locked` errors with a prompt.
4. **Privacy of `doctor` output.** `omaseal doctor` reports installed CLI presence but not secret values. Keep it that way.

### Dependencies
- `gnome-keyring` / Secret Service already on Omarchy.
- Go 1.27+ for the new commands.
- Omarchy plugin system for the panel handshake.
- BrowserOS server for the provider key auto-detection flow.
