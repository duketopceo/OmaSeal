---
title: Native encrypted backend spike — a store where bad unlocks can't re-key
date: 2026-10-03
execution: code
source: user request — "native backend spike", following the Sept-30 silent re-key incident and the confinement research track
---

# Native Encrypted Backend Spike

## Context

On 2026-09-30 a test daemon's wrong-handed unlock attempt caused
gnome-keyring to silently re-key `login.keyring` — 781 secrets held
hostage by a salt change nobody asked for (`docs/incidents/2026-09-30-
login-keyring-silent-rekey.md`). The failure is architectural: a
symmetric vault where "unlock" and "write the vault" share one
operation means a *failed* unlock can still mutate the file.

Two independent findings push the same direction:

- The pentest's deepest result (#25): every control lives in same-uid
  files — a shell-capable process can bypass the agent gates entirely.
- External prior art (`arenzana/arca`, `qntx/ks`) shows the asymmetric
  fix: **encrypt = public key (always available), decrypt =
  passphrase-gated private key (read-only)**. Writes never need an
  unlocked identity, so a bad unlock is a failed *read* — the store
  file is never touched. The re-key hazard is impossible by
  construction, not by discipline.

This spike produces a design document and a throwaway proof-of-concept.
It does not ship a production backend.

## Requirements

- R1: PoC store where `Set` encrypts with the public identity and works
  while "locked"; `Get` decrypts only when the private identity is
  unwrapped; a failed passphrase attempt performs zero writes to the
  store or identity files.
- R2: Store-format decision documented — per-value ciphertext with
  cleartext metadata (arca model) vs one-file-per-secret (ks model) vs
  fully-encrypted single doc; service/account names stay cleartext-
  queryable to match gnome-keyring session semantics.
- R3: Unlock model — passphrase → argon2id-wrapped age/X25519 identity.
  Passphrase never encrypts the vault directly; rotation re-wraps the
  identity without re-encrypting values.
- R4: Invariants proven in the PoC: atomic writes (temp + rename),
  flock-serialized writers (the audit-log chain lesson), values bound
  to their service/account path (envelope tamper-evidence), bad-unlock
  performs no writes.
- R5: Backend seam sketched — a `Store` interface covering the current
  call surface (`Set`/`Get`/`Delete`/`List`/`Unlock` across
  `engine/keyring.go`, `main.go`, `mcp.go`, `providers.go`, `run.go`,
  `sudo.go`, `keyringmigrate.go`), selectable via config; consumers
  cannot tell which backend serves them.
- R6: Migration story — `omaseal keyring migrate` gains a native
  direction (and back), reusing the existing migrate/doctor shape.
- R7: Confinement evaluation — same-uid is the accepted limit today;
  the spike prices the options (in-process only, different-uid helper
  daemon, polkit-brokered unlock) and recommends one, honestly noting
  that only the helper-daemon options change the confinement answer.
- R8: Memory hygiene assessment — zeroizing key material on lock,
  `PR_SET_DUMPABLE`, mlock/madvise; document what Go can and cannot
  guarantee.
- R9: Compatibility — IPC/MCP/CLI/manifest/presence semantics
  unchanged; `omaseal://` refs, stats, jev, and the panel behave
  identically on either backend.

## Key Technical Decisions

| Decision | Rationale |
|---|---|
| age/X25519 + argon2id identity wrap | `filippo.io/age` is single-file, audited, no cgo, plugin architecture accepts future hardware identities (age-plugin-se exists on macOS); passphrase wraps the identity, not the values — rotation is re-wrap only |
| Parallel backend, not replacement | `backend: secretservice | native` config; gnome-keyring stays for interop (omarchy-secrets-*, keytar, seahorse read `login`); full replacement is a v1 decision after dogfooding |
| Confinement: evaluate + recommend, don't build | Same-uid helper options change the answer materially; the spike's job is to make that choice informed — building the daemon is follow-up |
| Passphrase KDF baseline; hardware binding deferred | SEP on Asahi is optional/disabled and has no userspace keyring API (firmware re-bootstrap + xART/Gigalocker protocol work); passphrase is portable across ARM/x86 |
| PoC is throwaway | Proves invariants, then the design doc + findings decide the real implementation plan; no PoC code lands on main |
| Metadata cleartext, values ciphertext | gnome-keyring already exposes service/account when unlocked; encrypting names breaks `List`-without-unlock parity and adds an index-sync problem for no threat-model gain against the actual attacker (same-uid with unlocked session) |

## High-Level Technical Design

```text
┌─ writes (never need unlock) ──────────────┐
│  Set(svc, acct, secret)                   │
│    → age-encrypt to recipient pubkey      │
│    → atomic append/replace in store file  │
│    → done. identity file untouched.       │
└───────────────────────────────────────────┘
┌─ reads (need unlocked identity) ──────────┐
│  Get(svc, acct)                           │
│    → locked? → presence gate → unwrap     │
│      argon2id identity (fail = no writes) │
│    → age-decrypt value → return           │
└───────────────────────────────────────────┘
┌─ what a "bad unlock" can do ──────────────┐
│  fail argon2id → return error             │
│  store file: untouched (no write path)    │
│  identity file: untouched                 │
│  → the Sept-30 incident cannot recur      │
└───────────────────────────────────────────┘
```

## Implementation Units

### U1. Design doc skeleton — `docs/design/native-store.md`

**Goal:** threat model + format decision written down before any PoC
code, so the PoC tests the design's claims.

**Files:** `docs/design/native-store.md` (new)

**Approach:** document the incident threat model (mutate-on-failed-
unlock), the asymmetric property that removes it, the format decision
(R2 — recommend arca-style single doc, cleartext names, per-value
ciphertext: matches `List` semantics and keeps one atomic file to lock),
and the invariants list that U2 must prove.

**Test scenarios:** none — document unit. Review gate: doc must name
every invariant U2 tests.

### U2. Throwaway PoC — `spike/native-store/` (not merged)

**Goal:** prove R1 + R4 invariants empirically.

**Files:** `spike/native-store/*.go` (new, throwaway — scratch dir, not
committed to main)

**Approach:** ~200 lines: passphrase-wrapped age identity file, store
JSON with per-value ciphertext + cleartext `service`/`account`, atomic
write via temp+rename under `flock`, `Get`/`Set`/`List` only. No IPC,
no presence gate — bare invariants.

**Test scenarios:**

- `Set` succeeds with no unlocked identity (pubkey-only path)
- `Get` with correct passphrase returns value; wrong passphrase returns
  error AND store + identity file mtimes/hashes unchanged
- Concurrent `Set` from 4 processes → no torn writes, no lost values
  (flock serialization)
- Swapped/forged ciphertext for `svc/acct` fails decrypt — value bound
  to its name (envelope tamper-evidence)
- Identity re-wrap (passphrase rotation) leaves all values decryptable
  without re-encryption
- Crash between temp-write and rename leaves the store coherent
  (atomicity)

**Verification:** invariant test list all green; doc updated with
"proven" annotations per invariant.

### U3. Backend seam sketch — `Store` interface design

**Goal:** R5 + R9 — the seam that lets `native` sit beside
`secretservice` without consumers knowing.

**Files:** `docs/design/native-store.md` (interface sketch + call-surface
map)

**Approach:** enumerate the exact current surface (`Set`, `Get`,
`Delete`, `List`, `Unlock`-inside-`keyringStore`, `findItem`,
`itemAttributes`, `readItemMetadata`) and the six caller files; propose
`type Store interface` semantics (unlock state, presence coupling,
`external` provenance flags); write the config selection story
(`backend:` key + `omaseal backend` command sketch). Decide what
`List(service)` and usage analytics mean on the native store.

**Test scenarios:** none — design unit. Review gate: every current
caller in the six files maps to an interface method; no behavior gap
left unnamed.

### U4. Unlock + presence integration analysis

**Goal:** R3 — how unlock/lock/session and the presence gate (fprintd →
GUI confirm → deny) compose with an argon2id identity.

**Files:** `docs/design/native-store.md`

**Approach:** session semantics today are gnome-keyring's (daemon-held
unlocked collection); native store needs its own unlocked-window model
(decrypted identity in memory, `agent unlock` TTL, lock = wipe key).
Decide whether presence-gated ops re-derive or use the session key.
Document memory-hygiene findings (R8): zeroize on lock, dumpable=0 on
the CLI, mlock where Go allows.

**Test scenarios:** none — design unit. Review gate: unlock lifecycle
has a state diagram; every lock path (TTL, explicit, process exit) has
a documented key-disposal story.

### U5. Confinement evaluation — options matrix + recommendation

**Goal:** R7 — price the three architectures honestly.

**Files:** `docs/design/native-store.md` (options matrix)

**Approach:** compare (a) in-process store (status quo confinement:
same-uid reads the identity file + passphrase prompt = full access);
(b) different-uid helper daemon over a unix socket (identity never
leaves the helper's memory; agents get decrypt-service, not key —
the real macOS-style ACL boundary); (c) polkit-brokered unlock on top
of (a). For each: what a same-uid attacker can still do, UX cost
(unlock prompts), build cost, portability. Recommend one.

**Test scenarios:** none — design unit. Review gate: the recommendation
states plainly which same-uid capabilities survive in the chosen design
— no "solved" language where limits remain.

### U6. Decision write-up + roadmap — go/no-go

**Goal:** land the spike's outputs as the project's decision record.

**Files:** `docs/design/native-store.md` (final), `ROADMAP.md`
(confinement track), possibly `docs/plans/` follow-up if greenlit

**Approach:** final doc carries: chosen format, proven invariants, seam
sketch, unlock model, confinement recommendation with honest limits,
migration path, and an explicit go/no-go recommendation with cost
estimate for the real implementation. ROADMAP confinement section gets
the updated status line.

**Test scenarios:** none — decision artifact.

## Deferred to Follow-Up Work

- Building the real `native` backend on the `Store` interface (post-
  spike, if go)
- Hardware-bound identities (age-plugin-se on macOS; SEP-as-keysource
  on Asahi needs SEP userspace work — out of scope entirely)
- Different-uid policy daemon implementation (recommended option only;
  separate plan)
- Agent-sandbox angle (bubblewrap/Landlock around agent processes —
  orthogonal confinement lever worth its own look)
- Test-log isolation (`XDG_STATE_HOME` sandboxing for engine tests)

## Risks

| Risk | Handling |
|---|---|
| Go can't guarantee key-material zeroization (GC copies, strings) | Document as known limit in R8; consider `[]byte`+explicit zero + `runtime.KeepAlive` discipline; honest doc, no overclaim |
| Passphrase UX regression vs gnome-keyring's login-password integration | PAM/module integration deferred; spike notes it as a migration-story cost |
| Cleartext names leak service inventory | Accepted — same exposure as today's unlocked `login` collection; attacker model is exfiltration, not enumeration |
| Asymmetric write-while-locked confuses users ("set works but get asks") | Document in U4; it's a feature — writes never need the human |
| PoC scope creep into a real backend | Explicit throwaway: `spike/` dir, never merged; U6 writes go/no-go |
