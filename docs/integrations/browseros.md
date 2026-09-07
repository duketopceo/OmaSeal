# OmaSeal ↔ BrowserOS integration (planned)

> This integration is **deferred** to a follow-up `omarchy-browser` PR. It is not
> implemented in this release. The sections below describe the planned
> convention and fail-closed behavior so the implementation can be added later.

When implemented, BrowserOS will store AI provider credentials in OmaSeal
instead of its own SQLite database. When the option is enabled, BrowserOS will
keep only an `omaseal://` reference in the provider row and resolve the real key
from OmaSeal just before each outbound LLM request.

## Supported credentials

- `apiKey` for OpenAI, OpenRouter, Azure, Anthropic, Google, Moonshot, and `openai-compatible` providers
- `accessKeyId`, `secretAccessKey`, `sessionToken` for AWS Bedrock

OAuth providers (`chatgpt-pro`, `github-copilot`, `qwen-code`) and the BrowserOS gateway continue to use server-managed OAuth tokens and are not moved to OmaSeal.

## Service/account convention

BrowserOS stores one OmaSeal secret per provider credential field:

- `service = browseros`
- `account = <providerId>/<field>`

For example, an OpenRouter provider with id `openrouter-work` stores its API key at:

```
service: browseros
account: openrouter-work/apiKey
```

The stored SQLite value is the reference string:

```
omaseal://browseros/openrouter-work/apiKey
```

## Enabling in BrowserOS (planned)

When the integration lands, the planned enablement flow is:

1. Build and install `omaseal` so it is on `PATH` (default install location is `~/.local/bin/omaseal`).
2. Start BrowserOS. The server will look for `omaseal` in `OMASEAL_PATH`, on `PATH`, in the login-shell `PATH`, and finally at `~/.local/bin/omaseal`.
3. Open **Settings → AI Providers → Add/Edit Provider**.
4. Check **Store credentials in OmaSeal** and enter the API key.
5. Save. The key will be written to OmaSeal and the reference stored in BrowserOS.

If OmaSeal is not available when the checkbox is enabled, the save must fail
with a clear error and nothing may be written.

## Migrating existing plaintext keys (planned)

When implemented, editing an existing provider, re-entering the key, and
checking **Store credentials in OmaSeal** will call `omaseal set` and replace
the plaintext key with the reference. The key will then be available for other
Omarchy apps that use the same `service/account`.

## Manual verification (planned)

Once the integration lands, you will be able to verify stored references with:

```sh
omaseal list browseros --json
```

Resolve a single reference:

```sh
omaseal resolve browseros openrouter-work/apiKey
```

## Troubleshooting

- **"Failed to store credentials in OmaSeal"** — the server could not find or execute `omaseal`. Verify it is installed and on `PATH`, or set `OMASEAL_PATH` to the binary.
- **Chat fails after enabling** — `resolveLLMConfig` resolves references before the outbound request. If resolution fails, the request is aborted and a local configuration error is returned before contacting the provider. Check `omaseal list` and ensure the secret exists.
- **UI does not show the checkbox** — it is hidden for OAuth and other credentialless providers.

## Deferred work

- Expose `omaseal mcp` as a stdio MCP server inside BrowserOS agent chat.
- Migrate OAuth tokens from the BrowserOS SQLite `oauthTokens` table into OmaSeal.
- Wire OmaSeal into Chromium password autofill (requires a separate CDP/extension design).
