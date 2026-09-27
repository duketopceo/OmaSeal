---
title: fix(agent): presence-gated agent unlock — fail closed when biometric absent
created: 2026-09-26
origin: marketplace security review, omacom/omarchy-plugin-marketplace#5620 comment 2026-09-22
artifact_contract: ce-unified-plan/v1
product_contract_source: ce-plan-bootstrap
execution: code
---

# fix(agent): presence-gated agent unlock — fail closed when biometric absent

## Goal Capsule

**Objective.** On machines with no usable biometric hardware — the common case,
including this fleet's Asahi ARM box — the `ask` agent policy can no longer be
unlocked by any process that simply runs `omaseal agent unlock`. Unlock and
`reveal` require a confirmation the user physically performs (fingerprint or a
GUI dialog), unless the user has deliberately opted into ungated operation.
This is the exact bar the marketplace reviewer set for attestation on #5620.

**Means.** A single `requireUserPresence` chain — fprintd verify → GUI confirm
(pinentry `CONFIRM` / `zenity --question`, spawned by omaseal on the display
server, never the caller's stdin) → deliberate `allow_ungated` policy flag →
deny — applied to `agent unlock` and `reveal` (KTD1, KTD2).

**Authority.** The reviewer's five requirements in
omacom/omarchy-plugin-marketplace#5620 (2026-09-22 comment) outrank product
taste here; R-IDs below carry them verbatim in intent.

**Stop conditions.** Do not weaken the chain to pass CI (no stubbing the deny
path in production code). Do not gate `get`/`resolve`/IPC (KTD4 — out of the
threat boundary). Stop and flag if the only workable confirm channel turns out
to be caller-controllable.

**Profile.** Standard-depth security fix. Branch off `main` (`20a19ae`, the
frozen marketplace SHA); the merged commit becomes the new validation target.

## Product Contract

### Summary

`omaseal agent unlock` and `omaseal reveal` currently call `FprintdVerify`,
which returns success whenever fprintd is absent, inactive, deviceless, or
errors — so `ask` mode is unlockable by any caller on most real machines, and
`reveal`'s advertised biometric gate is a no-op there too. This plan adds one
shared user-presence gate with a fail-closed posture and a deliberate ungated
opt-in, wires both commands to it, surfaces which mechanism applies in
status/doctor, and proves every non-presence state yields no session and no
secret.

### Problem Frame

The marketplace reviewer's finding (verified against the code): `UnlockAgent`
(`engine/agent.go:245-267`) gates only on `FprintdVerify`
(`engine/fprintd.go:58-98`), which deliberately returns nil for every
unavailable/unenrolled state. A prompt-injected agent that can invoke the
advertised CLI unlocks `ask` itself, then reads the full keyring through MCP
tools for the session window — no user grant, no presence. `omaseal-max` and
most laptops without fingerprint readers are live examples. `reveal`
(`engine/main.go:269-287`) has the identical fail-open shape on the same
gate.

### Key Decisions

- **The gated boundary is the advertised agent path, not the whole CLI**
  (planning decision — see KTD4). `get`/`resolve`/IPC stay ungated; gating
  them buys nothing against the reviewer's threat model. Governs scope, not
  an R.
- **`reveal` takes the same gate** (planning decision). Same advertised
  biometric promise, same bypass class, near-zero extra cost. Governs R4.
- **GUI confirm is the non-biometric fallback, TTY never counts**
  (reviewer requirement). The caller owns its stdin; a spawned display-server
  dialog it cannot answer is the presence proof. Governs R2, R3.
- **`allow_ungated` resets when mode is (re)set** (planning decision).
  `agent mode ask --ungated` is the only way it turns on; plain `mode ask`
  clears it — ambiguous actions fail closed. Governs R2.

### Requirements

- **R1.** `omaseal agent unlock` under `ask` requires a user-presence
  confirmation before a session is written. When no presence mechanism is
  available and ungated is not set, it fails closed: nonzero exit, actionable
  error, no session file.
- **R2.** Ungated operation exists only as an explicit, deliberate user
  setting persisted in the agent policy (`allow_ungated`), set by
  `omaseal agent mode ask --ungated` with a loud warning. Any later
  `agent mode <mode>` invocation without `--ungated` clears it. The flag is
  never inferred, defaulted on, or settable via MCP/IPC.
- **R3.** The confirmation cannot be satisfied by the calling process.
  Acceptable proofs: fprintd match, or a GUI confirm dialog spawned by omaseal
  itself (pinentry `CONFIRM`, or `zenity --question` behind the existing
  prompter-order machinery). Caller stdin/TTY/argv never counts.
- **R4.** `omaseal reveal` runs the identical presence gate before printing a
  secret (it makes the same biometric promise in docs and has the same
  bypass).
- **R5.** Tests prove: no-fprintd + no-GUI + no-flag → no session, no secret;
  GUI cancel → no session; fprintd unavailable paths (daemon off, no bus name,
  no device, probe error) → fall through to the next mechanism, never to
  silent allow; `allow_ungated` → allow with a written warning. MCP
  `agent_unauthorized` behavior under `ask` is unchanged.
- **R6.** `omaseal agent status` (text + `--json`) and the MCP status tool
  report the effective presence mechanism (`fprintd`, `gui-confirm`,
  `ungated`, or `none`); `omaseal doctor` gains a presence-gate check that
  warns with remediation when `none`.
- **R7.** README and SUBMISSION.md describe the presence chain, the
  `--ungated` escape hatch, and honest limits (user-surface commands are
  outside the gate — see KTD4).

### Acceptance Examples

- **AE1.** omarchy-max, Wayland session, no fprintd: `omaseal agent unlock`
  spawns a confirm dialog; clicking Allow writes a session, Deny/cancel exits
  nonzero with no session.
- **AE2.** Same machine, `OMASEAL_GUI_PROMPT=off` (or headless): unlock exits
  nonzero, names the remediation (fprintd, GUI prompter, or `--ungated`),
  writes no session.
- **AE3.** `agent mode ask --ungated` prints a warning and persists
  `allow_ungated`; a later `agent mode ask` clears it; `agent status` shows
  the mechanism each time.
- **AE4.** Prompt-injected-agent scenario from the review: with no biometric
  and no display, `agent unlock` cannot produce a session, so MCP secret
  tools keep returning `agent_unauthorized`.

### Scope Boundaries

- **Deferred for later** (brainstorm in flight): howdy/face-recognition
  presence, polkit, hardware-capability auto-detect beyond fprintd+GUI, and
  the broader "can't assume our ARM chips" portability work. This plan's
  mechanism seam (`requireUserPresence`) is designed so those slot in as new
  chain steps later.
- **Outside this change's identity**: per-agent identity/permissions,
  session mechanics (expiry, keep-alive), gating `get`/`resolve`/IPC,
  changes to the secret store itself.

### Sources

- Reviewer finding: `omacom/omarchy-plugin-marketplace#5620`, 2026-09-22
  comment (five requirements carried as R1–R5 intent).
- `engine/agent.go:31-45` policy struct + fail-closed default;
  `engine/agent.go:195-212` `CheckAgentOperation` gate; `agent.go:244-267`
  `UnlockAgent`; `agent.go:289-360` status surfaces.
- `engine/fprintd.go:58-98` — every early `return nil` is an
  unavailable-state pass-through today.
- `engine/guiprompt.go` — prompter order (`OMASEAL_GUI_PROMPT` → pinentry,
  zenity), `fixedOrLookPath` anti-shadowing, `graphicalSession`, Assuan
  plumbing incl. cancel detection (`isAssuanCancel`, err 83886179).
- `engine/mcp.go:201` — MCP status tool, "unlocking stays a human-only
  action" (already true; keep it).
- `engine/main.go:269-287` `handleReveal`; `main.go:418` `handleAgent`
  dispatch; `ipc.go` (no unlock method exists — nothing to fence there).

## Planning Contract

### Key Technical Decisions

- **KTD1 — one `requireUserPresence(ctx, reason)` chain in new
  `engine/presence.go`.** Order: `fprintdUsable` → real `FprintdVerify`;
  else GUI confirm via prompter order; else `policy.AllowUngated` (loud
  `WriteLog` + stderr warning); else deny. Evaluated fresh per call, never
  cached. All probe errors classify as "mechanism unavailable" and fall
  through — except a *user-visible deny* (cancel/timeout), which stops the
  chain immediately (don't re-ask on a second prompter after a dismissal,
  matching `guiPromptSecret` semantics).
- **KTD2 — `fprintdUsable` extracted as a probe.** Refactor the
  early-return-nil prefix of `FprintdVerify` (service active → bus name →
  default device) into a predicate; `FprintdVerify` keeps the Claim/verify
  loop and stays fail-closed once usable (setup failures already return
  error). A device present but unenrolled lands in real verify, which
  already fails closed — no change needed there.
- **KTD3 — GUI confirm extends the `guiPrompter` interface.** Add
  `confirm(ctx, title, desc) (bool, error)` alongside `prompt`. pinentry:
  `CONFIRM` over the existing Assuan conn (OK=allow, cancel error=deny).
  zenity: `--question --ok-label=Allow --cancel-label=Deny --timeout`, exit
  0=allow. Same `guiPrompterOrder` env, `fixedOrLookPath` hardening, and
  5-minute default deadline. The dialog is spawned by omaseal on
  `$WAYLAND_DISPLAY`/`$DISPLAY`; the requesting process gets a boolean, never
  a channel it can write to.
- **KTD4 — `get`/`resolve`/IPC stay ungated, deliberately.** The reviewer's
  boundary is the advertised gated path (session + MCP tools) and
  biometric-advertised `reveal`. Session-local processes can always read the
  keyring via Secret Service D-Bus directly — gating the plain CLI would be
  security theater that breaks the documented user UX. This is stated
  plainly in docs (R7) so the reviewer sees the boundary is understood, not
  overlooked.
- **KTD5 — `allow_ungated` lives on `AgentPolicy`, reset by mode changes.**
  `allow_ungated` JSON field; `agent mode ask --ungated` sets it, every other
  `agent mode` invocation clears it. Missing/false = gated. `agent status`,
  `--json`, and the MCP status tool expose it plus the resolved mechanism.
- **KTD6 — test seams via package-level func vars**, matching the repo's
  existing `pinentryCandidatePaths`/`zenityCandidatePaths` stub pattern and
  `XDG_CONFIG_HOME`/`XDG_RUNTIME_DIR` redirection used by manifest tests.

### Sequencing

U1 (mechanism) → U2 (wiring/surfaces) → U3 (tests+docs). U1 and U2 touch
`agent.go`/`main.go` lightly; U3 is where the reviewer's R5 lives.

### Assumptions (pipeline-mode inferred scope — correct me if wrong)

- `reveal` is in scope (R4) — extends the reviewer's literal ask by one
  command; same hole, same fix, cheap.
- Wayland-spawned GUI dialogs count as presence proof the caller can't
  satisfy — standard assumption for graphical prompters; synthetic-input
  tooling is out of the threat model the reviewer described.
- Landing on `main` moves the freeze SHA — expected; the revalidation
  comment on #5620 names the new HEAD after merge (post-PR follow-up, not
  code).
- Branch: `fix/agent-unlock-presence-gate` off `main` (`20a19ae`), not off
  the open #19 branch.

## Implementation Units

### U1. Presence gate engine

- **Goal.** `requireUserPresence` exists and is correct in isolation.
- **Requirements.** R1 (mechanism), R3 (caller can't satisfy).
- **Files.** `engine/presence.go` (new); `engine/fprintd.go` (extract
  probe); `engine/guiprompt.go` (add `confirm` to `guiPrompter`, pinentry
  `CONFIRM`, zenity `--question`).
- **Approach.** KTD1–KTD3 verbatim. `requireUserPresence(ctx, reason,
  policy)` returns nil (allow) or a deny error carrying remediation text.
  New sentinel `errPresenceDenied`; distinguish "no mechanism" from "user
  denied" in the error text.
- **Test scenarios.** Probed per-mechanism with stubbed seams: each
  unavailable state advances the chain; user cancel short-circuits; probe
  errors never allow.
- **Verification.** `cd engine && go build ./... && go vet ./...`.

### U2. Wire the gate + surfaces

- **Goal.** `agent unlock` and `reveal` run the chain; `allow_ungated`
  exists; status/doctor/setup report honestly.
- **Requirements.** R1, R2, R4, R6.
- **Files.** `engine/agent.go` (policy field, `UnlockAgent`, `SetAgentMode`
  reset semantics, status text+JSON), `engine/main.go` (`handleReveal`,
  `handleAgent` mode flag parsing, `usage()`), `engine/mcp.go` (status
  field), `engine/doctor.go` (`checkPresenceGate`), `engine/setup.go`
  (presence note alongside the existing fingerprint hint).
- **Approach.** `UnlockAgent` loads policy, rejects `lock`, then
  `requireUserPresence` instead of `FprintdVerify`. `handleReveal` same
  swap. `agent mode <m> [--ungated] [--minutes N]`; `--ungated` only valid
  with `ask`, warns loudly on stderr. Status line reads e.g.
  `presence gate: gui-confirm (pinentry/gtk)` / `ungated (deliberate)` /
  `none — unlock will fail`. Doctor: mechanism present → ok naming it;
  ungated → ok with explicit warning; none → warn + remediation.
- **Test scenarios.** CLI parse: `--ungated` accepted only on `mode ask`;
  mode reset clears flag; status reflects each state.
- **Verification.** `cd engine && go build ./... && go vet ./...`.

### U3. Tests + docs

- **Goal.** R5 coverage lands; docs tell the truth (R7).
- **Requirements.** R5, R7, R2 (reset semantics), R6.
- **Files.** `engine/presence_test.go` (new), `engine/agent_test.go` (extend),
  `engine/guiprompt_test.go` (confirm-path cases, reuse its fake-binary
  pattern), `README.md`, `SUBMISSION.md`.
- **Approach.** KTD6 seams + temp-dir config. The reviewer-facing claim:
  every non-presence state denies — encode each one as a named test. Docs:
  README gets a "user-presence gate" paragraph under the agent section +
  `--ungated` escape hatch; SUBMISSION.md updates the "biometric unlock"
  claim to the real chain and states the user-surface boundary plainly.
- **Test scenarios.** The R5 matrix (every unavailable/unenrolled/error
  state → no session, no secret); `reveal` denied-path test;
  `CheckAgentOperation` still `agent_unauthorized` after a failed unlock;
  policy round-trip + reset.
- **Verification.** `cd engine && go test ./...` full suite green;
  `python3 -m unittest contrib.test_omaseal_jev_audit` unchanged.

## Verification Contract

- `cd engine && go build ./... && go vet ./...` — clean.
- `cd engine && go test ./...` — all green, including the new R5 matrix.
- `python3 -m unittest contrib.test_omaseal_jev_audit` — no regression.
- Manual on omarchy-max (has display, no fprintd): AE1/AE2 exercised before
  the PR is described as fixing the finding.
- Docs grep: `rg -n "biometric|fprintd|ungated|presence" README.md
  SUBMISSION.md` — no stale "best-effort" claims remain.

## Definition of Done

- Every R maps to merged code + a named test where R5 requires it.
- `agent unlock`/`reveal` on a reader-less, displayed machine prompts a GUI
  confirm; on headless without `--ungated` they fail closed.
- No dead or speculative code (no half-wired howdy hook, no unused prompter
  kinds); abandoned approaches removed from the diff.
- Commit(s) reference the marketplace finding; PR body carries the
  reviewer-requirements mapping so attestation can re-check point by point.
- Out of scope but noted for follow-up: after merge, post the new HEAD SHA
  to #5620 requesting revalidation (separate, non-code step).
