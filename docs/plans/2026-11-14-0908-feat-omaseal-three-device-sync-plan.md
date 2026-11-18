---
title: "feat: encrypted three-device OmaSeal sync"
created_at: 2026-11-14T09:08:00Z
date: 2026-11-14
type: feat
topic: omaseal-three-device-sync
artifact_contract: ce-unified-plan/v1
product_contract_source: legacy-requirements
origin: Luke — three devices syncing OmaSeal, encrypted; release handoff Gate 1 / ROADMAP U10
execution: code
---

# Plan — Encrypted three-device OmaSeal sync

## Goal Capsule

- **Objective:** At least three devices can hold decryptable copies of the same native vault: one writer (primary) and two read-only replicas, end-to-end encrypted with age multi-recipient — no shared `identity.age`.
- **Means:** Extend the existing laptop→replica push (`contrib/sync/omaseal-sync`) to N hosts and require ciphertext recipient stanzas to match the device count; keep the engine multi-recipient path (#72) unchanged. Document pairing and a fixture-backed verify recipe.
- **Authority:** Prefer age/encrypted store already in product; extend sync, do not invent a parallel vault. Surgical; no secrets in logs/docs.
- **Stop:** Do not build two-way sync, Keychain (#84), backup (#73), or U9 session/store lock changes.
- **Execution profile:** code. Implementer finishes the slice, opens a PR, and leaves Luke-only pairing (presence gate + real host SSH) documented.

## Product Contract

### Requirements

- R1. Primary remains the only writer. Replicas stay `replica: true` / `OMASEAL_REPLICA=1` and refuse Set/Delete/migrate/recipient rotation (`code=replica_readonly`).
- R2. Every secret on the primary encrypts to the primary identity plus each replica's `age1…` public key via existing `omaseal recipients add` (N extras already supported).
- R3. Sync copies only `store.json`. Never `identity.age`, `identity.pub`, `store.lock`, `recipients.json`, or session files.
- R4. One sync invocation can push the same store to at least two replica hosts (three devices total including primary). Single-host `OMASEAL_REPLICA_HOST` stays supported.
- R5. Before copy, refuse a store whose age headers do not have at least `1 + N` recipient stanzas when pushing to N hosts (primary + N replicas). Empty store: warn and continue (unchanged).
- R6. Per-host atomic install + sha256 verify; one host failure must not leave that host with a partial replace; other hosts already updated stay as-is; overall exit non-zero if any host fails.
- R7. Fixture/test evidence shows a three-device path (primary fixture → two dest dirs) with a 3-stanza store; docs list the real pairing commands without embedding secrets or personal host names.
- R8. No parallel vault format, no plaintext sync path, no identity sharing.

### Actors

- A1. Primary operator (laptop) — unlocks, writes, runs `omaseal-sync`.
- A2–A3. Replica operators/machines — init own identity, print `recipients local`, run replica-mode Get/List.

### Key flows

- F1. Pair three devices
  - **Trigger:** Operator wants device B and C to decrypt the vault.
  - **Steps:** On each replica: native + replica mode → `omaseal recipients local`. On primary: `recipients add` twice (presence-gated) → `recipients list` shows local + two names → enable multi-host sync.
  - **Covered by:** R1, R2, R7
- F2. Push after a Set
  - **Trigger:** `store.json` changes on primary (or timer).
  - **Steps:** `omaseal-sync` checks stanza count ≥ 3 for two hosts → scp+verify+mv to each host.
  - **Covered by:** R3–R6

### Acceptance examples

- AE1. Covers R4–R5. Given a fixture store with three age stanzas and `OMASEAL_REPLICA_HOSTS=h1,h2`, when sync runs under fake ssh/scp, then both dest `store.json` match the source hash and neither dest identity is overwritten.
- AE2. Covers R5. Given a two-stanza store and two hosts, when sync runs, then it exits non-zero and leaves both dest stores unchanged.
- AE3. Covers R2/R7 (Luke). Given three real machines with Tailscale SSH, when both replica pubs are added and sync runs, then each replica `omaseal get` decrypts the same service/account after unlock.

## Planning Contract

### Inventory (main @ v0.5.0)

| Piece | Status | Gap for 3 devices |
| --- | --- | --- |
| Native age store + multi-recipient (#72) | Shipped | None — N extras already rotate |
| Replica read-only mode | Shipped | None |
| `contrib/sync/omaseal-sync` (#75) | Shipped | Single `OMASEAL_REPLICA_HOST` only; stanza floor fixed at 2 |
| Sync tests/docs | 1 dest | No 3-device fixture or pairing section |

### Key technical decisions

- KTD1. Sync-only slice. Engine already encrypts to N; do not change `nativestore`/`nativerecipients` unless a real bug blocks 3 recipients (none found).
- KTD2. Host list env: `OMASEAL_REPLICA_HOSTS` comma/whitespace-separated. If unset, fall back to `OMASEAL_REPLICA_HOST`. Require at least one. Document both.
- KTD3. Min stanzas = `1 + host_count` (writer + each sync target). Override with `OMASEAL_SYNC_MIN_RECIPIENTS` only if needed for extras-without-sync (defer unless trivial); default derived is enough.
- KTD4. Same remote path resolution per host (`OMASEAL_REPLICA_PATH` or remote XDG formula). No per-host path map in this slice.
- KTD5. Partial multi-host success: continue after a failure, exit 1 if any failed; do not invent rollback across hosts.

### Assumptions

- A1. Devices are separate OmaSeal installs with distinct identities.
- A2. Transport remains Tailscale SSH BatchMode (unchanged).
- A3. Luke performs presence-gated `recipients add` and real SSH pairing; agents supply scripts/tests/docs.

### Sequencing

1. U1 multi-host sync + stanza floor
2. U2 tests (3-device fixture)
3. U3 docs / handoff verify recipe

## Implementation Units

### U1. Multi-host encrypted push

- **Goal:** One `omaseal-sync` run pushes `store.json` to N replica hosts with a device-count stanza gate.
- **Files:** `contrib/sync/omaseal-sync`, briefly `contrib/sync/README.md` env table (full prose in U3).
- **Approach:** Parse hosts from `OMASEAL_REPLICA_HOSTS` or single `OMASEAL_REPLICA_HOST`. Factor push-to-one-host into a function. `check_multi_recipient` takes `min_stanzas`. Loop hosts; accumulate failures.
- **Test:** Covered by U2.
- **Done when:** Two-host dry-run and live (shim) push work; single-host backcompat preserved; identities never copied.

### U2. Three-device fixture evidence

- **Goal:** Automated proof of primary → two replicas without live secrets.
- **Files:** `contrib/sync/omaseal-sync_test.sh`
- **Approach:** Add a section: 3-stanza store, two dest dirs (or host:path via two `OMASEAL_REPLICA_PATH` runs — prefer one sync with two hosts and a fake ssh that routes by host name to `$tmpdir/dst1` and `$tmpdir/dst2`). Assert both hashes match, identities untouched, and a 2-stanza store is refused for two hosts.
- **Done when:** `bash contrib/sync/omaseal-sync_test.sh` passes including the new cases.

### U3. Docs and verify recipe

- **Goal:** Operator can pair and verify three devices from docs alone.
- **Files:** `contrib/sync/README.md`; store handoff `omaseal-three-device-sync-handoff.md` (outside repo).
- **Approach:** Add "Three devices" section: pair commands, `OMASEAL_REPLICA_HOSTS=host-b,host-c`, verify with dry-run + per-replica `recipients local` / unlock / get. No real keys, hostnames, or secrets.
- **Done when:** README + handoff list the exact verify commands; blockers (presence, SSH, Luke) named.

## Verification Contract

```sh
bash contrib/sync/omaseal-sync_test.sh
# optional smoke (no secrets):
OMASEAL_STORE=/path/to/fixture/store.json \
OMASEAL_REPLICA_HOSTS=h1,h2 \
OMASEAL_SSH=... OMASEAL_SCP=... \
contrib/sync/omaseal-sync --dry-run
```

Engine regression optional this slice: `cd engine && go test ./... -count=1` if touching Go (not expected).

## Definition of Done

- [ ] U1–U3 complete
- [ ] Sync tests green with three-device cases
- [ ] PR opened; handoff documents Luke verify-on-3-devices steps and blockers
- [ ] No secrets/PII in logs, fixtures, or docs

## Appendix

- Prior plans: `docs/plans/2026-11-12-001-feat-native-multi-recipient-replica-plan.md`, `docs/plans/2026-11-12-feat-macos-menubar-and-mini-replica-plan.md`
- Design: `docs/design/native-store.md` (Multi-recipient and read-only replica)
- Shipped: #72 multi-recipient, #75 sync, #76 menu bar
