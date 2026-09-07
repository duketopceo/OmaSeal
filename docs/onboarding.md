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
- whether `~/.local/bin/omaseal` is on `PATH`

## First-time setup

```sh
omaseal setup
```

`setup` runs `doctor`, then prints the exact MCP config and shell `PATH` line to paste into the right files.

## For agents (Claude, Codex, MCP clients)

Add the MCP server to `~/.claude/mcp.json` or `~/.codex/mcp.json`:

```json
{
  "mcpServers": {
    "omaseal": {
      "command": "</absolute/path/to/omaseal>",
      "args": ["mcp"]
    }
  }
}
```

Replace `</absolute/path/to/omaseal>` with the path to the installed binary
(usually `~/.local/bin/omaseal` or `/usr/bin/omaseal`).

Or let OmaSeal write the config for you:

```sh
omaseal mcp install claude
omaseal mcp install codex
```

Available tools: `omaseal_get`, `omaseal_resolve`, `omaseal_set`, `omaseal_delete`, `omaseal_list`.

Agents should:
- call `omaseal_set` or `omaseal_resolve` rather than reading dotfiles
- never put a real secret in a command argument
- prefer `omaseal_resolve` because it falls back to `op` / `bw` and caches locally

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
