# Requirements — `omaseal migrate` (Secret Service → native store)

Origin: lfg "next phase" run, 2026-10-04. Settled in dialogue:

- **Copy semantics** — SS originals are never touched (user-settled).
- **All-items scope** — owned + external, provenance flags carry (user-settled).
- Deferred: `--purge-source`, reverse migration, selective `--only`, divergence detection.

## Problem

`backend: native` flips the entire store view; existing secrets stay in
Secret Service and become invisible to every omaseal surface (get, run,
sudo, IPC, MCP). There is no path between backends, so the native
backend cannot be adopted on a machine with a populated SS store.

## Requirements

- **R1** — `omaseal migrate` copies every item in the Secret Service
  collection into the configured native store, in-process, value in
  memory only — no dump file, no argv, no log value.
- **R2** — Copy-only: the command performs zero writes to Secret
  Service. Rollback remains `backend: secretservice` in config.
- **R3** — Idempotent: an item whose `service/account` key already
  exists in native is skipped, not overwritten or errored.
- **R4** — Per-item failure isolation: one bad item aborts nothing;
  failures are counted and named (service/account) in the summary.
- **R5** — Summary output: `migrated N, skipped N, failed N` (+ failed
  names). Chained audit log records the op, not values.
- **R6** — Requires an initialized native store; if none exists the
  command fails closed with the `agent unlock`/init hint (no silent
  partial state).
- **R7** — Works while native is locked: writes encrypt to the public
  recipient; SS reads use the existing collection-unlock path.
- **R8** — Provenance (`owned`/`external`) is preserved on the migrated
  items.

## Actors

- **A1** — Human operator flipping an existing install to `backend: native`.

## Key flows

- **F1** — Operator sets `backend: native`, runs `omaseal agent unlock`
  (initializes identity + passphrase), runs `omaseal migrate`, sees
  summary, verifies `omaseal list` parity, `omaseal get` on a known item
  works within the session window.
- **F2** — Re-run after partial interruption: skipped count grows,
  migrated count only covers new items; no duplicates, no corruption.

## Acceptance examples

- **AE1** — 770-item SS store → migrate → `omaseal list` on native
  shows 770; `omaseal get sudo lukekimball` (unlocked session) returns
  the original value; SS store still lists 770.
- **AE2** — Migrate, then re-run immediately → `migrated 0, skipped N`.
- **AE3** — Migrate invoked with no native identity → exits non-zero,
  prints init hint, writes nothing.
- **AE4** — One unreadable SS item → summary shows `failed 1` with the
  item named, other 769 migrated.
