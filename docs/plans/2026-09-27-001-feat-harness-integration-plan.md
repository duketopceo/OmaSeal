---
title: feat(fit): first-class integration with each detected agent harness
created: 2026-09-27
origin: "harness landscape analysis — what ships built-in vs the OmaSeal gap (session 2026-09-27)"
artifact_contract: ce-unified-plan/v1
product_contract_source: ce-plan
execution: code
---

# feat(fit): first-class integration with each detected agent harness

## Goal Capsule

**Objective.** OmaSeal already auto-installs its MCP server into seven detected
agents. "Fit" goes deeper: each harness gets the strongest secrets story its
config surface can express — keys materialized from the keyring at spawn time
instead of pasted into `env` blocks — plus one universal mechanism
(`omaseal run`) that works for every harness and every MCP server entry.

**Means.** One new CLI verb (`omaseal run`) + per-harness wiring in
`mcpinstall.go`/`setup` + a fit matrix doc.

**Authority.** Landscape facts verified 2026-09-27 (Claude Code `apiKeyHelper`
+ TTL refresh; Cursor `${env:}`/`envFile` — CLI parity flaky; Codex TOML
literal env only; OpenCode `{env:}`; Codex CLI auth now keyring-backed, same
Secret Service store we wrap).

**Stop conditions.** No secrets in any generated config — a `run` wrapper or
helper command name only. `omaseal run` uses local `Get` (never the provider
sweep or a GUI prompt inside an agent-spawned process). No harness-specific
forks of the manifest policy.

## Product Contract

### Requirements

- **R1.** `omaseal run --env NAME=svc/acct [--env ...] -- <cmd> [args...]` execs
  `<cmd>` with `NAME` set to the resolved secret. Also accepts `omaseal://svc/acct`
  as a value. Uses `Get` (local keyring only) by default — agent-spawned MCP
  children must never hit the provider sweep or pop a GUI prompt; `resolve`
  behind an explicit `--resolve` flag. Missing key → clear stderr + exit 127.
  Child env inherits the parent environment (secrets are additive).
- **R2.** Detection table gains a `fit` notion: `mcp` (server installed today),
  `run` (wrapper-supported), `helper` (native credential-helper hook). `omaseal
  doctor`/`status` reports fit level per detected agent.
- **R3.** Claude Code: `omaseal setup`/`mcp install` offers to write
  `"apiKeyHelper": "omaseal get anthropic default"` into `~/.claude/settings.json`
  when an `anthropic/*` item exists — Claude's own API key leaves the file
  system entirely. Honors `CLAUDE_CODE_API_KEY_HELPER_TTL_MS`. Opt-in only; a
  written helper is reversible via `omaseal mcp uninstall`-style removal or
  documented manual edit.
- **R4.** Codex: `[mcp_servers.*]` entries can embed `omaseal run` as the
  command (`command = "omaseal"`, `args = ["run","-e","X=svc/acct","--",...]`).
  Docs note that Codex's own CLI auth now lands in Secret Service — same store
  OmaSeal wraps — so `omaseal list` surfaces it alongside user secrets.
- **R5.** Cursor: `${env:NAME}` interpolation documented as the baseline; `run`
  wrapper recommended (interpolation is version-flaky and still requires the
  secret to live in shell env — the wrapper removes both). Cloud-agent secrets
  explicitly out of scope (platform-side vault).
- **R6.** OpenCode, agy, hermes, Gemini CLI: same `run` wrapper pattern in
  their `mcp`/mcpServers `command` field; docs matrix per harness.
- **R7.** Devin: `.devin/mcp_config.json` already installed; document `run`
  for local stdio MCP servers. Devin's managed integrations stay
  platform-side.
- **R8.** `omaseal setup` gain a "harness fit" summary line per detected agent
  (e.g. `claude: mcp ✓ apiKeyHelper ✓ run ✓`).
- **R9.** `docs/harness-fit.md`: per-harness matrix — helper hook /
  interpolation / wrapper support, recommended pattern, and the
  secrets-never-in-files claim.
- **R10.** `run` does not consult the agent manifest — shell access already
  implies user-level access (the manifest gates the MCP tool channel, not the
  process environment). Documented in the doc + `--help`.

## Implementation Units

### U1. `omaseal run` — env injection verb

- **Files:** `engine/run.go` (new), `engine/main.go` (dispatch), `engine/run_test.go` (new)
- **Approach:** parse `--env NAME=ref` pairs (repeatable) + `omaseal://` values;
  `Get` each ref; `syscall.Exec`-style spawn preserving stdio so an MCP child
  sees a clean JSON-RPC channel. Exit codes: 127 ref-miss / spawn-fail, child
  exit code otherwise. `--resolve` switches Get→Resolve for explicit cases.
- **Tests:** name=ref parsing (bad pairs rejected), omaseal:// values, missing
  ref → 127 + stderr naming the key, env propagation (child sees injected var),
  `--` terminator required before command, no `Get` call leaks the secret to
  stdout/logs.

### U2. Fit matrix in detection + status surfaces

- **Files:** `engine/mcpinstall.go`, `engine/doctor.go`, `engine/setup.go`
- **Approach:** extend `agentSpec` with `fit []string` (mcp/run/helper) +
  `helperNote` field; doctor line prints per-agent fit; `setup` summary lists
  it. Claude gets `helper` + note about apiKeyHelper.
- **Tests:** status JSON carries `fit` field; doctor output includes fit line.

### U3. Claude `apiKeyHelper` offer

- **Files:** `engine/setup.go`, `engine/mcpinstall.go`
- **Approach:** during `omaseal setup` (TTY-gated like the Jev offer), if an
  `anthropic/*` item exists and no apiKeyHelper is set, offer to write
  `"apiKeyHelper": "omaseal get anthropic default"` into
  `~/.claude/settings.json` — merge, don't clobber; preserve file mode; skip if
  already set (even to another value — report instead).
- **Tests:** merge into existing settings.json preserves other keys; skips
  when helper already present; persists decline.

### U4. Docs + examples

- **Files:** `docs/harness-fit.md` (new), `README.md` (one section + matrix link)
- **Content:** matrix per harness (helper/interpolation/wrapper), copy-paste
  `run` entries for each config format (JSON mcpServers, Codex TOML, OpenCode
  `mcp`, opencode command array), the R10 threat-model note, and the
  "secrets never in config files" claim verified by grep of generated configs.

### U5. Codex shared-store note + AUR/plugin docs

- **Files:** `docs/harness-fit.md`, `docs/packaging.md` (if needed)
- **Content:** Codex CLI auth (encrypted-local-secrets → OS keyring → Secret
  Service on Linux) shares our store — visible via `omaseal list`; no code,
  documentation only.

## Sequencing

U1 → U4 → U2 → U3 → U5. U1 blocks everything (the wrapper is the shared
mechanism); U3 is independent of U2.

## Risks

- **R-a.** `syscall.Exec` on Linux is clean; Windows path doesn't exist —
  `run` is Linux-only by product boundary anyway (guarded like providers).
- **R-b.** `apiKeyHelper` runs `omaseal get` — the local ungated path; key
  comes from keyring, not the file, and helper output goes only to Claude's
  auth channel. Acceptable — documented.
- **R-c.** A `run`-wrapped MCP server inherits the *agent's* env plus
  injected secrets — if the agent's env is hostile it sees them anyway;
  no worse than today's env blocks. Documented in R10.
- **R-d.** Cursor `${env:}` flakiness is why `run` is the recommendation —
  don't try to fix upstream, document around it.

## Test scenarios (cross-unit)

- `omaseal run -e T=test/svc -- sh -c 'echo $T'` prints the secret locally,
  exit 0; missing key → 127 naming the ref.
- mcp install for codex writes a `run` args array parseable by Codex TOML.
- doctor lists `claude` with helper/run/mcp fit when detected.
- setup on a machine with `anthropic/default` offers apiKeyHelper once,
  writes merged JSON, declines persist.
