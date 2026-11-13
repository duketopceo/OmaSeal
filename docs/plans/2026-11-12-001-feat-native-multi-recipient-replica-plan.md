# Plan — Multi-recipient native store and read-only replica

Origin: a second OmaSeal instance that stays in sync with the laptop
over Tailscale so agents can read secrets while the laptop is offline.
Engine scope only: encrypt each secret to the laptop identity *and*
additional machine recipients, plus a read-only mode for a synced
replica. The Tailscale sync and the macOS menu-bar app are a separate
unit of work. Depth: Standard.

## Problem Frame

The native store (`engine/nativestore.go`, `docs/design/native-store.md`)
encrypts every value to a single X25519 recipient — the machine's
passphrase-wrapped identity. A second machine cannot decrypt a copy of
`store.json` unless it holds that same identity (and passphrase), which
defeats "the replica Mac has its own key." age already supports N recipients
per ciphertext; this unit turns that on, with a gated rotation path and a
replica flag that refuses writes.

## Requirements

- R1. Every `Set` (and rotation) encrypts to the local identity.pub **and**
  every additional X25519 recipient in the store's committed recipient set.
  A second identity listed as a recipient can `age.Decrypt` the value; the
  primary identity still can. Passphrase-wrapped `identity.age` on the
  primary is unchanged (I6 still holds: passphrase rotation is a re-wrap,
  not a vault rewrite).
- R2. `omaseal recipients add <age1...> --name <name>`, `list`, `rm`, and
  `local`. Additional recipients are stored in `config.json` and in a
  committed snapshot in the native store directory. `list` shows the local
  public key plus extras; `local` prints only the local `age1…` key (for
  the replica to share with the laptop).
- R3. Adding or removing a recipient re-encrypts every item (rotation)
  under the store flock. A decrypt failure aborts with the store
  bit-identical to before. The primary identity cannot be removed.
- R4. Add and remove are weakening/sensitive changes: gated by
  `requirePresenceStrict` (fingerprint or GUI confirm; `allow_ungated`
  does **not** bypass). Every successful or denied change is written to
  the hash-chained audit log (`op=recipients-add` / `op=recipients-remove`,
  name + public key + item count — never a secret value).
- R5. Recipients-file integrity: `Set` encrypts to the **committed**
  snapshot (`$XDG_STATE_HOME/omaseal/native/recipients.json`), not to a
  raw config edit. An unsanctioned `config.json` change is not honored
  until a gated `recipients add`/`rm` rotates. Honest same-uid limit: a
  process that can write both the snapshot and `store.json` can still
  plant a key; that is the confinement track's documented bound. The
  official path is gated and audit-logged; `doctor` warns on divergence.
- R6. Read-only replica: `"replica": true` in `config.json`, or env
  `OMASEAL_REPLICA=1` (`true`/`yes`/`on`). Either is sufficient. `Set`,
  `Delete`, `migrateSet`, `import`, and `migrate` refuse with a named
  error (`code=replica_readonly`, `errReplicaReadOnly`). `Get`, `List`,
  unlock, and first-run identity init still work — the replica must be able
  to create its own `identity.age`. Recipient rotation is also refused
  on a replica (the primary owns the recipient set).
- R7. Tests: multi-recipient encrypt/decrypt with a second identity;
  rotation on add and on remove; replica refuses writes (including
  migrate); gate enforcement (deny writes nothing; `allow_ungated` is
  ignored; allow rotates). Existing I1–I6 tests stay green. `make test`
  (or at minimum the native-store and new tests).

## Key Technical Decisions

- **KTD-1: age multi-recipient, not a second envelope.** `age.Encrypt`
  already takes `...age.Recipient`. One ciphertext, N stanzas. Rejected:
  a second ciphertext field (doubles the file, two decrypt paths).
- **KTD-2: committed snapshot is what `Set` uses.** User-facing list lives
  in `~/.config/omaseal/config.json` (`recipients: [{name, pubkey}]`) as
  requested. Encryption honors `$XDG_STATE_HOME/omaseal/native/recipients.json`,
  written only by the gated rotation path, so a config-only edit cannot
  silently widen the encryption set. Primary `identity.pub` is always a
  recipient and is not listed in that file.
- **KTD-3: presence at the CLI, crypto at the store.**
  `requirePresenceStrict` wraps the CLI handlers — same family as manifest
  apply and Jev enablement. Store methods (`addRecipient`,
  `removeRecipient`) do the unlock + re-encrypt and are directly
  testable. This is a posture write, so `allow_ungated` is not honored.
- **KTD-4: replica is a store-level guard, not a CLI-only one.** IPC, MCP,
  `ask-save`, and the provider daemon all go through `Set`/`Delete`/
  `migrateSet`. The named error is returned there. `handleMigrate`
  constructs its own `nativeStore`, so `migrateItems` also checks the flag.
- **KTD-5: only X25519 recipients.** `age.ParseX25519Recipient` — no SSH
  or plugin recipients in this unit (hardware identities stay a future
  age-plugin-se track).
- **KTD-6: sync copies `store.json` only.** Out of engine scope, but
  recorded so the sync work has a contract: the replica Mac generates its
  own identity (first TTY `omaseal get`, or `recipients local` after
  init), shares `identity.pub`, and is added on the laptop. Sync must
  not overwrite the replica's `identity.age` / `identity.pub`. See
  "Handoff to the sync side" below.

## Implementation units

- **U1. Config + replica flag.** `omasealConfig` gains `Replica bool` and
  `Recipients []namedRecipient`. `saveConfig` atomic 0600. `OMASEAL_REPLICA`
  env. `newNativeStore` applies the flag. Files: `engine/config.go`,
  `engine/nativestore.go`. Covers R6 (flag plumbing). Depends: none.

- **U2. Multi-recipient encrypt, rotation, replica guard.** `Set` encrypts
  to local pub + committed extras, reloading the snapshot inside the
  flock so a concurrent rotation is not missed. `addRecipient` /
  `removeRecipient` decrypt every item, re-encrypt to the new set, write
  `store.json` + `recipients.json` under the lock; abort on any decrypt
  failure leaves both files unchanged. `Set`/`Delete`/`migrateSet` refuse
  when `replica` is set. Identity wrap paths are untouched. Files:
  `engine/nativestore.go`, `engine/nativerecipients.go` (new),
  `engine/nativestore_test.go`, `engine/nativerecipients_test.go` (new),
  `engine/backendmigrate.go` (early refuse). Covers R1, R3, R5, R6.
  Depends: U1.

- **U3. CLI + presence gate + audit.** `omaseal recipients add|list|rm|local`
  in the Setup group. Add requires `--name`. Bare `recipients` shows help.
  Handlers call `requirePresenceStrict`, then the store method, then
  `saveConfig` to match the committed snapshot, then `WriteLog`.
  Files: `engine/cli.go`, `engine/cli_test.go`, `engine/main.go`,
  `engine/recipients.go` (new), `engine/recipients_test.go` (new).
  Covers R2, R4. Depends: U2.

- **U4. Docs.** Plan (this file). `ROADMAP.md` unit line. Short addendum
  on `docs/design/native-store.md`. Brief README note under backend
  selection. `CONCEPTS.md` entries for store recipient and read-only
  replica. Doctor backend line mentions replica and recipient count.
  Files: `ROADMAP.md`, `docs/design/native-store.md`, `README.md`,
  `CONCEPTS.md`, `engine/doctor.go`. Covers R2 (discoverability), R5
  (doctor divergence). Depends: U3.

## Test scenarios

**U2 — store (engine/nativerecipients_test.go, engine/nativestore_test.go)**

- T1. After `addRecipient` of a second X25519 identity, the primary `Get`s
  the value, and `age.Decrypt` with the second identity also returns it.
  The age header has two `X25519` recipient stanzas. `identity.age` bytes
  are unchanged.
- T2. A fresh `Set` after add is also decryptable by both identities.
- T3. `removeRecipient` re-encrypts; the removed identity can no longer
  decrypt; the primary still can. Stanza count returns to one.
- T4. A corrupt item aborts rotation: `store.json` and `recipients.json`
  are bit-identical to before; the second identity cannot decrypt.
- T5. A replica store: `Set`, `Delete`, `migrateSet`, `addRecipient`,
  `removeRecipient` return `errReplicaReadOnly` (`code=replica_readonly`);
  `Get` and `List` succeed; identity init is not blocked.
- T6. `Set` uses the committed snapshot, not a config-only edit: writing
  a pubkey into `config.json` alone does not add a stanza to a new `Set`.
- T7. `migrateItems` against a replica dst fails closed without writing.
- Existing I1 (Set while locked), I2 (bad unlock never writes), I5
  (tamper), I6 (passphrase re-wrap) still pass with extra recipients.

**U3 — CLI / gate (engine/recipients_test.go, engine/cli_test.go)**

- T8. `requirePresenceStrict` deny (no mechanism, or declined) writes
  nothing: no rotation, no config change, no committed-file change.
- T9. `allow_ungated` does not bypass the gate.
- T10. Presence allow + add: items rotatable by the second identity,
  config and committed file agree, audit path is the official one.
- T11. Command-tree translation: `recipients add|list|rm|local` reach
  the legacy handler. Visible top-level count stays ≤ 18.

## Handoff to the sync side (not this PR)

- The replica Mac runs OmaSeal with `backend: native` and `replica: true`
  (or `OMASEAL_REPLICA=1`). First TTY use creates its own
  `identity.age` / `identity.pub`. `omaseal recipients local` prints
  the `age1…` key to add on the laptop:
  `omaseal recipients add age1example... --name replica`.
- Sync **only** `$XDG_STATE_HOME/omaseal/native/store.json` (the
  ciphertexts), laptop → replica, after a rotation or any `Set`. Do **not**
  copy `identity.age`, `identity.pub`, `store.lock`, or the tmpfs
  session files — those are machine-local. Copying the laptop's
  identity onto the replica would collapse the two keys into one.
- The replica unlocks with its own passphrase (`agent unlock`), then
  `Get`s. It cannot `Set`/`del`/`import`/`migrate`.
- Manifest, flags, and agent policy are out of this unit; the sync
  follow-up decides whether those travel with `store.json`.
- Two-way sync (writes on the replica) is a follow-up; this unit's
  replica is one-way by construction.

## Out of scope

- Tailscale transport, menu-bar app, launchd/plist on the replica.
- Two-way sync, conflict resolution, or a daemon watching `store.json`.
- age-plugin-se / hardware identities.
- Changing Secret Service or making native the default.
- Merging #70 (macOS engine build) or #71 (CI qmllint); this branch
  is based on `fix/ci-panel-qmllint` (#71) so the Linux test job is
  green. Neither PR is merged by this work.
