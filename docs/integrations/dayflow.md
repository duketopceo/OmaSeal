# Dayflow integration

OmaSeal is the default keyring for Dayflow provider credentials.

## Storage convention

Dayflow stores each provider under `service = <provider-name>` and `account =
default`. Examples:

- `service = openrouter`, `account = default`
- `service = ollama`,       `account = default`
- `service = mcp`,          `account = <mcp-server>`
- `service = custom`,       `account = <endpoint-host>`

## Retrieval

Dayflow should call:

```sh
omaseal resolve <provider> default
```

instead of `omaseal get`. `resolve` checks the local keyring first, then
1Password (`op`), then Bitwarden (`bw`), caches the result, and finally prompts
on a TTY if no source has the secret. This means a user with an `op` item named
`openrouter` never has to manually enter an API key.

## Migration

Dayflow's current `~/.config/dayflow/config.json` key (`openrouter_api_key`)
should be migrated on first run:

1. Read the current key.
2. If not already in OmaSeal, store it through stdin without putting it on the
   command line:
   ```sh
   omaseal set openrouter default < /path/to/openrouter_api_key.txt
   rm /path/to/openrouter_api_key.txt
   ```
   The key file should be removed immediately after import.
3. Remove the plaintext key from `config.json` or replace it with a non-secret
   sentinel such as `<omaseal:openrouter/default>` that Dayflow ignores.
4. On every summarization call, resolve the key via `omaseal resolve`.

## Failure behavior

If `omaseal resolve` fails, Dayflow must error cleanly and not call the
provider. Do not fall back to a plaintext copy in `config.json`. No API key is
embedded in process arguments or shell history.

## Security notes

- The key is only in memory while Dayflow is actively calling the provider.
- Dayflow never writes the resolved key to a log.
- The MCP server can be used instead of the CLI: `omaseal_resolve` with
  `{"service":"openrouter","account":"default"}`.
