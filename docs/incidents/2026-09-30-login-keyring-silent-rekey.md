# Incident: silent re-key of `login.keyring` by an incompletely isolated test daemon

**Date:** 2026-09-30 (event), resolved 2026-10-01 ~01:40
**Severity:** critical — 758-item secret store unreachable for ~10 hours; ~7 items lost
**Status:** resolved (restored from pre-test backup); preventive rules adopted

## Summary

During a destructive-test suite ("criticality tests": daemon SIGKILL mid-write,
file corruption, file deletion) intended to run in an isolated
`dbus-run-session` sandbox, a test-spawned `gnome-keyring-daemon` resolved the
**real** `~/.local/share/keyrings/` directory and executed gnome-keyring's
built-in re-key path against the live `login.keyring`. The file was rewritten
under new key material (fresh salt + iteration count), rendering the user's
login password — the correct password — invalid. The store was recovered from a
backup taken at 15:00, ~11 minutes before the re-key.

## Root cause

Three compounding failures:

1. **Incomplete environment isolation.** The sandbox script exported
   `HOME`/`XDG_CONFIG_HOME`/`XDG_STATE_HOME`, but the spawned daemons still
   resolved the real keyring directory (keyrings dir derives from
   `XDG_DATA_HOME`/`HOME` resolution order and control-socket discovery that
   the script did not fully redirect). `find /tmp/kr-crit` after the fact
   showed no `home/` dir at all — nothing under scratch was ever used by the
   daemons that mattered.
2. **Control-socket daemon discovery.** The first sandbox attempt logged
   `discover_other_daemon: 1` — the real daemon dual-homed onto the private
   bus via the shared `/run/user/1000/keyring` control socket. From then on,
   `GetConnectionUnixProcessID(org.freedesktop.secrets)` could resolve to the
   **real** daemon, and a `kill -9` phase delivered SIGKILL to it
   (`status=9/KILL` at 15:13:50).
3. **gnome-keyring's silent re-key "feature."** In `gkd-login.c`, when a
   daemon holding a candidate password fails to unlock `login.keyring`, it
   does not merely fail — it **re-encrypts the keyring under that password**
   and logs only `fixed login keyring password to match login password`.
   Designed for `passwd`-change sync via `--login`; here it executed on the
   real file with an unknown submitted value. No confirmation, no error, file
   remains structurally valid — nothing alarms until the next unlock attempt.

## Timeline (all 2026-09-30, -0600, unless noted)

| Time | Event |
|---|---|
| ~15:00:05 | Baseline `omaseal list` = 758 items; backup → `/tmp/kr-backup/login.keyring.pre-crit` (774 file-level records, salt `d245947e`, iter 1787) |
| 15:0x | Destructive suite starts; `discover_other_daemon: 1` logged — real daemon dual-homes onto private bus |
| 15:10:05 | Sandbox daemon pid 1544159 up; cannot find its expected control socket (`/tmp/kr-crit/control`) |
| 15:10:35 | `gcr-prompter` services sandbox-bus prompts; renders on real display via inherited `WAYLAND_DISPLAY` |
| 15:10:53 | Sandbox daemon pid 1554033: `failed to unlock login keyring on startup`; `another secret service is running` |
| 15:11:05.597 | A password prompt on the sandbox bus completes — a secret is submitted (source: a `printf '\n' \| gnome-keyring-daemon --unlock` pipe or a stray on-screen dialog) |
| **15:11:05.620** | **pid 1554033 logs `fixed login keyring password to match login password` → real `login.keyring` re-keyed** (file birth: 15:11:05.625, salt now `2f57c6a2`, iter 1436) |
| 15:11:13 | Real daemon pid 599193 logs `master password for keyring changed without our knowledge`; `find_unlocked_secret_data` assertion spam (in-memory unlocked state orphaned) |
| 15:13:50 | Real daemon SIGKILLed by the suite's kill phase (`daemon_pid` resolved via the shared bus name); systemd restarts it — now genuinely locked |
| 15:50 / 16:37 | User's login password rejected at unlock prompts; repeated gcr prompt cascade from clients touching Secret Service |
| ~17:xx | Stale `gnome-keyring-daemon --unlock` helper (alive since 12:07, pid 195466) found shadowing `org.freedesktop.secrets`; killed |
| 10-01 00:36 | User resets: `mv login.keyring → login.keyring.lost`; fresh daemon, empty keyring |
| 10-01 ~01:00 | Offline verifier (`kr-check.py`: gnome-keyring format — iterated SHA-256 KDF → AES-128-CBC → MD5 integrity) built and proven on a synthetic keyring (`hunter2` → MATCH) |
| 10-01 ~01:30 | Live file: no match on all candidates. **Backup: MATCH on login password, first try** — proves the original key was the login password and the 15:11 re-key was the sole break |
| 10-01 01:36 | Restore: `cp` backup → `login.keyring` + durable `login-774-backup.bak`; `omaseal list` = 758 items; stray `--start` daemon killed; systemd daemon owns bus cleanly |

## Data accounting

- **Restored:** 758 listed items (774 file-level records incl. metadata
  entries) — state as of 15:00.
- **Lost:** ~7 records written 15:00–15:11 (mostly test canaries) remain
  inside `login.keyring.lost`, encrypted under the unknown re-keyed value.
- **Preserved for forensics:** `~/.local/share/keyrings/login.keyring.lost`
  (372,732 bytes) and `/tmp/kr-backup/login.keyring.pre-crit`.

## What made recovery possible

The backup at 15:00:05 — taken before the first destructive command — and an
offline verifier that could check candidate passwords against file bytes
without involving the daemon or exposing plaintext. Restoring was a file copy,
not a data-reconstruction project.

## Preventive rules (mandatory for any future keyring destructive testing)

1. **Verify isolation on the spawned process, not the script.** Before any
   write/kill phase, read `/proc/<daemon_pid>/environ` and confirm `HOME`,
   `XDG_DATA_HOME`, `XDG_CONFIG_HOME`, `GNOME_KEYRING_CONTROL`, and
   `DBUS_SESSION_BUS_ADDRESS` all point at scratch paths. A script exporting
   variables proves nothing about what the daemon resolved.
2. **Assert bus-name ownership before PID-based kills.** The suite must
   refuse to `kill` any `daemon_pid` that is not the spawned child —
   `GetConnectionUnixProcessID` can return the real daemon when control-socket
   discovery dual-homes it.
3. **Tripwire the real keyring dir.** Snapshot `~/.local/share/keyrings/`
   mtimes/hashes before the suite; abort immediately if any real file changes
   during it. This converts a silent 15:11 re-key into a 15:11:06 abort.
4. **`gnome-keyring-daemon --unlock` helpers are long-lived prompt answerers.**
   Never leave them running — the 12:07 helper shadowed the bus for hours and
   confused every subsequent read/write diagnosis.
5. **A backup before destruction is the whole game.** `cp` the keyring dir
   before the suite starts, every time, no matter how confident the isolation
   looks.

## gnome-keyring hazards catalogued (for the native-backend threat model)

- `fixed login keyring password to match login password` — silent re-key on
  failed unlock-with-candidate; a wrong-handed attempt *rewrites the vault*.
- Same-UID collection read: any process on the session bus reads all items of
  an unlocked collection — no per-app ACL (macOS-parity gap).
- Coredumps contain decrypted in-memory secrets, readable by the owning UID
  without sudo — mitigated here via `LimitCORE=0` drop-in on
  `gnome-keyring-daemon.service`; spilled cores must be purged manually.
- Locked-collection `search`/`list` returns empty rather than surfacing a
  locked state — consumers must probe `Collection.Locked` first (fixed in the
  Omarchy secrets panel, omacom/omarchy-mac PR #448, `d9af2888`).
- Stale daemon processes can own `org.freedesktop.secrets` and shadow the
  systemd daemon; always verify bus-name owner PID against
  `systemctl --user status gnome-keyring-daemon`.

## Follow-ups

- [ ] Upstream the offline verifier (`/tmp/kr-check.py` at time of writing —
      `/tmp` is volatile) into this repo as a recovery/diagnostic tool.
- [ ] Native-backend spike: own encrypted store where unlock failure can never
      mutate the file (no re-key-equivalent path).
- [ ] `prctl(PR_SET_DUMPABLE, 0)` on `omaseal` startup — CLI has no systemd
      unit to guard against coredumps/ptrace-by-parent.
- [ ] Rotate credentials that appeared in the 10:18 coredump (GitHub PAT at
      minimum — it was memory-resident during the crash window).
