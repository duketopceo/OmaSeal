---
title: "macOS menu bar client and a read-only replica Mac"
created_at: 2026-11-12
type: feat
topic: macos-client-and-mini-replica
artifact_contract: ce-unified-plan/v1
product_contract_source: ce-brainstorm
execution: code
---

# Requirements — macOS menu bar client and a read-only replica Mac

## Goal Capsule

- **Objective:** A second OmaSeal instance on the replica Mac that stays
  in sync with the laptop, so agents on the replica can still read secrets when
  the laptop is offline. A SwiftUI menu bar app on the Mac is the human
  front end for search, reveal, and copy.
- **Product authority:** 2026-11-12 — a second instance running and
  syncing on the replica Mac; encrypted connection and store, over SSH or
  Tailscale; agents keep a copy when the operator is on the move and the laptop is
  offline.
- **Engine boundary:** this work does not change `engine/**`. Multi-recipient
  encryption and read-only replica mode come from #72. The macOS CLI build
  comes from #70. This document covers the sync path and the menu bar app.

## Product Contract

### Problem frame

The native store lives on the laptop. When that machine is offline, nothing
else can read those secrets. A raw copy of `store.json` is useless to the
replica Mac unless every ciphertext is also encrypted to the replica's own age
key, and a two-way copy would let a compromised replica write back. The goal is
for the replica to keep a current, decryptable, read-only copy over an encrypted
link, plus a Mac menu bar client that can search and reveal those secrets
without linking the Go engine.

### Requirements

**Replica and encryption set**

- R1. The laptop is the only writer. The replica Mac runs OmaSeal as a
  read-only replica (`replica: true` or `OMASEAL_REPLICA=1` from #72). The
  replica can `Get`, `List`, unlock, and create its own identity; it cannot
  `Set`, delete, import, migrate, or rotate recipients.
- R2. Each secret is age-encrypted to both the laptop key and the replica key.
  The replica prints its public key with `omaseal recipients local`. It is added
  it on the laptop with `omaseal recipients add <age1…> --name <name>`
  (presence-gated rotation, as #72 already specifies).
- R3. Sync copies only the native store document
  (`$XDG_STATE_HOME/omaseal/native/store.json`, or the path the engine
  already uses on that OS). It never copies `identity.age`, `identity.pub`,
  `store.lock`, tmpfs session files, or the committed recipients snapshot.
  Copying a laptop identity onto the replica would collapse the two keys into
  one.
- R4. The copy travels over Tailscale SSH. The file is encrypted at rest
  (age) and the link is encrypted in transit. The install is atomic and
  checksum-verified: a failed or partial transfer leaves the previous
  `store.json` in place.
- R5. An optional allowlist can limit which service/account items the replica
  holds. Default is a full copy of `store.json`. The allowlist is a
  sync-side filter of the document the replica receives; it is not an engine
  change and it does not remove the replica key from the laptop's encryption
  set.

**Sync machinery**

- R6. Laptop-side units live under `contrib/`: an `omaseal-sync` script plus
  a systemd path unit (fires when `store.json` changes) and a low-frequency
  timer for catch-up. The replica Mac has a launchd job, also under `contrib/`,
  that verifies an incoming copy, atomically installs it, and records sync
  status. Neither job overwrites identity files.
- R7. Two-way sync (writes originating on the replica, conflict resolution) is
  out of this work. The replica stays one-way by construction.

**Menu bar app**

- R8. A SwiftUI `MenuBarExtra` app lives under `macos/`. It talks to
  OmaSeal only by shelling out to the `omaseal` CLI. It never links the
  engine and never reads identity files itself.
- R9. The app can search and list service/account names (metadata only,
  no secret values in the list).
- R10. Revealing a secret requires a successful Touch ID prompt through
  LocalAuthentication. After a successful presence check, the app calls
  `omaseal get` and shows the value.
- R11. Copy places the value on the clipboard and clears it after a short
  timeout (45 seconds, or the existing OmaSeal clipboard-clear interval if
  one is already documented).
- R12. A replica status line shows the time of the last successful sync,
  and a stale or failed state when the checksum check failed or the
  interval was missed.

**Quality and CI**

- R13. The app ships with unit tests. A GitHub-hosted macOS runner workflow
  runs those tests on `pull_request`. The workflow must not use a
  self-hosted runner, and it must not add a new self-hosted runner.
- R14. The menu bar app and a runnable `omaseal` on the replica depend on #70
  landing. Replica mode and multi-recipient encryption depend on #72
  landing. This requirements document does not merge those PRs.

### Success criteria

- After a laptop `Set` or a recipient rotation, the replica's `store.json`
  matches the pushed checksum, and `omaseal get` on the replica returns the
  value using only the replica's identity.
- Identity files on the replica are unchanged by a sync, including when a
  transfer is interrupted.
- The menu bar lists service/account names, refuses to reveal without
  Touch ID, copies with auto-clear, and shows a last-sync time from a
  real status file.
- `pull_request` CI for the app is green on a GitHub-hosted macOS runner.

### Scope boundaries

**Deferred for later**

- Two-way sync and conflict resolution.
- Running the menu bar app as a writer on a Mac that is not a replica.
- Keychain backend, launchd service for the engine itself, and any
  `engine/**` change (owned with #70 / #72).
- Cluster copies beyond the replica Mac.
- Syncing manifest, flags, or agent policy; those stay machine-local.

**Outside this work**

- Merging #70, #71, or #72, or any other open OmaSeal PR.
- Printing or committing secret values, passphrases, or age identities.
- New self-hosted runners, or self-hosted jobs on `pull_request`.

### Dependencies / Assumptions

- #72 lands (or is available to build against) before the replica is
  useful: multi-recipient `Set`, `omaseal recipients local` / `add`, and
  `replica` mode.
- #70 lands before the macOS CLI and the menu bar app can be exercised
  on the replica.
- The laptop can reach the replica over Tailscale SSH as the user who owns
  the replica's OmaSeal state directory.
- The operator performs the one-time recipient exchange (replica `recipients local`,
  laptop `recipients add`) at a presence-gated prompt. This work does not
  invent a new key-distribution channel.
- The official encryption-set and replica behavior is as specified in
  #72 and in `docs/design/native-store.md` on that branch. A config-only
  recipient edit on the laptop is not honored; rotation is presence-gated.
