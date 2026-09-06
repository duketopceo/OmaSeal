# Oma Ring

A first-party keyring for [Omarchy](https://omarchy.org). It layers a
macOS-Keychain-style secret store over the existing `gnome-keyring` /
`libsecret` stack so every Omarchy plugin can read and store secrets the same
way.

## Why

Linux has had `gnome-keyring` for years. Omarchy plugins currently reinvent
storage in `~/.config/<app>/config.json`, `.env` files, or worse, commit API
keys to dotfiles. Oma Ring is the one standard interface for secrets: ask for
`service / account`, get back the secret, and never worry about where it lives.

## What it uses

- `gnome-keyring-daemon` — Secret Service backend (already on Omarchy).
- `github.com/zalando/go-keyring` — pure Go, no CGO.
- `github.com/godbus/dbus/v5` — direct D-Bus when needed.
- Optional `op` (1Password) and `bw` (Bitwarden) CLI bridges for imports and
  fallback resolution.

## Install

```sh
cd engine
go build -o oma-ring .
install -Dm755 oma-ring ~/.local/bin/oma-ring

# Omarchy plugin
cp -r . ~/.config/omarchy/plugins/io.github.duketopceo.oma-ring
omarchy-restart-shell
```

## CLI

```sh
# Store
printf 'sk-or-...' | oma-ring set openrouter default

# Retrieve (fast, local-only)
oma-ring get openrouter default

# Retrieve with best-effort fprintd gate
oma-ring reveal openrouter default

# Resolve: local → 1Password → Bitwarden → prompt, with local caching
oma-ring resolve openrouter default

# Delete
oma-ring del openrouter default

# List metadata (no secrets)
oma-ring list
oma-ring list openrouter --json

# Import from another vault
oma-ring import 1password pace-dev
oma-ring import bitwarden

# IPC for other plugins
oma-ring ipc resolve '{"service":"openrouter","account":"default"}'

# MCP stdio server for agents
oma-ring mcp
```

## Quickshell panel

A `BarWidget` and `Panel` are included:

- Click the **O** in the bar.
- Browse stored secrets.
- `+ Add` creates a new `service / account / secret`.
- The copy button runs `reveal` and uses `wl-copy` with a 30-second clear.
- `r` refreshes; `a` toggles the add form.

## Security model

- Secrets live in the Secret Service default/login collection, encrypted at
  rest by `gnome-keyring`.
- Oma Ring only ever sees secrets in memory; it never writes them to files,
  logs, argv, or the panel state.
- `list` returns metadata only.
- `reveal` triggers the `fprintd` gate when a reader is enrolled; on systems
  without one it falls through to the local secret.
- `resolve` falls back to `op` / `bw`, but always caches the result locally so
  the secret is not re-requested from the external vault.

## Agent / MCP

```json
{
  "mcpServers": {
    "oma-ring": {
      "command": "/home/lukedaduke/.local/bin/oma-ring",
      "args": ["mcp"]
    }
  }
}
```

Tools: `oma_ring_get`, `oma_ring_resolve`, `oma_ring_set`,
`oma_ring_delete`, `oma_ring_list`.

## License

MIT
