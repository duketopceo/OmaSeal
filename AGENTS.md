# Oma Ring — agent access guide

This is the contract for agents (Claude, Codex, MCP clients) that operate the
Oma Ring keyring on this machine.

## What this is

A first-party Omarchy keyring that stores secrets in the existing
`gnome-keyring` / Secret Service stack. It is not a new crypto primitive; it is
a standardized namespace and interface so Omarchy apps and agents can store and
retrieve secrets safely.

## Interfaces

### CLI (preferred)

```sh
# Store (read secret from stdin, not argv)
printf 'sk-...' | oma-ring set <service> <account>

# Retrieve
oma-ring get <service> <account>

# Secure retrieve (fprintd if available)
oma-ring reveal <service> <account>

# Resolve with fallback and cache
oma-ring resolve <service> <account>

# Delete
oma-ring del <service> <account>

# List metadata only
oma-ring list [service] [--json]

# Import from 1Password / Bitwarden
oma-ring import 1password [vault]
oma-ring import bitwarden

# JSON IPC
oma-ring ipc resolve '{"service":"openrouter","account":"default"}'

# MCP stdio
oma-ring mcp
```

### MCP

`oma-ring mcp` exposes:

- `oma_ring_get`
- `oma_ring_resolve`
- `oma_ring_set`
- `oma_ring_delete`
- `oma_ring_list`

## Rules

- **Never put a real secret in a command argument.** Use `printf` to pipe into
  `set`, or use `oma_ring_set` over MCP/IPC.
- **Read secrets only when explicitly asked.** `list` and `oma_ring_list` are
  metadata-only; they do not return values.
- **Prefer `resolve` over `get` for app automation.** It falls back to `op` / `bw`
  and caches locally.
- **Use `reveal` only when the user asks for a visible or clipboard copy.** It
  may require a fingerprint.
- **Do not export or share secret values.** The keyring is local-first.
