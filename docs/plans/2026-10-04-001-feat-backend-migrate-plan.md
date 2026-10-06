# Plan — `omaseal migrate` (Secret Service → native bulk copy)

Origin: `docs/brainstorms/2026-10-04---backend-migrate-requirements.md`
(R1–R8, A1, F1–F2, AE1–AE4). Depth: Standard — small surface, high-value
data path, must not corrupt either store.

## Decisions

- **KTD-1 (user-directed): copy semantics.** Zero Secret Service writes —
  rejected move/delete-originals because rollback must stay a config-line
  flip. SS remains the pre-migration snapshot; divergence is documented,
  not managed. Covers R2.
- **KTD-2 (user-directed): all-items scope.** Owned + external migrate —
  rejected owned-only because post-flip omaseal must be functionally
  complete (`get`/`run`/`sudo`/MCP on external items keeps working).
  Covers R1/AE1.
- **KTD-3: provenance as native record metadata.** Native `store.json`
  entries gain a provenance bool **outside** the ciphertext envelope (it
  is advisory metadata, same trust class as cleartext key names — not
  part of the tamper-bound payload). Shipped as `external` (inverted):
  the Go zero value `false` reads as owned, so records written before
  the field existed get `Owned: true` for free — no pointer, no store
  migration. `Set` writes owned; `migrateSet` carries `item.Owned`;
  `List` surfaces `Owned: !it.External`. Rejected: extending
  `Store.Set`'s signature (churns every caller for a native-only
  concern). Covers R8.
- **KTD-4: explicit backend handles, not `currentStore`.** Migration
  constructs `ssStore` + `nativeStore` directly so it works regardless of
  the configured `backend:` — the operator can migrate *before* flipping
  config (recommended flow) or after.
- **KTD-5: injectable item source.** The migrate core takes an
  `itemSource` seam (enumerate + get) so tests drive a fake SS against a
  real sandboxed native store — no D-Bus fixture needed.
- **KTD-6: uninitialized-native behavior.** If no native identity exists
  and stdin is a TTY, the command may run the existing init/passphrase
  flow inline; non-TTY fails closed with the init hint (R6). Either way:
  no silent partial state.
- Deferred per origin: `--purge-source`, reverse migration, `--only`
  filters, divergence detection, doctor backend line.

## Implementation units

- **U1. `owned` provenance in the native record** — `store.json` entries
  gain `owned`; `Set`/`migrateSet` write it; `List` reports it; existing
  records without the field read as `owned: true` (omaseal wrote them —
  safe default since only omaseal writes native). Files:
  `engine/nativestore.go`, `engine/nativestore_test.go`. Covers R8.
  Depends: none.

- **U2. `itemSource` enumeration seam** — shipped as the narrow
  `itemSource` interface (`List` + `Get`) in `backendmigrate.go`,
  satisfied by `ssStore` directly. Rejected extracting
  `keyringmigrate`'s collection walk: `ssStore.List("")` already
  enumerates the login collection with `Owned` provenance, which is
  also the entire read world omaseal exposes (`findItem`/`List` never
  leave the default collection). Covers R1. Depends: none.

- **U3. `migrate` core + command** — `migrateItems(src, dst)` iterates
  the source: `Get(svc, acct)` → skip if key exists in dst →
  `migrateSet(svc, acct, secret, owned)`; per-item failures counted and
  named, never fatal (R4); summary `{migrated, skipped, failed[]}`;
  chained-log `op=migrate` record with counts only (R5). `handleMigrate`
  wires ssStore+nativeStore, enforces KTD-6, prints the summary. Files:
  `engine/backendmigrate.go`, `engine/main.go` (dispatch + help).
  Covers R1/R3/R4/R5/R6/R7. Depends: U1, U2.

- **U4. Tests** — fake `itemSource` + `newNativeStoreAt` sandbox.
  Covers AE2/AE3/AE4. Depends: U3.

## Test scenarios

**U1**
- happy: `migrateSet` with `owned:false` → `List` reports `Owned:false`;
  normal `Set` reports `Owned:true`.
- edge: a store.json written before the field existed (no `owned` key)
  → `List` reports `Owned:true`, no error.
- error: corrupted `owned` field type → record fails parse, surfaced as
  store error (same class as malformed ciphertext today).

**U2**
- happy: enumeration yields every item across collections with correct
  `Owned` from the `app` attribute.
- edge: item missing `app` attribute → `Owned:false`.
- edge: empty collection → empty slice, no error.
- (Existing SS-path tests cover the D-Bus leg; unit seam tested via fake
  in U4.)

**U3** (fake source → real native sandbox)
- happy (AE1 shape): N source items → all land in native; `Get` under a
  seeded session returns original values; source fake records zero
  writes.
- edge (AE2): run twice → second run `migrated 0, skipped N`.
- error (AE4): source Get fails for one item → `failed 1` names it,
  other N−1 migrated, command still exits the summary path.
- error (AE3): no native identity + non-TTY → non-zero exit, init hint,
  nothing written.
- edge: item whose svc/acct already exists in native with a *different*
  value → skipped (no divergence detection — deferred); counted skipped.
- edge: service/account containing `/`, spaces, unicode → envelope
  binding round-trips; `Get` under unlock returns value.
- integration: `op=migrate` lands in `omaseal.log` with counts; chain
  still verifies.

## Risks & dependencies

- **RISK-1: enumerate misses items in non-default collections** —
  accepted and documented as scope. Every omaseal read path (`findItem`,
  `List`, MCP, IPC) resolves against the login collection only, so
  parity for post-flip completeness means copying exactly that set;
  secrets a user keeps in a second collection were never visible to
  omaseal anyway. Caveat: `omaseal keyring migrate` can leave a second
  populated collection behind — those items are out of scope by design.
  Verified live: 773 source items → `migrated 773` → `list` parity 774.
- **RISK-4: per-item `Get` cost** — each `ssStore.Get` opens a bus
  connection + session (~7 round-trips). Rejected the bulk-session fast
  path after measuring: 773 items migrated live in ~7s — not worth the
  extra D-Bus plumbing for a one-shot command. Revisit if a migration
  ever runs on a slow/stale daemon.
- **RISK-6: enumeration drops are inherited from `ssStore.List`.** An item
  whose metadata fetch fails is dropped inside `keyring.go` before
  `migrateItems` ever sees it — not counted migrated/skipped/failed. The
  chain log records `list: N of M items skipped`; the migrate summary
  can't name them (the unreadable metadata carries the name). Same
  behavior `omaseal list` has always had — parity is honest, not lossy.
- **RISK-7: `Owned` provenance is forgeable — inherited, kept for
  consistency.** `attrs["app"]=="oma-ring"` is stampable by any same-uid
  SS writer; a forged item wins the dedup preference. But `findItem`
  applies the identical preference to live reads — post-flip `get`
  returns the same item pre-flip `get` would have. Diverging here would
  make migrate *less* faithful, not safer. Real fix belongs to the
  different-uid trust boundary, not the copy.
- **RISK-8: `--dry-run` may initialize the native store** (KTD-6) —
  init writes `identity.age`/`identity.pub`, never secret values. The
  alternative (dry-run on a non-existent store) can't evaluate presence
  and gives a worse error later.
- **Follow-ups recorded, not in diff scope:** `sanitizeField` passes C1
  controls (U+0080–U+009F) and has no length cap — same gap `list`/
  `stats` already have; fix the shared helper separately. `confirm` is
  satisfiable by piped stdin — house-consistent with `keyring migrate`.
- **RISK-5: TOCTOU clobber** — a concurrent `Set` landing between the
  presence snapshot and a `migrateSet` write would be silently
  overwritten. Mitigation shipped: `set(ifAbsent)` checks existence
  inside the flock'd `update` transaction and returns `errItemExists`;
  the migrate loop counts it skipped. Snapshot map remains the fast
  path only.
- **RISK-2: mid-migration interruption** — partial copy. Mitigation:
  idempotent re-run is the recovery (AE2); no transaction needed because
  per-item writes are atomic and skip-covered.
- **RISK-3: provenance default wrong for legacy records** — the
  `owned:true` default is safe only because native has no external
  writers; keep the comment + test asserting it so a future external-
  writer design can't inherit it silently.
- **Dep:** `newNativeStoreAt` already exists (test seam); `ssStore`
  methods exist. No new dependencies.
