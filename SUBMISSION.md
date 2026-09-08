### Repository URL

https://github.com/duketopceo/OmaSeal

### Category

System

### Tags

security, bar, quickshell

### Suggest a missing tag

_No response_

### Maintainer notes

OmaSeal is a first-party Omarchy keyring that gives the desktop a macOS Keychain-style secret store. It uses the existing `gnome-keyring` / Secret Service stack and adds an Omarchy-native CLI, Quickshell panel, `omarchy-shell` JSON IPC, and MCP server so any plugin or agent can store and request secrets without inventing its own storage.

Key features:
- `get / set / delete / list` over `gnome-keyring`.
- `resolve` with automatic fallback to 1Password (`op`) and Bitwarden (`bw`), caching locally.
- `reveal` with a best-effort `fprintd` fingerprint gate.
- Quickshell bar widget and panel for browse/add/copy/delete.
- MCP stdio server for agents: `omaseal_get`, `omaseal_resolve`, `omaseal_set`, `omaseal_delete`, `omaseal_list`.
- JSON IPC surface for other Quickshell/Omarchy plugins.
- Agent access modes (`open`, `ask`, `lock`) with biometric unlock.

Installation:

```sh
yay -S omaseal
# or for the prebuilt binary
yay -S omaseal-bin
```

Removal:

```sh
omarchy plugin disable io.github.duketopceo.omaseal
omarchy plugin remove io.github.duketopceo.omaseal
rm -f ~/.local/bin/omaseal
```

Permissions / dependencies:
- Requires `gnome-keyring-daemon` (Secret Service provider). Already on Omarchy.
- Optional 1Password CLI (`op`) or Bitwarden CLI (`bw`) for import/resolve.
- Optional `fprintd` for the biometric `reveal` and `agent unlock` gate.
- Uses `wl-copy` for the panel's copy-to-clipboard action.

Privacy / consent:
- Secrets are stored only in the local Secret Service collection; no cloud or network is used by the core keyring.
- 1Password/Bitwarden calls are local CLI invocations; the bridge caches the secret in `gnome-keyring` after the first resolution.
- `list` returns metadata only; secret values are never printed by default.

License: MIT

### Submission checklist

- [x] The repository is public and contains installation and removal instructions.
- [x] I have documented the plugin license and any external dependencies.
- [x] I confirm that I own or have permission to submit this plugin and its preview assets.
- [x] The plugin does not overwrite user configuration without explicit consent.
- [x] I understand that approval is for listing and is not a security review.
