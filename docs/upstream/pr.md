# Upstream Omarchy OS integration proposal

This document describes the eventual upstream shape for Oma Ring. The current
plugin is the standalone prototype; the proposal below is the path toward
shipping it as an official Omarchy package.

## Goal

Make `oma-ring` the default keyring facility in Omarchy, equivalent to macOS
Keychain or Windows Credential Locker. Plugins should be able to request a
secret by `service`/`account` without knowing what backend holds it.

## Proposed API

- `org.freedesktop.Secret` compatibility is preserved. Oma Ring does not
  replace `gnome-keyring`; it sits as a user-namespace filter on top of it.
- `omarchy-shell` gains a first-party `oma-ring` IPC target:
  - `get {service, account}`
  - `set {service, account, secret}`
  - `del {service, account}`
  - `list {service?}`
  - `resolve {service, account}`
- `pam_fprintd` integration remains optional and best-effort: `reveal` uses it
  when enrolled, otherwise falls through.

## System-level behavior

- `oma-ring` binary ships in `/usr/share/omarchy/bin` or `/usr/bin`.
- A `omarchy-oma-ring` systemd user socket or D-Bus activation target is not
  needed; the binary is stateless and uses the Secret Service directly.
- Migration from the plugin to system package is `rm -rf
  ~/.config/omarchy/plugins/io.github.duketopceo.oma-ring`; the data stays in
  `gnome-keyring`.

## Security review

- Audit the `reveal` fprintd path to ensure no fingerprint data is logged.
- Review `providers.go` to confirm `op` / `bw` secrets are only cached, never
  logged.
- Confirm `oma_ring_set` MCP tool is only usable when the client is local and
  the user has authorized the agent.

## Testing strategy

1. Unit tests for `keyring.go`, `providers.go`, `mcp.go`.
2. Manual test matrix:
   - Secret set/list/get/delete.
   - `resolve` with `op` and `bw`.
   - `reveal` with and without `fprintd`.
   - Panel operations on a real Omarchy bar.
   - MCP lifecycle with `claude mcp add`.

## Packaging

- AUR `oma-ring-git` and `oma-ring-bin` PKGBUILDs.
- `omarchy` package can add `oma-ring` as an optional dependency.
- Marketplace manifest remains the entry point for non-Arch Omarchy installs.
