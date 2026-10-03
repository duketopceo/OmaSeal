---
title: Tamper-evident audit log — hash-chained omaseal.log with external anchor
date: 2026-10-01
execution: code
source: user request — "hash chain it is", following the blockchain-fit discussion (ledger rejected; hash chain + anchor kept)
---

# Tamper-Evident Audit Log

## Context

`omaseal.log` (0600, `~/.local/state/omaseal/`) is the forensic record for
agent secret access and, since PR #30, failure telemetry. The confinement
research track already names the gap: every defense lives in same-uid
files, so a shell-capable process that exfiltrates secrets can also edit or
truncate the log that would expose it. A log you can't trust is barely a
log.

Fix: chain each line to its predecessor — `chain_n = sha256(chain_{n-1} ‖
content_n)` — so any mid-history edit, deletion, or splice breaks
verification. Because the same-uid attacker can *recompute* a forged chain
(no secret exists to stop them), integrity of the whole file additionally
requires the chain head to be anchored out-of-band: `omaseal logs seal
<path>` appends `{timestamp, head}` to a user-chosen anchor file (e.g. a
synced git repo), and `omaseal logs verify --anchor <path>` proves every
anchored head still exists in the on-disk chain. This is the systemd-journald
FSS / Certificate-Transparency shape, minus the ledger.

## Requirements

- R1: Every line written to `omaseal.log` carries ` chain=<hex64>` computed
  as `hex(sha256(prev_hex + "|" + line_content))`, covering the timestamped
  line minus the chain field itself. First chained line after a genesis or
  unchained tail uses `prev = hex(sha256("omaseal-log-genesis-v1"))`.
- R2: Resume across processes: at `initLog`, the writer recovers `prev`
  from the last line's `chain=` field; absent → genesis.
- R3: `truncateLogIfLarge` keeps working; a file starting mid-chain is a
  normal state, reported as "starts mid-chain (truncated)" and verified
  pairwise from line 2 — never reported as tamper.
- R4: `omaseal logs verify [--anchor <file>] [--json]` — scans the file,
  reports lines checked, first divergence (line number + reason), current
  head, and anchor-membership verdicts. Exit 0 clean / informational,
  nonzero on tamper.
- R5: `omaseal logs seal <path>` — appends `RFC3339 <head>` to the anchor
  file (0600, created if absent). Anchor semantics: an anchored head absent
  from the on-disk chain = history rewritten = tamper; anchor equal to
  current head = sealed; anchor present-but-behind = "N writes since last
  seal" informational.
- R6: Existing parsers ignore the chain field — `accessLogRe`,
  `opTelemetryRe` (access stats + failure telemetry unchanged), and the
  panel's `logLineRe` strips ` chain=…` from displayed messages.
- R7: A sidecar `omaseal.chain` (0600) mirrors the last-written head —
  catches naive whole-file replacement and gives `seal`/`doctor` a cheap
  head lookup. Documented honestly: a knowing attacker defeats it; the
  anchor is the real guarantee.
- R8: `omaseal doctor` gains a one-line chain status (verified / broken at
  N / no chain yet).

## Key Technical Decisions

| Decision | Rationale |
|---|---|
| Chain field appended to the same log line | Single file, self-verifying, truncation-safe; a sidecar hash table would drift on every rotation |
| `chainWriter` wraps the file half of `LogWriter`'s MultiWriter | `log.Logger` serializes one Write per record, so every line — WriteLog or stray `log.*` — gets chained without touching call sites; stderr stays clean |
| flock + re-resume per write on `omaseal.log.lock` | Found live during verification: many omaseal processes share the log, and a `prev` snapshotted at init forks the chain when siblings interleave (observed: identical `list:` lines with competing chain values). LOCK_EX around re-read-tail+append serializes writers; readers take LOCK_SH so verify never sees a torn mid-append tail. Old binaries write unchained lines, which verify skips — no fork, no tamper flag |
| Stats/telemetry read rotated `omaseal.log.N` siblings too | Rotating the forked-era log aside must not zero the stats history; this also repairs the silent history loss `truncateLogIfLarge` already caused |
| Plain SHA-256, no key | Any key we could hold is reachable by the same-uid attacker anyway; secrecy adds dead complexity. Integrity via chain + external anchor instead |
| Anchor = append-only `{ts, head}` file the user points anywhere | Minimal mechanism covering git-synced, other-machine, or offline copies; no remote automation in v1 |
| Unchained pre-feature lines verify as "pre-chain", not tamper | The live log already has thousands of unchained lines; flagging them would cry wolf |

## Implementation Units

### U1. Chain core — `engine/chain.go` (new)

**Goal:** hashing, resume, verification, and seal primitives, all
path-parameterized for tests.

**Files:** `engine/chain.go` (new)

**Approach:** `chainGenesis` const; `newChainWriter(w io.Writer, statePath
string, prevHex string) io.Writer` buffering partial writes and chaining on
newline boundaries (multi-line Writes split per line); `resumeChainHead
(logPath)` reading the last line's `chain=` or genesis;
`verifyLogChain(path string) chainReport{LinesChecked, FirstDivergence,
Head, StartNote}`; `sealHead(anchorPath, head)` append.

**Tests:** `engine/chain_test.go`

- First line after genesis verifies; sequence of N lines verifies pairwise
- Mid-file edit fails verify at the edit's successor line; mid-file delete
  fails at the line after the gap; forged append with wrong chain fails
- File starting mid-chain: first line "unverifiable", rest verify — no
  tamper verdict
- Partial and multi-line Writes chain identically to single-line Writes
- `sealHead` + `verify --anchor`: anchored head present → pass; file
  rewritten with recomputed chain after seal → anchored head absent →
  tamper; anchor present but behind head → informational "writes since
  seal"

### U2. Wire the writer — `engine/log.go`

**Goal:** every appended line gets chained; head sidecar maintained.

**Files:** `engine/log.go`

**Approach:** `initLog` resolves `prev` via `resumeChainHead`, then
`logWriter = io.MultiWriter(os.Stderr, newChainWriter(f, dir+"/omaseal.chain", prev))`.
Sidecar write is best-effort — logging must not fail if the state file
can't update.

**Tests:** covered by U1's writer tests plus one init-resume test (write,
new process-equivalent resume, continuation verifies).

### U3. Parser tolerance — `engine/analytics.go`, `engine/log.go`

**Goal:** chain field is invisible to stats and panel.

**Files:** `engine/analytics.go`, `engine/log.go`

**Approach:** `accessLogRe` and `opTelemetryRe` gain an optional trailing
`(?:\s+chain=[0-9a-f]{64})?`; `accessLogRe`'s capture becomes non-greedy so
`svc/acct` doesn't swallow the field; `logLineRe` message group strips a
trailing chain field.

**Tests:** extend `analytics_test.go` — chained variants of access and
telemetry lines parse identically to unchained; `logLineRe` message drops
the field.

### U4. Commands — `engine/main.go`

**Goal:** `logs verify` / `logs seal` reachable; doctor reports integrity.

**Files:** `engine/main.go`, `engine/doctor.go` (or wherever the doctor
checks live)

**Approach:** `handleLogs` dispatches `verify`/`seal` before the `[n]`
numeric parse; usage lines added; doctor prints `log chain: verified
(N lines)` / `BROKEN at line N` / `no chain yet`.

**Tests:** handler-level test on a temp `XDG_STATE_HOME` if the harness
supports it; otherwise verify/seal covered via U1's file-level functions.

### U5. Docs — `README.md`, `ROADMAP.md`

**Goal:** honest capability statement.

**Files:** `README.md`, `ROADMAP.md`

**Approach:** README gains `logs verify`/`logs seal` usage + the
threat-model sentence (same-uid recompute is possible; the anchor catches
history rewrites since the last seal — seal early, seal somewhere off the
state dir). ROADMAP confinement track gains the shipped line.

## Risks

| Risk | Handling |
|---|---|
| Knowing same-uid attacker recomputes the chain after editing | Expected — anchor membership is the detection surface; docs say so |
| Attacker deletes log + chain + sidecar wholesale | Detectable only via anchored heads no longer present; cannot prevent deletion of same-uid files |
| Write amplification | One SHA-256 + one 65-byte sidecar write per line — µs-scale, far under the 4ms get budget |
| Test pollution of the real log | Pre-existing wart (tests don't sandbox `XDG_STATE_HOME`), unchanged here; separate follow-up candidate |
