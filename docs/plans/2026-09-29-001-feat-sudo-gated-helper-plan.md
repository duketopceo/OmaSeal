# Plan: `omaseal sudo` — a separate, presence-gated sudo feed

## Context

Request: "quick feature, fully separate gated thing made for sudo, totally
optional."

The pain is real on this machine: agents need sudo but cannot type the
password (no Touch ID on Asahi; agent has no TTY of its own), so every sudo
call bounces back to the user by hand. The password already lives in the
keyring (`sudo/lukekimball`), but handing it to `sudo` must require the
user's physical presence — an agent must never be able to self-authorize a
root escalation feed.

Design constraints from the pentest-hardening work (R1/R4):

- A typed "yes" on a pty is forgeable — gating uses
  `requirePresenceStrict` (fingerprint or GUI confirm; never honors
  `allow_ungated`; fails closed with a filesystem-free escape: run `sudo`
  yourself).
- The secret must never appear in argv, env, files, or logs — only in the
  password line sudo reads from stdin.
- "Fully separate": no MCP tool, no IPC verb, no manifest policy change,
  nothing in `setup`. The feature exists only as the `omaseal sudo` verb —
  invoking it is the opt-in. MCP/IPC clients cannot reach it.
- Exit code = sudo's own; signals reach sudo (forward SIGTERM/SIGINT).

## Requirements

- R1: `omaseal sudo [-- secret/svc acct | -r svc/acct] -- <cmd> [args]` —
  default ref `sudo/$USER` (e.g. `sudo/lukekimball` here). `--` optional;
  first non-flag token starts the command, mirroring `omaseal run` syntax.
- R2: Strict presence gate BEFORE the secret read. Fingerprint → GUI
  confirm → refuse. No `allow_ungated` honor (root password feed is a
  policy-grade action, not a read unlock). Headless alternative is literal
  `sudo`.
- R3: Secret flows via `sudo -S -p ""` and
  `stdin = io.MultiReader(secret+"\n", os.Stdin)` — password line first,
  the caller's stdin after. Never in argv/env/files/logs; error paths
  print the ref name, never the value.
- R4: Missing secret → `sudo: no secret for <ref>` exit 127; keyring
  failure → real error exit 1; malformed ref → exit 2. Child exit code
  propagated; SIGTERM/SIGINT forwarded.
- R5: Zero new surfaces: not in `mcp` tool list, not in `ipc` handlers,
  not offered by `setup`, not in `mcpinstall` specs. Usage line +
  README bullet only.
- R6: Tests — parse, miss→127 naming, presence-denied short-circuit (stub
  gate), no-secret-in-output, rate-limit refusal math. Same
  `stubPresence` machinery as `presence_test.go`.
- R7: Rate limit — the threat is prompt flooding, not the secret read
  (presence already gates it). Every ATTEMPT (before presence runs) is
  counted: non-blocking flock so a concurrent caller is refused
  immediately; a sliding window caps attempts (default 5 per 10 min) and
  a minimum interval (default 10s) paces single attempts. Over-limit →
  refuse with retry-after, exit 1. Denied/expired presence prompts burn
  budget too — an agent cannot spam dialogs by never answering. State:
  `~/.local/state/omaseal/sudo-rate.json` + `sudo.lock` (runtime, not
  config). Same-uid caveat documented (a hostile process can delete the
  counters — this bounds polite agents and prompt fatigue, not hostile
  code).
- R8: Audit logging — every attempt writes `sudo:`-prefixed lines to the
  persistent log (invoked command, ref name, presence outcome, fed /
  denied / rate-limited / concurrent-refused). Never the secret value.
  `omaseal stats` shows the `sudo/*` access via the existing Get-path
  counting; `tail -f ~/.local/state/omaseal/omaseal.log` is the watch
  surface.
- R9: Numbat stays OUT of this repo — it is a read-only consumer of
  `~/.numbat/records.ndjson` fed by agent-side hooks, which already
  record an agent's `omaseal sudo` invocation as a spawn event (command
  line visible; internal outcome invisible to hooks). Emitting
  numbat-schema records from omaseal would couple a general Omarchy
  package to one user's plugin internals — wrong direction. The
  `sudo:`-prefixed lines in `omaseal.log` are the structured surface; a
  numbat-side log tailer belongs in the `io.github.duketopceo.numbat`
  plugin repo if ever wanted.

## Out of scope

- `SUDO_ASKPASS` integration / an `omaseal askpass` shim — `-S` covers
  one-shot sudo without temp shims; askpass can come later if re-prompt
  flows matter.
- Caching the sudo timestamp (sudo itself owns `timestamp_timeout`).
- Exposing sudo via MCP — deliberately never.
- Feeding non-sudo prompts (polkit, ssh, git askpass) — later if wanted.
- Numbat / notification / shell-hook integrations — the log is the seam;
  consumers attach externally.

## Units

### U1 — `omaseal sudo` verb

`engine/sudo.go` (~120 lines): arg parse (mirroring `run.go`'s loose
style: `-r/--ref` repeatable-not-needed single flag, `--`, first-bare-token),
`runPresenceStrict(ctx, "feed sudo password to <cmd>", "run sudo yourself — omaseal sudo requires presence")`,
`Get(ref)` (local only — no provider sweep, no prompt), spawn
`sudo -S -p ""` with the MultiReader stdin, `signal.Notify` →
`child.Process.Signal`, exit-code propagation. Dispatch + usage in
`main.go`.

Edge cases: `-r` overrides the ref; `sudo` binary lookup via fixed path
(`/usr/bin/sudo`, `/bin/sudo`) consistent with `fixedPaths` — sudo's path
is not a secret-bearing prompter, `exec.LookPath` is acceptable, but
fixed-first matches house style. Empty command → usage + exit 2.

### U2 — rate limit + audit trail

`engine/sudorate.go`: attempt counter persisted under
`~/.local/state/omaseal/` (`sudo-rate.json`, atomic write) + non-blocking
`sudo.lock` flock (second concurrent attempt → immediate refusal).
`checkSudoRate()` runs BEFORE the presence prompt and records the attempt
either way, so flooding burns budget even when unanswered. Constants:
`sudoRateWindow=10m`, `sudoRateMax=5`, `sudoRateMinInterval=10s`.
WriteLog lines: `sudo: attempt <cmd> via <ref>` /
`sudo: presence denied|no mechanism` / `sudo: rate-limited (retry in Ns)`
/ `sudo: fed`. Never the secret.

### U3 — tests + docs

`engine/sudo_test.go`: arg-parse table; rate-limit math (window window
counting, min-interval, refusal timing) against a temp state dir via
XDG override; presence-denied short-circuit via `stubPresence`.
README: security bullet + usage line. Log-watch line: point at
`tail -f ~/.local/state/omaseal/omaseal.log`.

## Verification

- `go vet ./engine`, `go test ./engine`
- Live: `omaseal sudo -- id -u` → GUI/fingerprint prompt → `uid=0`.
- Live: `OMASEAL_GUI_PROMPT=off omaseal sudo -- id -u` → refused, exit 1,
  nothing fed.
- Live: wrong/absent ref → exit 127 with named ref.
- `omaseal sudo -- sudo -k; omaseal sudo -- id -u` — second run re-prompts
  presence (no caching by omaseal; sudo's own timestamp may apply).

## Risks / notes

- `sudo -S` reads exactly one line per auth attempt; a wrong stored
  password causes sudo to eat the next stdin line — noted in code comment
  (bounded: only the caller's own stdin).
- MultiReader means the child's stdin still works for interactive
  commands after auth.
- `system/sudo` also exists in this keyring — default is `sudo/$USER`;
  `-r system/sudo` covers alternates.
