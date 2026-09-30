# Plan: Test rigor — cover the security surfaces that matter

## Context

Request: "ce plan tests first, then once rigorous tests pass we can go onto
the next" — a coverage hardening pass before the next feature work.

Current state: `go test -cover` reports **36.4%** statement coverage, and
the gaps concentrate on the highest-risk surfaces rather than the boring
ones:

| File | Coverage | Why it matters |
|---|---|---|
| `ipc.go` | **0%** | Agent/plugin JSON surface — get/set/del/list/resolve with NO manifest or mode enforcement (design: trusted-plugin channel; must be a tested contract, not an accident) |
| `keyringmigrate.go` | 19% | Destructive plaintext-deletion path (`--delete-old`) — a bug here loses secrets |
| `mcp.go` | 18% | THE agent channel — manifest DENY/ASK, session gating, tool dispatch |
| `mcpinstall.go` | 24% | Writes agent config files — corrupt output breaks every harness |
| `providers.go` | 28% | Resolution ordering + negative-cache fallback |
| `main.go` handlers | 11% | Thin os.Exit shells — low value, skip except ref parsing |
| `clipboard.go`, `ping.go`, `version.go`, `setup.go` | 0% | Genuinely trivial / hardware-coupled — exempt |

Already well-covered: manifest (85%), guiprompt (78%), keyring (70%),
run (66%), manifestapply (65%), agent (58%).

Key enabler found while scoping: `zalando/go-keyring` ships
`keyring.MockInit()` — an in-memory backend — so `Get`/`Set`/`Delete` can
be exercised end-to-end without a real Secret Service. No tests use it
yet. Also: the `sudoFeed` refactor just established the testable-core
pattern (return exit code + inject writer/seams) that IPC needs.

## Requirements

- R1: **IPC refactor + contract tests.** `runIPC` calls `os.Exit` on every
  error path — untestable. Extract `ipcDispatch(method, jsonArgs string,
  stdin io.Reader) (ipcResponse, int)` returning the response object and
  exit code; `runIPC` becomes the writeJSON+os.Exit shell. Tests with
  `keyring.MockInit()`: get/set/del/list round-trips, invalid JSON →
  `invalid_json`, unknown method → `unknown_method`, empty-secret stdin
  rejection, TTY-stdin rejection (seam `isStdinTTY`), ref-aware
  `omaseal://` service/account parsing, malformed-name rejection.
- R2: **IPC policy question — document, don't change.** `ipc get`
  currently ignores manifest DENY and trust mode entirely. Same-uid
  caveat makes manifest enforcement advisory anyway (a plugin can read
  Secret Service directly), but the absence must be a tested decision:
  tests pin current behavior; plan documents the open question for a
  follow-up (deny-by-default IPC flag? manifest enforcement with
  `trusted-plugin` exempt list?). NOT changed in this pass.
- R3: **MCP enforcement tests.** `handleMCPMessage`/`callMCPTool` against
  mock keyring + temp manifest: DENY → `manifest_denied`; ASK without
  session → refused; ASK with valid session → allowed (session-file seam
  or fixture); lock mode → everything refused; `set` payload containing a
  DENY-matching secret name (`manifestDeniesPayload`) → refused;
  malformed JSON-RPC; unknown tool.
- R4: **Session lifecycle edges.** Expired session refuses; keepalive
  renews within window; `agent lock` revocation takes effect immediately;
  open/ask/lock mode matrix on the agent read path.
- R5: **keyring-migrate deletion safety.** `--delete-old` removes the
  plaintext file only after successful import; absent/corrupt legacy file
  errors out WITHOUT deleting; no flag → legacy file untouched. Temp-dir
  fixtures; never touches the real keyring.
- R6: **Provider resolution order.** keyring hit short-circuits;
  miss → negative-cache consult → provider sweep order; provider error →
  falls through; negative-cache write on miss. (Check existing
  `providercache_test.go` first — add only gaps.)
- R7: **Coverage floor.** `Makefile` target `test-cover` reporting
  per-file and total; CI step uploads/report coverage (non-blocking).
  No hard threshold yet — the goal is visibility, then ratchet.
- R8: No behavior changes outside testability seams. Every extracted
  function keeps identical semantics; the refactor is the risk, so each
  unit ends with the full suite green.

## Out of scope

- Changing IPC's trust/model behavior (R2 is documentation + tests only).
- New coverage floors enforced in CI (advisory only for now).
- Tests for GUI/fprintd/dbus paths — already stub-seamed; hardware paths
  stay untestable.
- Fuzzing/property tests — table-driven is enough for this pass.

## Units

### U1 — IPC refactor + tests (`ipc.go`, `ipc_test.go`)

Extract `ipcDispatch(method, jsonArgs, stdin) (ipcResponse, int)`; move
all `writeJSON+os.Exit` sites into `runIPC`. Add `stdinTTY` var seam
(default `isStdinTTY`). `ipc_test.go` (~200 lines): `keyring.MockInit()`
in TestMain or per-test; round-trip each method; error-path table.

### U2 — MCP enforcement tests (`mcp_policy_test.go`)

Temp `XDG_CONFIG_HOME` manifest fixture (DENY `bank/*`, ASK `api/*`,
open rest) + mock keyring + session fixture file under temp XDG state.
Assert response codes/messages per matrix row. Find and use the existing
session-load seam (`agent.go` session path helpers) rather than new
globals.

### U3 — Session lifecycle (`agent_session_test.go` or extend `agent_test.go`)

Write session fixtures directly (JSON in temp state dir): expired →
denied; within window → allowed; keepalive flag renews `expires`; revoked
session file deleted/missing → denied even in ask mode.

### U4 — keyring-migrate tests (`keyringmigrate_test.go` extend)

Fixture a fake legacy `Default_keyring.keyring` (the tests already have
parsing fixtures — reuse); `--delete-old` deletes only on import success;
corrupt file → error, file preserved.

### U5 — Provider gaps + coverage floor

Audit `providercache_test.go`/`providers.go` for the uncovered paths (28%)
and add only what's missing. `Makefile`: `test-cover` target →
`go test -coverprofile` + `go tool cover -func` tail. CI: report step.

## Verification

- `go vet ./engine`, `go test -count=1 ./engine` — all green.
- `go tool cover -func` uplift evidence: ipc ≥60%, mcp ≥50%,
  keyringmigrate ≥60%; total ≥50% (from 36.4%).
- Refactor smoke: `omaseal ipc ping '{}'` and `omaseal mcp` round-trip
  still behave identically (manual live check).
- No new deps, no behavior changes in non-test paths (diff audit).

## Risks / notes

- The `runIPC` extraction touches every error path in a security surface —
  mechanical move only; the diff must be reviewable as pure motion.
- `keyring.MockInit` is process-global: tests that need it must not run
  parallel to real-keyring tests in the same package — gate via a
  `mockKeyring(t)` helper that calls `keyring.MockInit()` and check the
  mock API surface first (`MockInitWithError` exists for failure paths).
- Session fixtures must use the real on-disk schema — read the struct
  before writing fixtures, don't guess field names.
