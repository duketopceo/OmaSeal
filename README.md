# Oma Ring

A system keyring manager for Omarchy. It gives the desktop a macOS Keychain-style secret store without inventing new crypto: it uses the `gnome-keyring` + `libsecret` stack that Omarchy already ships.

## Why

macOS has Keychain. Linux has `gnome-keyring`. But there is no Omarchy-native way to view or request secrets, and every Omarchy plugin currently invents its own storage (`~/.config/<app>/config.json`, `.env` files, etc.).

Oma Ring fixes that by being the single, standard secret interface for Omarchy apps.

## What it uses

- `gnome-keyring-daemon` — the Secret Service backend.
- `libsecret` / `secret-tool` — the CLI/library interface.
- `github.com/zalando/go-keyring` — pure Go client, no CGO.

## Install

```sh
cd engine
go build -o oma-ring .
install -Dm755 oma-ring ~/.local/bin/oma-ring

# Omarchy plugin
cp -r . ~/.config/omarchy/plugins/io.github.duketopceo.oma-ring
omarchy-shell shell rescanPlugins
omarchy plugin enable io.github.duketopceo.oma-ring
```

## Usage

```sh
# Store a secret
printf 'sk-or-...' | oma-ring set openrouter default

# Retrieve a secret
oma-ring get openrouter default

# Delete a secret
oma-ring del openrouter default
```

## Roadmap

- [x] CLI get/set/delete via `gnome-keyring`
- [ ] Panel UI for viewing/searching secrets
- [ ] `omarchy-shell` IPC target for other plugins
- [ ] Premium: collections, import/export, encrypted backup, SSH/2FA helpers

## License

MIT
