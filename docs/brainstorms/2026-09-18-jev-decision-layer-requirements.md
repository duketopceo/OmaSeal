# Jev Decision Layer — Requirements

Date: 2026-09-18
Status: settled in brainstorm; passed 7-persona doc-review on 2026-09-18 — Jev's 0.62 completeness score was dispositioned by that review round (gaps found and resolved inline below).
Driver: Jev as an optional decision layer inside OmaSeal, plus Jev used while *developing* OmaSeal (local hook + CI). Phases 1–2 ship **advisory** capabilities; the "decision layer" claim lands only with Phase 3.

## Product intent

OmaSeal guards secrets through a manifest/policy layer that today is written and reviewed entirely by hand. Jev (TypeSafe System One via OpenRouter, `~typesafe/jev-latest`, decisions endpoint) becomes an optional advisor that audits the manifest, watches access patterns, and — later, optionally — gates reads at runtime. Jev proposes; the human disposes. Core secret operations never depend on it.

## Architecture (settled)

**External companion (Approach B, revised)** — Jev is a *local script*, not an embedded package and not an MCP agent.

- Post-review correction: the codebase has **no per-agent identity** — `ai-manifest.txt` rules are per-secret (`ALLOW/ASK/DENY`), MCP carries no caller identity, and `jev` is not a recognized agent. The original "Jev authenticates as an agent through the policy surface" design is therefore descoped: Jev reads the manifest file and `omaseal` metadata output **directly, as the local user** — exactly as `jev-triage` already does. Per-agent identity/permissions is a separate deferred workstream, not a dependency of this feature.
- Consequence: `manifest.propose`/`manifest.apply` are **human-run CLI commands**, not agent-callable tools — the human gate is structural (no agent surface can reach apply).
- Audit metadata reads MUST return the **unfiltered** inventory — the manifest's own DENY filtering must not blind the auditor, or a wrong DENY rule conceals the evidence of its own error. For the local path this means reading the keyring inventory + manifest directly rather than going through the manifest-filtered `omaseal list` path.
- The Jev companion is a separate, optional artifact (OpenRouter-facing script) that consumes the audit's local findings and produces the rationale/confidence layer.
- The omaseal binary contains **no OpenRouter client**. Supply-chain surface of the shipped binary is unchanged.
- The read-hook trigger lives in-process because it is the only trigger that needs neither a daemon nor user action — the watch work itself is external. (In-process interception, which is what would actually require code in the read path, is Phase 3.)

Chosen over embedded (A) on Jev's own review: external/mediated 0.76 vs hybrid 0.14 vs embedded 0.10. Top embedded-risk: external AI inside the secrets daemon's trust boundary (0.54) + bypass of the gates it strengthens (0.42). Top external-risk: circularity — Jev governed by the manifest it audits (0.97) — mitigated by R6 below.

## Requirements

### R1 — Strictly optional, setup-offered

- `omaseal setup` detects a usable OpenRouter credential in the keyring (probe `openrouter/jev` then `openrouter/default`) and offers enablement during onboarding. The offer must state what enabling consents to: metadata (never secret values) is sent to OpenRouter for audit and watch. Declining is persisted — the offer does not re-ask on every setup run.
- Marketplace listing, README, and privacy/consent text are updated to disclose the opt-in metadata path to OpenRouter before the feature ships or is announced (current listing claims "no cloud dependency").
- Default is OFF. Nothing Jev-related runs, spawns, or phones home unless enabled.
- An explicit post-onboarding enable/disable surface exists (`omaseal jev enable|disable` or equivalent): enable writes the defined enabled-state artifact (e.g. a config/state file the companion checks); disable removes it and disarms the read-hook. What represents "enabled" is defined — the hook and the companion have a concrete gate to check.

### R2 — Phase 1: manifest audit (propose → review → apply)

- `omaseal manifest audit` ships as a **built-in local linter for every user, no OpenRouter key required** — it mechanically detects stale secrets, dead rules (matching nothing), uncovered items (no matching rule), and item-level advisories (e.g. stale keyring items). Jev, when enabled, adds the rationale/confidence layer on top of the same findings.
- With Jev enabled, the audit produces a **proposal file** — a diff of suggested manifest rules with per-item rationale + confidence.
- User reviews the file; a second command applies it (`omaseal manifest apply <proposal>`). `manifest apply` renders the parsed rule changes it will commit and refuses when the proposal file or the manifest changed since generation — the human gate binds to applied content, not to the file path or a remembered review.
- Every rule change passes a human gate. No auto-apply, no confidence threshold that bypasses review.
- An audit may return **"no changes"** — no manufactured diffs. Proposal volume/precision is bounded so the review stream stays worth reading; a noisy stream trains rubber-stamping, the exact failure R6 guards against.

### R3 — Phase 2: access-log watch (read-hook, detached)

- Every Nth `get`/`resolve` spawns a detached background watch: Jev scores the new window of `omaseal.log` for anomalous access patterns.
- The watch sends **parsed access tuples** (timestamp, operation, service, account) extracted from the log window — not raw log text — so the no-secret-values invariant holds by construction, not by log-format discipline.
- **Detached, always** (Jev: 0.99). A `get` never waits on a network call; watch failure is silent-skip (debug log only).
- Rate-limited by the counter; no scheduler, daemon, or timer.
- Anomalies surface as a **desktop notification** (`notify-send`) — immediate, outside the terminal. Alerts carry a minimum actionable payload — affected service/account(s), the window, and a next-step pointer (`omaseal logs` or a persisted report) — using fixed-structure bounded content, not free model prose. With no notification daemon, anomalies persist to state and surface on the next omaseal run instead of evaporating.

### R4 — Phase 3: runtime trust gate (second opt-in, deferred)

- Optional policy mode where Jev scores a `resolve`/`get` before it returns.
- Separate enable beyond R1 — sits on the hot path by definition.
- Fail-open vs fail-closed posture, latency budget, and cache semantics are decided when this phase is planned — not now.

### R5 — Privacy + dependency invariants

- Jev **never** receives secret values — metadata only (service, account, label, attribute keys, usage counts, timestamps).
- Core ops (`get`/`set`/`resolve`/`mcp` serving) never depend on Jev availability. Jev down / key missing / rate-limited → silent skip.
- Uses the existing `openrouter/jev` budget-limited subkey; falls back to `openrouter/default`.
- Everything Jev consumes and produces is **untrusted model I/O** — log windows, proposal rationale, and model output can carry injected or manipulative text; inputs are parsed/bounded and outputs are rendered in fixed structures, never trusted as instructions or verbatim UI.
- A discoverable status surface (doctor check or status line) reports Jev enabled state, last successful run, and blocked reason. An enabled-but-policy-blocked Jev must not be indistinguishable from "no anomalies"; silent-skip applies to availability, not to policy denial.

### R6 — Circularity guard (hard mechanism, not just UX)

- `manifest.apply` **rejects any proposal that modifies the `jev` agent's own policy entry** — Jev cannot edit its own leash, even if a human rubber-stamps it. The same rejection covers proposals touching rules that govern jev's own credential path (`openrouter/*`), including indirect self-edits via wildcards.
- Proposals that would *remove* access for another agent are annotated prominently in the proposal file.
- Every **capability-expanding** change requires explicit per-item confirmation — DENY→ALLOW or ASK→ALLOW flips, wildcard broadening, and new agent entries. Escalation is the dangerous direction; it cannot hide inside a large benign diff.

### R7 — Dev-side: hook + CI

- **Local hook**: a **pre-commit** hook (only pre-commit can see the staged diff) runs Jev risk-scoring — advisory only; it prints the score and suggestions and never blocks the commit.
- **CI**: a workflow step on PRs that:
  - always posts an advisory comment (risk score, tests-needed, secrets-paths-touched; requires `pull-requests: write` on `GITHUB_TOKEN`), and
  - **fails** when the diff touches keyring/credential/security paths without accompanying tests (advisory + secrets gate). The fail-gate is **deterministic** — paths-touched and tests-present are checked mechanically; Jev's score is advisory-only, never the gating signal. On Jev API error the job posts the advisory comment marked "scan unavailable" and does not fail (fail-open — an external outage can never block merges).
- CI uses a repo secret (`OPENROUTER_API_KEY`); the workflow must be visibly dev-only for the marketplace reviewer — it ships no runtime code path and touches no user install.

## Phasing

| Phase | Ships | Depends on |
|---|---|---|
| 1 | `manifest audit` (local lint), proposal format, `manifest apply` (human CLI), setup offer, jev companion artifact (OpenRouter-facing script), R6 guard | nothing — first PR(s) |
| 2 | read-hook watch (detached), anomaly scoring, notify-send alerts | nothing new — the watch reads `omaseal.log` directly |
| 3 | runtime trust gate | Phase 2 operational experience; separate opt-in |

Phases 1 and 2 are independently deliverable. Phases 2–3 proceed only after real Phase-1 audits demonstrated applied value — not on schedule.

Dev-side (R7) is **independent of Phases 1–3** — it reuses the `jev-triage`-style decision-sweep, not the MCP surface — and may ship first.

## Test requirements (Jev: "both", 0.97)

- Unit tests: proposal parsing/diff, R6 self-edit rejection, read-hook counter + spawn gating, silent-skip on Jev failure, metadata-only payload construction (assert no secret value field ever serializes).
- Mocked OpenRouter HTTP for decision-parsing paths.
- Env-gated live checks against the real decisions endpoint (skip without `OMASEAL_JEV_IT` + key).
- CI gate itself tested (fixture diff touching `engine/keyring*.go` without tests → fail; with tests → pass).

## Open questions for planning (not product-blocking)

- Proposal file format + location (`~/.local/state/omaseal/jev/`?) and whether `apply` is interactive or flag-gated.
- Watch counter N, log-window cursor state, notification de-dup/cooldown.
- Where the jev agent artifact lives (repo `contrib/`? shipped helper? keep `jev-triage`-adjacent?) and how setup installs/registers it.
- CI: which repo, which workflow file, comment format.
- Phase 3 posture — deferred by design.

## Explicit non-goals

- No OpenRouter client linked into the `omaseal` binary.
- No auto-apply of manifest changes at any confidence.
- No MCP exposure of secret *values* to the jev agent (or anything else).
- No blocking `get`/`resolve` on Jev (except opt-in Phase 3).
- No change to the frozen `main` SHA before marketplace attestation — Jev work lands on branches/PRs like everything else.

## Evidence trail

- Jev review of this spec (gen-dec-1789787495): architecture B 0.76; circular-risk 0.97 → R6; detached watch 0.99 → R3; tests both 0.97 → Test requirements; completeness 0.62.
- Prior Jev review of keyring-migrate spec correctly flagged delete-by-default (0.24 safe), headless hang (vendored prompt handler waits forever — confirmed in code), dry-run need — every flag became a shipped gate.
- Live audit precedent: the by-hand Jev manifest audit found `test/service` staleness and policy gaps — the workflow R2 encodes.
