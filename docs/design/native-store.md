# Native Encrypted Store — Design

Status: **spike output (draft)** · plan: `docs/plans/2026-10-03-001-feat-native-backend-spike-plan.md`

## Threat model

The 2026-09-30 incident (`docs/incidents/2026-09-30-login-keyring-silent-rekey.md`)
exposed the load-bearing property of the current backend: gnome-keyring's
symmetric vault couples *unlock* with *write*. A wrong-handed unlock attempt
didn't fail — it **re-keyed the file**, rendering 781 secrets unreadable.
No caller intended a write; the write happened anyway because the backend's
unlock path is a write path.

Two attacker-relevant facts follow:

1. **A failed authentication can mutate the vault.** Any process that can
   reach Secret Service can attempt an unlock; if the attempt re-keys,
   the attacker destroys availability without ever reading a secret.
2. **Every gate lives in same-uid files** (manifest, agent.json, session).
   A shell-capable same-uid process can call Secret Service directly —
   the gates govern only processes that choose to use OmaSeal's
   interfaces (the confinement track's documented limit).

The second fact bounds what any in-process fix can claim; the first is
the one a new backend can actually eliminate.

## The asymmetric property

Both findings point at one structural answer, already proven by two
independent prior-art projects (`arenzana/arca`, `qntx/ks` — both
age-based, agent-facing secret stores):

> **Encrypt = public key (always available). Decrypt = private key
> (passphrase-gated, read-only).**

With an age/X25519 identity:

- `Set` encrypts to the recipient public key — it works while the store
  is "locked" and never touches the identity file.
- `Get` needs the private key — which lives argon2id-wrapped on disk and
  only ever enters memory after a successful passphrase check.
- A wrong passphrase is a **failed read**. There is no code path from
  "unlock attempt" to "write the vault" — the re-key incident is
  impossible by construction, not by discipline.

This is the single property the spike exists to prove and preserve.

## Store format decision

Three candidates evaluated:

| Model | Winner? | Why |
|---|---|---|
| Per-value ciphertext + cleartext metadata in one JSON doc (arca) | **yes** | Matches gnome-keyring `List` semantics exactly; one atomic file to flock; metadata queryable without unlocking — same exposure as today's unlocked `login` collection |
| One file per secret (ks) | no | N files to lock/rotate; `List` becomes directory scan; no win over the doc model for our access pattern |
| Fully-encrypted single doc | no | Every `List` needs the key — breaks unlocked-vs-locked parity and adds an index-sync problem for no threat-model gain (the attacker is same-uid, not disk-read) |

**Decision: arca-style.** One store document; `service`/`account` names
cleartext; each value an age ciphertext bound to its `service/account`
path (envelope tamper-evidence — a value relocated or swapped under a
different name fails decrypt).

## Invariants (the contract U2 must prove)

- **I1 — write-while-locked:** `Set` succeeds with no unlocked identity
  (public-key path only).
- **I2 — bad-unlock-never-writes:** `Get` with a wrong passphrase returns
  an error; store file and identity file bytes are bit-identical before
  and after the attempt.
- **I3 — atomic writes:** a crash between temp-write and rename leaves
  the store coherent (all-or-nothing).
- **I4 — serialized writers:** N concurrent `Set` processes produce no
  torn writes and no lost values (flock — the audit-log chain lesson).
- **I5 — envelope tamper-evidence:** a ciphertext relocated under a
  different `service/account` fails decryption.
- **I6 — rotation without re-encryption:** re-wrapping the identity
  (passphrase change) leaves every stored value decryptable — the values
  were encrypted to the *public* key, which doesn't change.

## Crypto stack

- `filippo.io/age` v1.3.2 — X25519 recipients, argon2id-wrapped
  `X25519Identity` via `age.NewScryptIdentity`/`Encrypt`'s scrypt
  recipient for the identity file itself.
- Passphrase wraps the **identity**, never the values — rotation is a
  re-wrap, not a vault rewrite (I6).
- No cgo, no daemon, auditable single-file format, plugin architecture
  that accepts future hardware identities (`age-plugin-se` on macOS;
  SEP on Asahi is out of scope — no userspace path).

## Backend seam (U3)

The seam already exists and predates this design: `engine/keyring.go`
declares `storeGet`/`storeSet`/`storeDelete` as package vars (tests
swap them for an in-memory store — go-keyring's `MockInit` can't
intercept the direct D-Bus calls). The native backend slots behind the
same vars plus `List`; consumers in `ipc.go`, `mcp.go`, `providers.go`,
`run.go`, `sudo.go`, `keyringmigrate.go` never learn which backend
answered.

### Call-surface map

| Consumer | Calls | Backend sees |
|---|---|---|
| `ipc.go` | `storeGet`, `storeSet`, `storeDelete` | value ops only |
| `mcp.go` | `storeGet`, `storeSet`, `storeDelete` | value ops only |
| `providers.go` | `storeGet`, `storeSet` | value ops only |
| `run.go` | `storeGet` (+`bindings.Set` env, not store) | value ops only |
| `main.go` | `handleSet/Get/Delete/List` → `keyring.*` | value ops + `List(service)` |
| `keyringmigrate.go` | `svc.Unlock`, `keyring.*` | migration source/sink |
| `agent.go` | `svc.Unlock` (presence/session) | unlock semantics |

### Proposed interface (sketch)

```go
type Store interface {
    Get(service, account string) (string, error) // locked → ErrLocked
    Set(service, account, secret string) error   // works while locked
    Delete(service, account string) error        // works while locked
    List(service string) ([]Item, error)         // cleartext names
    Unlock() error                               // presence-gated wrap
    Lock() error                                 // wipe identity
    Locked() bool                                // session state
}
```

`keyring.go`'s swap vars become the dispatch point: `storeGet =
currentStore.Get`, etc., where `currentStore` is chosen once at init
from config (`backend: secretservice | native`, default
`secretservice` — existing installs unchanged). `Unlock` semantics
differ deliberately: Secret Service unlocks a *collection* (daemon-held
session); native unwraps an *identity* into the calling process's
memory. Both map to "locked → presence gate → unlocked" at the call
surface.

`List(service)` on the native store returns cleartext `service/account`
names with `external: false` provenance — the same exposure shape as
the unlocked `login` collection today. Usage analytics, access-log
lines, and the `owned` flag are backend-agnostic (they wrap the store
calls, not the store itself).

## Unlock model + memory hygiene (U4)

Session semantics today are the daemon's: gnome-keyring holds the
unlocked collection; `omaseal` processes share it. The native store has
no daemon — the unwrapped X25519 identity lives in whichever process
unlocked it, so "unlocked" becomes per-process state unless a shared
holder exists. Options:

| Model | What it means | Cost |
|---|---|---|
| Per-invocation unwrap | each `omaseal get` re-derives (argon2id ~0.5s) or prompts | simplest; argon2id latency every call, or a prompt every call — bad agent UX |
| In-memory identity + `agent unlock` TTL | the `agent.json` session model already exists: one unlock, TTL'd; the unwrapped key lives in the *calling* process, not a daemon | matches today's UX; same-uid can read /proc or wait — the confinement limit, honestly stated |
| Long-lived omaseal daemon holding the key | daemon = different trust domain only if different uid (see U5); same-uid daemon adds a process without adding a boundary | only interesting under the U5 helper model |

**Recommendation: in-memory identity + `agent unlock` TTL** — it
reuses the presence-gate and session machinery already shipped (fprintd
→ GUI confirm → deny), keeps `Get` fast after one unlock, and keeps the
key out of any shared file. `Lock` = wipe the identity slice; process
exit = automatic.

**Memory hygiene (R8), honest limits:** Go's GC copies strings; the
unwrapped identity must live as a `[]byte` wiped on `Lock` (plus
`runtime.KeepAlive` discipline), never converted to `string`.
`PR_SET_DUMPABLE` on the CLI prevents core-dump leakage (already a
postmortem follow-up). `mlock` is aspirational — Go doesn't guarantee
it, and the identity passing through `io.ReadAll` makes perfect
hygiene impossible; document it as "wipe on lock, dumpable=0, best
effort," not "guaranteed."

## Confinement options (U5)

The pentest finding stands regardless of backend: same-uid processes
can read the state dir and call the store directly. Three
architectures priced:

| Option | Same-uid attacker can… | Build cost | Confinement gain |
|---|---|---|---|
| **A. In-process store (recommended baseline)** | read `identity.age` (still argon2id-locked — offline brute force only), read store.json ciphertext, watch `omaseal.log` | the spike's PoC, essentially | **marginal** — values can't be read without the passphrase; the unlocked-in-memory window is the exposure |
| **B. Different-uid helper daemon** | see ciphertext + names only; identity file owned by `omaseal-d` uid, never readable by the agent uid | new daemon, unix socket, peer-cred check, presence gate in daemon | **real** — agents get decrypt-*service*, never key material; the only option that closes the same-uid read gap |
| **C. Polkit-brokered unlock on A** | same as A, but unlock needs a polkit action | polkit action file + helper | unlock-time auditability only; the key still lands in same-uid memory after |

**Recommendation: A now, B as the v1.0 confinement track.** A alone
removes the re-key incident and shrinks the exposure to the
unlocked-window — the actual attack requires an *unlocked* session or a
passphrase crack, not a file copy. B is the only option that changes
the same-uid answer materially (macOS-Keychain-style per-app ACLs), and
it costs a daemon, a socket protocol, and a migration story — a plan of
its own. C buys ceremony, not boundary.

## Proven invariants (U2, PoC at `spike/native-store/`)

All six invariants verified empirically (age v1.3.2, 7/7 tests green):

- **I1** `Set` while locked — works; `Get` while locked → `ErrLocked`
- **I2** 5 wrong-passphrase `Unlock` attempts — zero file mutations
  (all files bit-identical before/after, verified by hash)
- **I3** Simulated crash (orphaned `.tmp`) — store reads coherent prior state
- **I4** 40 concurrent writers — zero lost writes (flock serialization)
- **I5** Ciphertext relocated under a forged name → `ErrTamper`
- **I6** Identity re-wrap (passphrase rotation) — all values still
  decrypt; store file untouched (values never re-encrypt)

## Decision (U6)

**Go — native backend is viable, and the architecture is confirmed
before a line of production code shipped.**

The asymmetric bet held under test: `Set` never touches key material
(proven — I1), a wrong passphrase is a failed read and nothing else
(proven — I2, the invariant the Sept-30 incident violated), and the
seam costs nearly nothing because `storeGet`/`storeSet`/`storeDelete`
were already swap vars for tests (I4 proves the file layer can back
them).

**What this greenlights:** backend abstraction + native encrypted
backend as an opt-in `backend: native` config — a v0.7.0/v0.8.0 build
target, gated on the same `agent unlock` presence machinery. Secret
Service stays the default; existing installs and every consumer that
reads `login` directly (seahorse, keytar, `omarchy-secrets-*`) are
untouched.

**What it doesn't:** replacing gnome-keyring wholesale (interoperating
consumers exist), the different-uid helper daemon (the only real
confinement — priced above, its own plan), or hardware binding (SEP on
Asahi has no userspace path; `age`'s plugin architecture keeps
`age-plugin-se` as the macOS future without committing).

**First follow-up tickets:** Store interface extraction behind the
swap vars → `backend:` config key → native backend implementing the
proven file format → migration path (Secret Service → native, the
inverse of `keyring migrate` that already exists).
