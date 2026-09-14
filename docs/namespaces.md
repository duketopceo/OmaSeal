# Service/account namespaces

OmaSeal stores every secret under a `service` / `account` pair. Two naming
conventions coexist, and picking the right one decides whether other
applications can share the credential.

## Canonical form

- **service**: one segment — `[A-Za-z0-9._~-]`, max 256 bytes.
- **account**: one or more segments — `[A-Za-z0-9._~/-]`, max 256 bytes.

No whitespace, no control characters, no `%`. There is no percent-encoding in
`omaseal://` references; a name that needs one is outside the alphabet by
design.

The alphabet is enforced on **writes** (`set` on every surface — CLI, MCP,
IPC). **Reads** (`get`, `del`, `reveal`, `list`, `resolve`) are permissive so
entries created before validation existed — or imported from 1Password /
Bitwarden titles with spaces — stay reachable and deletable. To update a
legacy non-conforming entry, delete it and re-add it under a conforming name.

## `omaseal://` references

Anywhere the CLI takes `<service> <account>`, a single reference works too:

```
omaseal://<service>/<account>
```

The account may span segments, so `omaseal://browseros/openrouter-work/apiKey`
parses to service `browseros`, account `openrouter-work/apiKey`. The scheme
token is case-insensitive (`OMASEAL://…` is accepted). Malformed
`omaseal`-scheme strings (`omaseal:x`, `omaseal:///acct`) are errors, never
silent literals. `omaseal list` accepts `omaseal://<service>` with an optional
trailing slash; a reference carrying an account is rejected there.

**Agent surfaces (MCP tools and `omaseal ipc`)** take either separate
`service`/`account` fields or a verbatim `omaseal://<service>/<account>`
reference in the `service` field. A reference and a separate `account` may not
be combined. Note that an account may begin or end with `/` — `svc//x` parses
to account `/x`; the charset permits it, so keep segments meaningful.

**References are capability pointers, not secrets.** Storing
`omaseal://browseros/x/apiKey` in a database or config file avoids embedding
the key — but any process that can run `omaseal resolve` on that reference gets
the real secret back. Files containing references still need protection; the
reference just narrows the blast radius from "key material at rest" to "a
pointer requiring keyring access".

## Provider-owned namespaces (shared credentials)

```
service = <provider>     account = default
```

Examples: `openrouter/default`, `ollama/default`, `anthropic/default`.

Use this when **one credential serves many applications**. A user's OpenRouter
key should be stored once and resolvable by Dayflow, BrowserOS, agents, and
anything else — not duplicated per app.

This convention also preserves `omaseal resolve`'s external fallback: the
service name doubles as the item title when searching 1Password (`op`) or
Bitwarden (`bw`), so `resolve openrouter default` can find a vault item named
"openrouter" without any OmaSeal entry at all.

See [`integrations/dayflow.md`](integrations/dayflow.md) for a consumer that
uses this convention.

## App-owned namespaces (private or multi-field credentials)

```
service = <app>          account = <context>/<field>
```

Examples: `browseros/openrouter-work/apiKey`, `browseros/aws/secretAccessKey`,
`myapp/instance-a/token`.

Use this when the credential is **private to one application** or when one
logical credential has **several fields** (Bedrock's `accessKeyId` +
`secretAccessKey` + `sessionToken`). The app name occupies the service so the
namespace cannot collide with a shared provider key, and the account carries
the per-credential structure.

See [`integrations/browseros.md`](integrations/browseros.md) for a consumer
that uses this convention.

## Choosing

| Question | Convention |
| --- | --- |
| Would another app reasonably use this same credential? | Provider-owned: `<provider>/default` |
| Is it one field per credential? | Provider-owned is fine |
| Several fields, or app-specific instances? | App-owned: `<app>/<context>/<field>` |
| Must it fall back to an `op`/`bw` vault item by title? | Provider-owned — service is the title |

When in doubt, prefer provider-owned: deduplication across the keyring is the
point of a shared store.

## Parser test vectors

Consumers implementing their own `omaseal://` parser (e.g. BrowserOS's
TypeScript side) must agree with these vectors:

| Input | Result |
| --- | --- |
| `omaseal://openrouter/default` | service `openrouter`, account `default` |
| `OMASEAL://svc/acct` | service `svc`, account `acct` (scheme is case-insensitive) |
| `omaseal://browseros/openrouter-work/apiKey` | service `browseros`, account `openrouter-work/apiKey` |
| `omaseal://svc/a/b/c` | service `svc`, account `a/b/c` |
| `omaseal://svc` | service `svc`, empty account — valid for `list`, invalid for get/set/del/reveal/resolve |
| `omaseal://svc/` | same as above |
| `omaseal://` | error: empty service |
| `omaseal:///acct` | error: empty service |
| `omaseal:svc/acct` | error: malformed omaseal reference |
| `omaseal:/svc/acct` | error: malformed omaseal reference |
| `omaseal://svc/acct%20x` | error: `%` is not in the alphabet |
| `omaseal://svc/acct with space` | error: whitespace rejected |
| `omaseal://` + 300 chars + `/x` | error: component exceeds 256 bytes |
| `foo://bar` | **not** an omaseal reference — treated as a literal argument (which then fails the service alphabet) |

The reference implementation is `engine/refs.go`; its tests in
`engine/refs_test.go` are the conformance suite.
