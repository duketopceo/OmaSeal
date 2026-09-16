# Onboarding agents and apps with OmaSeal

OmaSeal is the Omarchy-native keyring. It works like macOS Keychain: any app, plugin, agent, or browser with an MCP client can store and request secrets without inventing its own storage.

## Is it working?

```sh
omaseal doctor
omaseal ping --json
```

`ping` returns a machine-readable health summary that agents and plugins can call without popping a keyring unlock dialog.

This checks:
- the binary and its version
- the `gnome-keyring-daemon` Secret Service backend
- `fprintd` for biometric `reveal`
- `op` (1Password) and `bw` (Bitwarden) CLI availability
- a graphical prompter (GUI pinentry or zenity) for `resolve` without a TTY
- whether `~/.local/bin/omaseal` is on `PATH`

## First-time setup

```sh
omaseal setup          # interactive; --yes to auto-accept
```

`setup` runs `doctor`, installs the OmaSeal MCP server into every detected
agent's config (preserving unrelated entries), and offers to set your primary
agent. It prints a `PATH` hint if `~/.local/bin` is missing from it.

## For agents (Claude, Codex, MCP clients)

Let OmaSeal wire itself into every agent it detects:

```sh
omaseal mcp install-detected
omaseal mcp status          # who is detected / installed
omaseal mcp install claude  # a single agent
```

Each agent's real config path is used (for example Codex's
`~/.codex/config.toml` or Devin's `~/.config/devin/mcp_config.json`); existing
keys in those files are preserved, and writes are atomic so a crash cannot
truncate the file.

Available tools: `omaseal_get`, `omaseal_resolve`, `omaseal_set`, `omaseal_delete`, `omaseal_list`, `omaseal_status` (read-only agent/session state).

Agents should:
- call `omaseal_set` or `omaseal_resolve` rather than reading dotfiles
- never put a real secret in a command argument
- prefer `omaseal_resolve` because it falls back to `op` / `bw` and caches locally
- store and pass `omaseal://<service>/<account>` references verbatim — see `docs/namespaces.md` for the shared service/account conventions

## For BrowserOS and Omarchy apps

BrowserOS integration is planned: it will store provider API keys as
`omaseal://browseros/<provider>/<field>` references and resolve the real secret
server-side before each outbound LLM request. It is not shipped in this
release.

Other apps can use the CLI or the JSON IPC surface:

```sh
omaseal ipc get '{"service":"openrouter","account":"default"}'
omaseal ipc set '{"service":"myapp","account":"api"}' < secret.txt
omaseal ipc list '{"service":"myapp"}'
```

## Error codes

CLI and IPC errors now include a `code` and `help` field:

```sh
$ omaseal get missing account
error: retrieving secret: secret not found in keyring (code: not_found, help: omaseal set)

$ omaseal ipc get '{"service":"missing","account":"x"}'
{"error":"secret not found in keyring","code":"not_found","help":"omaseal set"}
```

Agents can use `code` to pick the right recovery action and `help` to surface the fix to the user.

## For Quickshell / Omarchy plugins

The plugin uses `BarWidget.qml` and `Panel.qml`. After `omaseal` is on `PATH`, enable the plugin with:

```sh
omarchy plugin add https://github.com/duketopceo/OmaSeal.git --enable
omarchy-restart-shell
```

## Error messages

If a command fails, the message will tell you what to check and usually suggests `omaseal doctor`. Run that first before reporting a problem.
