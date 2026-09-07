---
artifact_contract: ce-unified-plan/v1
artifact_readiness: implementation-ready
execution: code
product_contract_source: 2026-09-06-003-feat-omaseal-effortless-discovery-brainstorm
plan_type: feat
title: "OmaSeal: effortless discovery and onboarding"
date: 2026-09-06
---

# OmaSeal: effortless discovery and onboarding

## Summary

OmaSeal works, but it is not yet obvious to agents, plugins, browsers, or users that it exists or how to connect it. This plan adds the minimal surface to make OmaSeal feel like a native platform feature: a fast, read-only `ping`, an interactive `setup` that wires agents, machine-readable error codes, and a QML status dot. Later work can add per-app prompts and BrowserOS server-side auto-detect.

## Problem Frame

- Agents do not know whether `omaseal` is installed before they try to use it.
- Users must manually edit `~/.claude/mcp.json` or `~/.codex/mcp.json` to connect it.
- Plugins have no stable probe; they would have to shell out to `omaseal` and parse ambiguous text.
- Errors say "keyring error" but do not tell the user or agent the one command that fixes it.
- The panel does not signal when the environment is broken.

## Scope Boundaries

### In scope

1. `omaseal ping --json` and `omaseal doctor --json` for fast, read-only health probes.
2. `omaseal setup` becomes interactive: choose agents to wire, then write the MCP config and PATH hint.
3. `omaseal mcp install <claude|codex>` for one-command agent wiring.
4. CLI and IPC errors include a machine-readable `code` and a `help` command.
5. QML `BarWidget` shows a warning dot when `omaseal doctor` fails.
6. `docs/onboarding.md` updated with the new flows.

### Out of scope

- BrowserOS server-side auto-detect and pre-check UI (deferred; depends on BrowserOS PR).
- One-time plugin setup prompts in `Panel.qml` (deferred; needs Omarchy plugin API).
- Marketplace badge system.

## Key Technical Decisions

### KTD1. `ping` is the canonical probe
`omaseal ping --json` returns a small JSON object. Plugins and agents use this instead of parsing human output or reading `PATH`. It never unlocks the keyring, so it is safe to call at startup.

### KTD2. `setup` writes agent config files only with explicit yes
We ask before editing `~/.claude/mcp.json` or `~/.codex/mcp.json`. The alternative (silent edits) breaks user trust; opt-in keeps the macOS Keychain feel.

### KTD3. Errors carry `code` and `help` fields
All CLI/IPC failures return a stable `code` and a `help` command. This makes agents self-correcting and users find the fix immediately.

### KTD4. QML panel reads health with `ping`
The `BarWidget` runs `omaseal ping --json` on a timer. It does not access secret values; it only shows a dot if the keyring/fprintd/dependency is missing.

## Implementation Units

### U1. Add `omaseal ping` and `omaseal doctor --json`

**Goal:** Provide a fast, safe, machine-readable health probe.

**Files:**
- `engine/ping.go`
- `engine/ping_test.go`
- `engine/doctor.go` (add `--json`)
- `engine/main.go` (add `ping` subcommand)

**Approach:**
- `omaseal ping --json` returns the version/commit and reports a small set of checks split into required and optional groups:
  - **Required:** `gnome-keyring-daemon` (Secret Service) is reachable, and the `omaseal` binary is on `PATH`.
  - **Optional:** `fprintd` service, `op` (1Password CLI), `bw` (Bitwarden CLI).
- Output JSON: `{ "ok": false, "version": "dev", "commit": "...", "checks": [...], "degraded": false, "help": "omaseal doctor" }`. `ok` is true only when all required checks pass; optional failures set `degraded: true` and include a `help` command.
- `omaseal doctor --json` prints the same JSON, but always returns the full checklist.
- Use 2-second timeouts on all external commands to avoid hanging the probe.

**Test scenarios:**
- `ping` returns valid JSON when the keyring is missing.
- `ping` returns `ok: true` on a healthy Omarchy session.
- `doctor --json` contains all check names and booleans.

**Verification:** `go test ./...` and manual `omaseal ping --json`.

---

### U2. Interactive `omaseal setup`

**Goal:** Walk the user through connecting agents and fixing `PATH`.

**Files:**
- `engine/setup.go` (rewrite)
- `engine/setup_test.go`

**Approach:**
- After running `doctor`, ask which agents to wire: `claude`, `codex`, both, or none.
- For each chosen agent, write `~/.<agent>/mcp.json` with `omaseal mcp` config, creating the directory if needed.
- If `~/.local/bin/omaseal` is not on `PATH`, print the exact `export PATH` line and optionally append it to `~/.bashrc` / `~/.zshrc` with user confirmation.
- Add `--yes` / `--no-path` flags for non-interactive installs.

**Test scenarios:**
- `setup --yes` with a known agent writes the expected MCP JSON file.
- `setup` with a missing home directory creates it.
- `setup` does not write without `--yes` in a test.

**Verification:** `go test` with a temp `$HOME`.

---

### U3. `omaseal mcp install <agent>`

**Goal:** One command to wire a specific MCP client.

**Files:**
- `engine/mcpinstall.go`
- `engine/mcpinstall_test.go`
- `engine/main.go` (add `mcp install` subcommand)

**Approach:**
- `omaseal mcp install claude` writes `~/.claude/mcp.json`.
- `omaseal mcp install codex` writes `~/.codex/mcp.json`.
- Merge with existing `mcpServers` without overwriting other servers.
- Validate JSON before writing and print the path.

**Test scenarios:**
- Merges `omaseal` into an existing config.
- Creates a new config from scratch.
- Returns an error for an unknown agent.

---

### U4. Machine-readable error codes

**Goal:** Every CLI and IPC failure tells the user or agent what to do.

**Files:**
- `engine/keyring.go`
- `engine/fprintd.go`
- `engine/providers.go`
- `engine/ipc.go`

**Approach:**
- Wrap errors with a `code` string and a `help` string.
- `omaseal get` prints to `stderr`: `error: keyring_unavailable (is gnome-keyring-daemon running? run 'omaseal doctor')`.
- IPC JSON errors include `{"error":"...","code":"...","help":"..."}`.

**Test scenarios:**
- Missing keyring prints the code and `help`.
- Missing fprintd in `reveal` does not crash; it prints `fprintd_unavailable`.
- Unauthenticated `op` prints `op_not_authenticated` with `op signin`.

---

### U5. QML `BarWidget` status dot

**Goal:** The panel shows when OmaSeal needs attention.

**Files:**
- `BarWidget.qml`

**Approach:**
- On `onVisibleChanged` and a 30-second timer, run `omaseal ping --json`.
- If `ok: false`, show a small warning dot and a tooltip: "OmaSeal needs setup. Run `omaseal doctor`".
- If `ok: true`, show a normal lock icon.

**Test scenarios:**
- Panel loads without QML errors.
- Warning dot appears when `gnome-keyring-daemon` is not running (manual).

---

### U6. Update onboarding and README

**Goal:** Docs match the new commands.

**Files:**
- `docs/onboarding.md`
- `README.md`

**Approach:**
- Add `omaseal ping --json` and `omaseal mcp install <agent>` examples.
- Document the error code format.

## Output Structure

```
OmaSeal/
├── engine/
│   ├── ping.go
│   ├── ping_test.go
│   ├── mcpinstall.go
│   ├── mcpinstall_test.go
│   ├── setup_test.go
│   └── main.go
├── BarWidget.qml
├── docs/onboarding.md
├── README.md
└── docs/plans/2026-09-06-004-feat-omaseal-effortless-onboarding-plan.md
```

## Risks & Dependencies

### Risks

1. **QML `Process` hangs.** `ping` uses only non-blocking external checks, but a slow `gnome-keyring-daemon` could still block. Mitigation: all probe commands have 2-second timeouts.
2. **Agent config path drift.** Claude Code, Codex, Cursor, and others may move their config directories. Mitigation: detect known paths, document them, and fall back to printing the JSON for the user to paste.
3. **Overwriting agent configs.** Merging `mcpServers` preserves other entries, but malformed existing JSON will fail with a clear message.

### Dependencies

- Existing `doctor` and `setup` commands.
- `go-keyring` and `godbus` for non-blocking probes (no direct calls in `ping`).
- Quickshell `Process` for the panel.
