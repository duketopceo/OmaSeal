# feat: `omaseal keyring migrate` — encrypt plaintext keyring at rest

**Type:** feat · **Depth:** Standard · **Target repo:** OmaSeal (`engine/` package)

## Summary

Add a first-party `omaseal keyring migrate` command that migrates an unlocked,
empty-password GNOME keyring (all secrets stored plaintext at rest — the
autologin gotcha the `keyring-encryption` doctor check now detects) into a new
encrypted `login` collection, using only the Secret Service D-Bus API that
OmaSeal already speaks. No GUI tools, no new dependencies.

## Problem Frame

OmaSeal v0.3.0 shipped with a real gap: it writes correctly through the Secret
Service API, but never checked whether the daemon's backing file was actually
encrypted. On machines with display-manager autologin, `pam_gnome_keyring`
receives no password, the keyring stays empty-password, and every secret sits
plaintext in `~/.local/share/keyrings/Default_keyring.keyring` — bypassing the
entire ALLOW/ASK/DENY manifest for any local reader. This was found by
accident (a `strings` inspection during unrelated work), not by a test or
threat model. PR #14 adds detection; this plan adds the remediation — the
Seahorse path the doctor check currently recommends **does not work** on
Omarchy/non-GNOME sessions (its password-keyring backend fails to load), so
the fix must be in-product.

The migration design below is not theoretical — it was executed successfully
by hand (Python/secretstorage) on the author's machine: 750 items migrated
into an encrypted `login` keyring, `default` alias repointed, plaintext file
deleted. The plan encodes that proven sequence plus the safety gates an
independent review (Jev decision-model pass) flagged as missing.

## Requirements

- R1: Migrate all items (label, attributes, secret) from the current default
  collection into a new `login` collection via Secret Service API only
- R2: New keyring password is entered into gnome-keyring's own GUI prompt
  during `CreateCollection` — never through omaseal argv/stdin/logs
- R3: Repoint the `default` alias to the new collection so all consumers
  (omaseal, keytar, omarchy-secrets-*, agent tools) follow transparently
- R4: `--dry-run` reports what would migrate without creating anything
- R5: Old plaintext collection is only removed behind an explicit
  `--delete-old` flag, after copy+verify succeeds — never by default
- R6: Fail fast with an actionable error when no graphical session can render
  the daemon's password prompt (headless/SSH), and bound prompt waits so the
  command can never hang indefinitely
- R7: Verify before mutating state — count resolvable items in old vs new;
  abort before `SetAlias`/`Delete` on mismatch
- R8: Update the `keyring-encryption` doctor check's remediation text to point
  at `omaseal keyring migrate` instead of Seahorse

## Key Technical Decisions

- **KTD-1: Copy-migrate, not password change.** The Secret Service spec has no
  ChangePassword method; the only path to an encrypted keyring is create →
  copy → repoint → delete. The daemon's private `ChangeWithMasterPassword`
  family exists but is undocumented gnome-keyring internals — not used.
- **KTD-2: Target collection named `login`.** `pam_gnome_keyring` unlocks by
  password match at login, and `ss.GetLoginCollection()` already prefers
  `/collection/login` — the name is both the PAM convention and the library's
  own. Set the alias after copying, not at creation: gnome-keyring rejects
  non-`default` aliases in `CreateCollection` ("Only the 'default' alias is
  supported").
- **KTD-3: Delete behind `--delete-old`, default keep.** Jev review scored
  delete-by-default as unsafe-to-ship (0.24). Keeping the old collection is
  safe (plaintext remains, warning printed); deleting is opt-in and only
  reachable after verified copy.
- **KTD-4: Pre-flight graphical-session guard + bounded prompt.** `handlePrompt`
  in the vendored `secret_service` package blocks on a signal channel forever.
  Guard: check `WAYLAND_DISPLAY`/`DISPLAY` before starting, then wrap the
  `CreateCollection` call in the goroutine+timeout pattern already used by
  `runSetup` (30s bound) and `guiprompt.go`.
- **KTD-5: Lazy item resolution, honest accounting.** The `Items` property can
  list stale paths that fail on read (observed: 1 of 751). Resolve each path
  lazily and skip `ItemNotFound`. `CreateItem(replace=true)` merges items with
  identical attribute dicts — report "copied N → landed M (K merged)" rather
  than claiming a lossless 1:1 count.
- **KTD-6: Ordering.** create → copy → verify → `SetAlias` → optional delete.
  Every irreversible step sits behind a verified reversible one; a failure
  anywhere before `SetAlias` leaves the system exactly as found.

## High-Level Technical Design

```
omaseal keyring migrate [--dry-run] [--delete-old]
        │
        ▼
  pre-flight ──────────────────────────────► fail fast if headless
   (WAYLAND_DISPLAY/DISPLAY set?              "needs a graphical session —
     daemon reachable? source unlocked?)       the daemon prompts for the
        │                                     new password via GUI"
        ▼
  CreateCollection("login")  ── bounded wait ──► daemon GUI prompt:
        │                                      user enters login password
        ▼
  enumerate source Items ── lazy resolve ──► skip stale paths
        │
        ▼
  per item: read label+attrs+secret ──► CreateItem(new)   [replace=true]
        │
        ▼
  verify: count resolvable in new vs resolved in old
        │  mismatch ──► abort, report, old collection + alias untouched
        ▼
  SetAlias("default", /collection/login)     [point of no return]
        │
        ▼
  --delete-old? ──► Collection.Delete() on old ──► plaintext file removed
        │
        ▼
  report: copied/landed/merged/skipped-stale + autologin remediation hint
```

## Implementation Units

### U1. Migration engine — `engine/keyringmigrate.go`

**Goal:** The D-Bus migration sequence, callable independently of CLI flags.
**Requirements:** R1, R2, R3, R6, R7 · **Dependencies:** none
**Files:** `engine/keyringmigrate.go` (new)

**Approach:** New file, `package main` (matches the engine package layout).
Functions over a small interface so the copy/verify logic is unit-testable
without a daemon:

- `migrateKeyring(dryRun bool) (migrateResult, error)` — orchestrates
  pre-flight → create → copy → verify → alias. Returns counts
  (resolved source, copied, landed, merged, stale-skipped) and paths.
- `enumerateItems(collection dbus.BusObject) []itemRef` — reads the
  `Items` property via raw `Call`/`GetProperty`, resolves each path's
  label+attributes lazily, skips `ItemNotFound` errors, records them as
  stale.
- `copyItems(svc, src, dst, refs)` — `GetSecret` + `CreateItem` per ref.
- `verifyMigration(src, dst)` — count resolvable items on both sides;
  landed-vs-copied delta reported as merges, real shortfall is an error.
- Raw-call helpers: `serviceObject(svc)`, `setDefaultAlias(svc, path)`,
  `deleteCollection(coll)` — thin wrappers on `svc.Object(serviceName,
  path).Call(...)` for `SetAlias`, `Collection.Delete`, and the
  `Collections`/`Items` properties not exposed by the library.
- Prompt bounding: wrap `svc.CreateCollection` in `errgroup`-free
  goroutine + `select`/`time.After` (pattern: `runSetup`'s bounded
  round-trip, `engine/setup.go` ~line 78–90).

**Patterns to follow:** `keyringStore()` session setup in
`engine/keyring.go`; timeout pattern in `engine/setup.go`; error taxonomy
via `newError`/`keyringError` in `engine/errors.go`.

**Test scenarios** (in `engine/keyringmigrate_test.go`):
- Happy path: given a fake item list, `enumerateItems` yields label+attrs
  for each resolvable path
- Edge: item path that errors on read is counted stale and skipped, not fatal
- Edge: `verifyMigration` treats `copied > landed` as merged (ok) but
  `landed < distinct-source-count` as error
- Error: `SetAlias` failure after successful copy → error returned, no panic
- Pure-function coverage only — D-Bus calls stay behind the injected seam

**Verification:** package builds; unit tests green without a daemon.

### U2. CLI surface — `omaseal keyring migrate`

**Goal:** User-facing command with safety flags and headless guard.
**Requirements:** R4, R5, R6 · **Dependencies:** U1
**Files:** `engine/main.go`, `engine/keyringmigrate.go` (or a small
`engine/keyringcmd.go`)

**Approach:**
- `case "keyring":` in `main()`'s switch, sub-arg parsing mirroring the
  `mcp` block: `keyring migrate`, `keyring status` (nice-to-have: report
  default alias target + encryption guess — cheap and useful).
- Flags: `--dry-run` (enumerate + report only), `--delete-old`
  (post-verify removal), `-y/--yes` to skip the interactive
  "proceed?" confirmation in scripted use.
- Default interactive flow: print source path + item count + what will
  happen, require confirmation before `CreateCollection`.
- Headless pre-flight: if neither `WAYLAND_DISPLAY` nor `DISPLAY` is set
  (or `XDG_SESSION_TYPE` unset and no tty), fail with: *"the daemon must
  prompt for the new keyring password via GUI — run this inside a
  graphical session"*.
- Post-migration guidance printed on success: keyring password should
  equal login password for `pam_gnome_keyring` auto-unlock; autologin
  must be disabled for a password to reach PAM; `omaseal doctor` re-check.

**Test scenarios:**
- `--dry-run` on a fake engine calls enumerate but never create/copy
- `--delete-old` absent → old collection untouched even when delete stub
  would succeed
- Headless env (`env -u WAYLAND_DISPLAY -u DISPLAY`) → error before any
  D-Bus mutation call
- Confirmation declined → clean exit, nothing created

**Verification:** `omaseal keyring migrate --dry-run` prints the plan and
exits 0; flag matrix behaves per tests.

### U3. Doctor remediation text points at the new command

**Goal:** The `keyring-encryption` warning routes to the in-product fix.
**Requirements:** R8 · **Dependencies:** U2 (text references the command)
**Files:** `engine/doctor.go`, `engine/doctor_test.go`

**Approach:** Replace the Seahorse remediation lines in
`checkKeyringEncryption`'s failure message with
`omaseal keyring migrate` (+ note that the daemon prompts for the new
password, use login password for PAM auto-unlock, disable autologin).
Keep the "LUKS still protects powered-off" nuance. Update the doctor test
asserting message content if present.

**Test scenarios:**
- Message contains `omaseal keyring migrate`; no longer contains `seahorse`
- Existing plaintext/encrypted fixture assertions unchanged

**Verification:** `omaseal doctor` on a plaintext fixture prints the new
remediation.

### U4. Integration test (opt-in) + docs

**Goal:** Prove the sequence against a real daemon; document the flow.
**Requirements:** R1–R7 · **Dependencies:** U1–U3
**Files:** `engine/keyringmigrate_integration_test.go` (new),
`README.md` (security section)

**Approach:**
- Build-tag or env-gated test (`OMASEAL_IT=1`, or `-tags=integration`)
  matching repo convention — existing `keyring_test.go` already hits the
  live daemon, so the gate is for the *prompt interaction* requirement:
  the test creates a throwaway collection (no password prompt needed if
  created unlocked — verify empirically; if CreateCollection always
  prompts, gate on env and document manual run).
- Scenario: create `omaseal-migrate-test` collection → write items via
  `Set`-equivalent → run engine copy against it → assert items landed →
  delete collection (cleanup).
- README: under security/threat-model, document at-rest encryption, the
  autologin/empty-password relationship, and the migrate command as the
  remediation path.

**Test scenarios:** the integration test itself is the deliverable;
manual matrix: encrypted-after-migrate verified via
`strings login.keyring | grep -c '^secret='` → 0.

**Verification:** `OMASEAL_IT=1 go test ./engine -run Migrate` passes on
a machine with a graphical session; README renders the new section.

## Scope Boundaries

- **In scope:** the migrate command, doctor text, integration coverage,
  README note.
- **Deferred to follow-up work:** `omaseal setup` plaintext detection +
  migrate hint (Jev review: subcommand now, setup hook later — fold in
  when this proves stable); MCP exposure of migrate (deliberately
  excluded — an agent-callable "re-encrypt my keyring" is a prompt-
  injection footgun; this command should be human-initiated only).
- **Out of scope:** changing existing keyring passwords in place
  (impossible via the API); headless/SSH migration support (the daemon
  requires a GUI prompt — documented as a hard limitation, not worked
  around); migrating *locked* source keyrings.

## Risks & Dependencies

- **Prompt dismissal mid-migration:** `handlePrompt` returns
  `dismissed=true` — treat as user abort, clean up nothing (new empty
  collection may linger; harmless, note in output).
- **Partial copy + crash:** safe by construction — alias still points at
  old collection; re-running is idempotent (replace=true).
- **Downstream during alias swap:** a concurrent consumer read between
  `SetAlias` and daemon propagation could miss — vanishingly small
  window; acceptable (reads fail open to existing error handling, not to
  wrong data).
- **`secret_service` version:** v0.2.8 API verified — `CreateCollection`,
  `CreateItem(replace=true)`, `GetLoginCollection` all present; no
  dependency bump needed.
- **aarch64 + x86_64:** pure D-Bus, no arch-specific surface — both
  targets covered by existing build matrix.

## Postmortem Note (why this plan exists)

The plaintext-at-rest state shipped because nothing in the product or
test suite inspected the daemon's backing file — the API path was
correct, so every green test was consistent with plaintext storage. The
doctor check (PR #14) closes the detection gap; this plan closes the
remediation gap. The regression lesson: **threat-model the storage
layer, not just the API layer** — and when a check warns, the fix it
points at must actually work on the target platform (the Seahorse
recommendation did not).

## Sources & Research

- Live migration executed on this machine via python-secretstorage:
  750 items, 0 errors, `login.keyring` verified 0 plaintext `secret=` lines
- Vendored `github.com/zalando/go-keyring@v0.2.8/secret_service` — API
  surface + unbounded `handlePrompt` confirmed by source read
- Jev decision-model spec review (7 questions): safe_to_ship 0.24 →
  drove `--dry-run` (0.81) + `--delete-old` gate (0.72) + headless guard
  (top risk pick); surface=subcommand_only (0.66); tests=both (0.96)
- Empirical quirks encoded: stale `Items` paths, alias restriction at
  CreateCollection, replace-merge accounting, Seahorse backend failure
  on non-GNOME sessions
