# Plan — macOS Keychain backend (opt-in identity holder)

Origin: next engine item after the darwin build (#70) and the replica
replica (#72, #75). Secret Service is Linux-only; on macOS the engine
needs a place to keep the native store's age identity that is not a
D-Bus bus. Depth: Standard.

## Problem Frame

`backend: native` keeps a passphrase-wrapped X25519 identity in
`$XDG_STATE_HOME/omaseal/native/identity.age`. That file is
machine-local (replica sync copies only `store.json`) and is the
at-rest gate once no tmpfs session exists. On macOS the login Keychain
is the natural place for that blob: encrypted by the user's login,
already used by `github.com/zalando/go-keyring` (an existing, no-CGO
dependency), and reachable from a `CGO_ENABLED=0` darwin build.

The Keychain path must not become the default, and it must not steal
the replica's store. The replica decrypts a synced
`store.json` with its own file identity; a Keychain-backed item store,
or a silent Keychain identity on the replica, would break that
contract.

## Requirements

- R1. Opt-in only. The Keychain backend is selected by an explicit
  `{"backend": "keychain"}` in `config.json`. It is never the default
  on macOS or Linux. An empty or omitted `backend` is `native` on
  darwin (Secret Service is not present) and `secretservice` on Linux
  (unchanged).
- R2. Native store stays the item store. `backend: keychain` is a
  native-store variant: items, recipients, and the flock stay in
  `store.json` / `recipients.json` / `store.lock`. The login Keychain
  holds only the passphrase-wrapped age identity. The scrypt wrap, the
  tmpfs session, and unlock semantics are unchanged — Keychain is a
  better at-rest location, not a replacement for the passphrase.
- R3. Replica always uses the native file store. If
  `"replica": true` or `OMASEAL_REPLICA=1` is set, `selectStore`
  installs a file-identity `nativeStore` and ignores `backend:
  keychain` (and any other non-native backend). The replica can still
  create its own `identity.age`. Doctor reports the ignore.
- R4. Linux is unchanged except for a clear refusal: `backend:
  keychain` on a non-replica Linux host is an error (no Keychain).
  Secret Service default, `backend: native`, and every Linux-only
  daemon path stay as they are.
- R5. Tests: a fake in-memory keyring covers the darwin-agnostic
  paths (init writes the blob to the fake and not to `identity.age`;
  unlock lazy-loads; file fallback promote when Keychain is empty and
  `identity.age` exists; replica ignores/refuses Keychain). Darwin-only
  tests behind `//go:build darwin` exercise the real `security(1)`
  go-keyring path and skip if Keychain is unavailable. Existing I1–I6
  and replica tests stay green. `go vet ./...` and `go test ./...`
  pass on Linux; `GOOS=darwin GOARCH=arm64` and `amd64` `go build ./...`
  pass (no CGO).

## Key Technical Decisions

- **KTD-1: identity in Keychain, items in `store.json`.** A third
  Store that puts secrets in Keychain cannot serve the replica (sync
  is `store.json` only) and go-keyring has no List API, so a full
  item backend would need Security.framework/CGO. Rejected for this
  unit. Items-in-Keychain is a later question.
- **KTD-2: keep the passphrase wrap.** The Keychain item is the same
  scrypt-wrapped blob that `identity.age` holds. Login-unlocks-the-store
  (raw `AGE-SECRET-KEY` in Keychain, no passphrase) would change the
  unlock model and the session story; left as an open question.
- **KTD-3: adopt `identity.age`, do not delete it.** If Keychain has
  no item and `identity.age` exists, the first Read promotes the file
  blob into Keychain and uses it. The file is left in place (deleting
  an identity is a data-loss risk). Subsequent Reads prefer Keychain.
  Writes go to Keychain only. Doctor can mention a leftover file.
- **KTD-4: replica ignores, it does not fail closed.** A replica that
  inherited `backend: keychain` (a copied config, a shared template)
  must still start and read `store.json`. `selectStore` forces the
  file-identity native store and doctor warns. Non-replica Linux
  with `backend: keychain` *does* fail closed, with a hint to use
  `native`.
- **KTD-5: seam is an interface, real Keychain is darwin-tagged.**
  `identityHolder` (`Read`/`Write`/`Kind`) and `identityKeyring`
  (`Get`/`Set`/`Delete`) live in untagged files so Linux tests inject
  a mutex-protected memory fake. `systemKeyring()` is `//go:build
  darwin` and wraps `github.com/zalando/go-keyring` (`/usr/bin/security`,
  no CGO). Service `io.github.duketopceo.omaseal`, account
  `native-identity`. No new dependency.

## Implementation Units

- **U1. Plan and config contract.** This document. `omasealConfig`
  accepts `backend: keychain`. `defaultBackend()` is `native` on
  darwin, `secretservice` elsewhere. `selectStore` implements R1–R4.
- **U2. Identity holder in the native store.** `engine/keychain.go`
  (interface, fake, file holder, keychain holder with file fallback).
  `engine/keychain_darwin.go` / `engine/keychain_stub.go`.
  `nativeStore` reads and writes the wrapped blob through the holder;
  `newNativeStoreAt` stays file-based so existing tests are untouched.
  `newNativeStore` attaches the Keychain holder only when backend is
  `keychain` and the instance is not a replica.
- **U3. Doctor, README, ROADMAP.** `checkBackend` describes `keychain`,
  treats it (and replica) as a native item store for the Secret Service
  optional check, and reports when replica ignored a Keychain config.
  README macOS section documents the opt-in and the replica rule.
  ROADMAP gains a U11 line.
- **U4. Tests (same change).** See R5. Verification: `go vet` +
  `go test` on Linux; cross-compile darwin arm64 and amd64.

## Out of scope

- Putting secret items themselves in Keychain.
- Raw (unwrapped) identity in Keychain; Touch ID / partition-ID ACLs.
- Deleting `identity.age` after promote.
- A CLI flag or `OMASEAL_BACKEND` env (config is the explicit opt-in).
- Enabling the full `go test ./...` suite on the `build-macos` job
  (many tests assume a Linux Secret Service session). Darwin-tagged
  tests are for a real Mac.
- Draft #73, PR #67, and any of Luke's machines.

## Open questions for Luke

- Q1. Should a later unit store the raw identity in Keychain so
  unlocking the login Keychain unlocks the store (no passphrase)?
- Q2. After a successful promote, should `omaseal` offer to delete
  `identity.age`, or always leave it?
- Q3. Is an items-in-Keychain Store (`List` via Security.framework)
  wanted later, knowing it cannot be a replica source?

## Verification

- `cd engine && go vet ./... && go test ./...` on Linux.
- `CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build ./...` and the
  same for `amd64`.
- Replica + `backend: keychain` test: `currentStore` is a file-identity
  `nativeStore`.
- Empty config is never `keychain`, on any GOOS.
