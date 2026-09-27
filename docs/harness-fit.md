# Harness Fit — putting OmaSeal where each agent actually looks

Every agent harness can reach secrets in three places: its own auth storage,
MCP server config (`command`/`args`/`env`), and the process environment.
OmaSeal fits all three without any config file holding a raw key.

## The universal mechanism: `omaseal run`

`omaseal run` materializes secrets into a child's environment at spawn:

```bash
omaseal run -e GITHUB_TOKEN=github/token -- npx -y @mcp/github
omaseal run -e KEY=omaseal://svc/acct -e OTHER=svc2/acct2 -- my-server
```

- `-e`/`--env` is repeatable; `--env=NAME=ref` also works.
- Refs: `service/account` or `omaseal://service/account`.
- Missing secret → named error on stderr, exit 127; the child never starts.
- Reads go through `Get` (local keyring) — no provider sweep, no GUI prompt,
  so spawned MCP servers can't block on interaction. `--resolve` opts into
  the full fallback chain for interactive use.
- The child inherits your environment plus the injected names.

**The config holds a command, not a secret.** Nothing to interpolate, nothing
to `.gitignore`, no shell-env dependency.

## Per-harness matrix

| Harness | Config file | MCP env support | Best fit |
|---|---|---|---|
| Claude Code | `~/.claude.json`, `.mcp.json` | `env` literals + `${VAR}` | `omaseal run` wrapper; `apiKeyHelper` for Claude's own key (below) |
| Codex | `~/.codex/config.toml` | `env` literal only | `omaseal run` in `command`/`args` |
| Cursor | `~/.cursor/mcp.json`, `.cursor/mcp.json` | `${env:NAME}` (version-flaky, needs shell env) | `omaseal run` — drops interpolation *and* the shell-env dependency |
| Devin (local) | `~/.config/devin/mcp_config.json` | `env` literals | `omaseal run`; cloud Devin can't see your keyring — use platform secrets there |
| OpenCode | `~/.config/opencode/opencode.json` | `{env:NAME}` interpolation | `omaseal run` in `command` |
| Antigravity (`agy`) | `~/.agy/mcp.json` | `env` literals | `omaseal run` |
| Hermes | `~/.hermes/mcp.json` | `env` literals | `omaseal run` |

### Example: a GitHub MCP server, wired the OmaSeal way

```jsonc
// .mcp.json / ~/.cursor/mcp.json — works in every harness that takes command+args
{
  "mcpServers": {
    "github": {
      "command": "omaseal",
      "args": ["run", "-e", "GITHUB_TOKEN=github/token", "--", "npx", "-y", "@modelcontextprotocol/server-github"]
    }
  }
}
```

Compare with the env-block form — the key sits in the file:

```jsonc
"env": { "GITHUB_TOKEN": "ghp_realsecret..." }   // or "${GITHUB_TOKEN}" — still needs it in your shell env
```

## Claude Code: `apiKeyHelper` for Claude's own key

Claude Code supports an `apiKeyHelper` setting — a command it executes whose
**stdout becomes the API key**, re-run on a TTL. OmaSeal's `get` is exactly that
contract:

```jsonc
// ~/.claude/settings.json
{ "apiKeyHelper": "omaseal get anthropic default" }
```

The key lives only in the keyring — never in `settings.json`, never in a file.
`omaseal setup` offers this wiring on machines with Claude installed.

## Codex: shared store

Codex CLI's own auth already lands in the OS keyring (Secret Service) on
Linux — the same store OmaSeal wraps. So a Codex-side credential written by
Codex is already in the shared store; `omaseal run` covers the *other* keys
Codex MCP servers and shell tools need.

## What `omaseal run` is not

- It does not weaken the manifest/presence boundary — those govern the
  **agent channels** (MCP/IPC/`agent unlock`). A spawned child is a local
  process with user-level access; the Secret Service session itself already
  trusts it (same as `env` blocks today). `run` just removes the copy-paste
  plaintext middle step.
- It is not a prompt surface. If a secret is missing it exits instead of
  asking — agents can't block on it, and can't be prompt-injected into
  approving anything through it.
- For cloud harnesses (Devin cloud agents, Cursor background VMs) the local
  keyring isn't reachable — use each platform's native secrets UI there.
