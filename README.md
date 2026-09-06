# OmaSeal

A first-party keyring for [Omarchy](https://omarchy.org). It layers a
macOS-Keychain-style secret store over the existing `gnome-keyring` /
`libsecret` stack so every Omarchy plugin can read and store secrets the same
way.

## Why

Linux has had `gnome-keyring` for years. Omarchy plugins currently reinvent
storage in `~/.config/<app>/config.json`, `.env` files, or worse, commit API
keys to dotfiles. OmaSeal is the one standard interface for secrets: ask for
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
go build -o omaseal .
install -Dm755 omaseal ~/.local/bin/omaseal

# Omarchy plugin
cp -r . ~/.config/omarchy/plugins/io.github.duketopceo.omaseal
omarchy-restart-shell
```

## CLI

```sh
# Store
printf 'sk-or-...' | omaseal set openrouter default

# Retrieve (fast, local-only)
omaseal get openrouter default

# Retrieve with best-effort fprintd gate
omaseal reveal openrouter default

# Resolve: local → 1Password → Bitwarden → prompt, with local caching
omaseal resolve openrouter default

# Delete
omaseal del openrouter default

# List metadata (no secrets)
omaseal list
omaseal list openrouter --json

# Import from another vault
omaseal import 1password pace-dev
omaseal import bitwarden

# IPC for other plugins
omaseal ipc resolve '{"service":"openrouter","account":"default"}'

# MCP stdio server for agents
omaseal mcp
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
- OmaSeal only ever sees secrets in memory; it never writes them to files,
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
    "omaseal": {
      "command": "/home/lukedaduke/.local/bin/omaseal",
      "args": ["mcp"]
    }
  }
}
```

Tools: `omaseal_get`, `omaseal_resolve`, `omaseal_set`,
`omaseal_delete`, `omaseal_list`.

## License

MIT
