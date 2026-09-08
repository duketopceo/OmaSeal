# OmaSeal — agent access guide

This is the contract for agents (Claude, Codex, MCP clients) that operate the
OmaSeal keyring on this machine.

## What this is

A first-party Omarchy keyring that stores secrets in the existing
`gnome-keyring` / Secret Service stack. It is not a new crypto primitive; it is
a standardized namespace and interface so Omarchy apps and agents can store and
retrieve secrets safely.

## Interfaces

### CLI (preferred)

```sh
# Store (read from a hidden prompt or a secure file; never put it in argv)
omaseal set <service> <account>

# Or, if the secret is already in a file:
# omaseal set <service> <account> < /path/to/secret.txt

# Retrieve
omaseal get <service> <account>

# Secure retrieve (fprintd if available)
omaseal reveal <service> <account>

# Resolve with fallback and cache
omaseal resolve <service> <account>

# Delete
omaseal del <service> <account>

# List metadata only
omaseal list [service] [--json]

# Import from 1Password / Bitwarden
omaseal import 1password [vault]
omaseal import bitwarden

# JSON IPC
omaseal ipc resolve '{"service":"openrouter","account":"default"}'

# MCP stdio
omaseal mcp
```

### MCP

`omaseal mcp` exposes:

- `omaseal_get`
- `omaseal_resolve`
- `omaseal_set`
- `omaseal_delete`
- `omaseal_list`

## Rules

- **Never put a real secret in a command argument.** Type or paste it at the
  hidden prompt, redirect from a secure file, or use `omaseal_set` over MCP/IPC.
- **Read secrets only when explicitly asked.** `list` and `omaseal_list` are
  metadata-only; they do not return values.
- **Prefer `resolve` over `get` for app automation.** It falls back to `op` / `bw`
  and caches locally.
- **Use `reveal` only when the user asks for a visible or clipboard copy.** It
  may require a fingerprint.
- **Do not export or share secret values.** The keyring is local-first.

## Agent access control (OmaSeal 0.2.1+)

Users can gate MCP access with `omaseal agent`:

```sh
omaseal agent mode open   # agents may read/write secrets
omaseal agent mode ask    # agents must unlock before each session
omaseal agent mode lock   # agents cannot access secrets
omaseal agent unlock      # biometric/best-effort session unlock
omaseal agent lock        # revoke the active session
omaseal agent status      # show mode and session state
```

In `ask` mode, the user must run `omaseal agent unlock` before an agent can
use any MCP tool that touches the keyring. `unlock` uses `fprintd` when a
fingerprint reader is enrolled; otherwise it authorizes the session with a
warning. The session lasts 15 minutes by default and is stored in the user's
runtime directory so it is cleared at logout.

Rules for agents:

- Respect `agent_unauthorized` errors: stop and ask the user to `omaseal agent unlock`.
- Do not attempt to bypass the gate by writing the session file directly.
- In `ask` mode, still prefer `resolve` over `get` and `set` only when asked.
