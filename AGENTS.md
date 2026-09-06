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
# Store (read secret from stdin, not argv)
printf 'sk-...' | omaseal set <service> <account>

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

- **Never put a real secret in a command argument.** Use `printf` to pipe into
  `set`, or use `omaseal_set` over MCP/IPC.
- **Read secrets only when explicitly asked.** `list` and `omaseal_list` are
  metadata-only; they do not return values.
- **Prefer `resolve` over `get` for app automation.** It falls back to `op` / `bw`
  and caches locally.
- **Use `reveal` only when the user asks for a visible or clipboard copy.** It
  may require a fingerprint.
- **Do not export or share secret values.** The keyring is local-first.
