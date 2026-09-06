---
artifact_contract: ce-unified-plan/v1
artifact_readiness: implementation-ready
execution: code
product_contract_source: ce-plan-bootstrap
title: "OmaSeal → BrowserOS: provider key integration"
date: 2026-09-06
plan_type: feat
target_repo: omarchy-browser
---

# OmaSeal → BrowserOS: provider key integration

**Target repo:** `omarchy-browser`  
Plan home repo: `OmaSeal`

---

## Summary

Integrate OmaSeal as the system keyring for BrowserOS AI provider credentials. BrowserOS server will resolve provider API keys from OmaSeal at request time and, when the user opts in, store new provider keys into OmaSeal on save, keeping only a non-secret reference in the BrowserOS SQLite `providers` table. The first milestone covers AI provider keys; MCP stdio exposure, OAuth tokens, and browser password autofill are deferred.

---

## Problem Frame

BrowserOS currently stores API keys, AWS credentials, and session tokens in its own SQLite `providers` table (`packages/browseros-agent/apps/server/src/lib/db/schema/providers.ts`). As it becomes an Omarchy-first browser, it should delegate secret storage to OmaSeal so the user has one local keyring and one recovery/backup surface. The integration must:

- Keep public provider reads secret-free.
- Continue working for existing plaintext keys without forcing migration.
- Avoid putting secrets in CLI arguments, server logs, or environment variables.
- Resolve secrets just-in-time for outbound LLM requests only.
- Fail provider saves explicitly when the user has asked to store a key in OmaSeal but OmaSeal is unavailable.

---

## Requirements

- **R1.** BrowserOS server can resolve an `omaseal://` credential reference to a real secret by calling `omaseal resolve <service> <account>`.
- **R2.** Provider `apiKey`, `accessKeyId`, `secretAccessKey`, and `sessionToken` can be written to OmaSeal from the BrowserOS AI settings UI.
- **R3.** Public provider reads (`GET /providers`, `GET /providers/:id`, `GET /providers/default`) continue to expose only `has*` flags and never the resolved value or raw key.
- **R4.** Existing plaintext provider keys keep working without user action.
- **R5.** The dev/dogfood BrowserOS launcher can find `omaseal` on the host.

---

## Key Technical Decisions

- **KTD1 — Credential reference format:** `omaseal://<service>/<account>`, where `service = browseros` and `account = <providerId>/<field>` (e.g. `browseros/openrouter-default/apiKey`). This maps one OmaSeal secret per provider credential field, keeps references short, and avoids a DB schema migration for the first milestone.  
  *Governs R1, R2, R4.*

- **KTD2 — Resolution boundary:** Resolve `omaseal://` references inside `packages/browseros-agent/apps/server/src/lib/clients/llm/config.ts` (`resolveLLMConfig`) before the Vercel AI SDK provider factory consumes the config. This covers `/chat`, `/test-provider`, and `/refine-prompt` from a single point.  
  *Governs R1, R4.*

- **KTD3 — Write boundary:** The `PUT /providers/:providerId` route shells out to `omaseal set <service> <account>` and replaces real credential values with references before calling `providerStore.upsert`. `providerStore` remains storage-agnostic.  
  *Governs R2, R3.*

- **KTD4 — Fail-fast on write:** If the user sends `storeInKeyring: true` and `omaseal set` fails, the request fails. A silent plaintext fallback would undermine the opt-in.  
  *Governs R2, security.*

- **KTD5 — No DB schema migration for v1:** Store the reference string in the existing `apiKey` / `accessKeyId` / `secretAccessKey` / `sessionToken` columns. Real keys and references are distinguished by the `omaseal://` prefix. A future iteration can add an explicit `keySource` column for richer UI state.  
  *Governs R2, R4, scope.*

---

## Scope Boundaries

### In scope

- AI provider credentials for the BrowserOS chat, provider test, and prompt-refine paths.
- Optional per-provider "Store in OmaSeal" opt-in from the AI settings UI.
- Dev/dogfood PATH wiring so the server can locate `omaseal`.

### Deferred to follow-up work

- **stdio MCP server exposure:** make `omaseal mcp` available as a first-class stdio MCP server inside BrowserOS agent chat. Requires extending `packages/browseros-agent/packages/shared/src/schemas/browser-context.ts`, `apps/server/src/agent/mcp-builder.ts`, and the extension MCP add-server UI.
- **OAuth token migration:** move server-managed OAuth tokens (`chatgpt-pro`, `github-copilot`, `qwen-code`, BrowserOS gateway) from the BrowserOS SQLite `oauthTokens` table into OmaSeal. Needs a refresh/expiration story.
- **Browser password autofill:** wire OmaSeal into Chromium password storage. No server/extension abstraction exists today; needs separate CDP/extension design.
- **Production `browseros-cli launch` env injection:** the released Go CLI does not set `Env` today. If OmaSeal is not on the default PATH at GUI launch, this may need a sidecar config field or launcher change.

### Out of scope

- Upstreaming to the public `browseros-ai/BrowserOS` repo, packaging, or CI release work.

---

## Implementation Units

### U1. OmaSeal resolver adapter in BrowserOS server

**Goal:** Add a server-side module that resolves a credential reference by calling `omaseal resolve <service> <account>`.

**Requirements:** R1, R5.

**Dependencies:** `omaseal` binary on `PATH` or at `OMASEAL_PATH`.

**Files:**
- `packages/browseros-agent/apps/server/src/lib/secrets/omaseal.ts` (new)
- `packages/browseros-agent/apps/server/tests/lib/secrets/omaseal.test.ts` (new)

**Approach:**
1. Export `resolveOmaseal(service: string, account: string, opts?: { timeoutMs?: number }): Promise<string | null>`.
2. Spawn `omaseal resolve <service> <account>` with `Bun.spawn` or `child_process`, capturing stdout.
3. If the binary is missing, exit code is non-zero, or stdout is empty, return `null` and log only the exit code / error message (never stdout/stderr).
4. Trim the resolved secret before returning.
5. Optionally expose `isOmasealAvailable(): Promise<boolean>` by probing `which omaseal` / `command -v omaseal` so callers can fail fast.

**Test scenarios:**
- Happy path: resolver returns the trimmed secret from `omaseal` stdout.
- Missing binary: returns `null` and logs a single diagnostic.
- Non-zero exit code: returns `null`.
- Empty stdout: returns `null`.
- Timeout: returns `null` without leaking the process.

**Verification:** `cd packages/browseros-agent/apps/server && bun test lib/secrets/omaseal` passes with mocked `Bun.spawn`.

---

### U2. Resolve OmaSeal references in `resolveLLMConfig`

**Goal:** Ensure every outbound LLM request uses real keys, whether the key is stored as plaintext or as an `omaseal://` reference.

**Requirements:** R1, R3, R4.

**Dependencies:** U1.

**Files:**
- `packages/browseros-agent/apps/server/src/lib/clients/llm/config.ts` (modify `resolveLLMConfig`)
- `packages/browseros-agent/apps/server/src/lib/clients/llm/types.ts` (confirm `ResolvedLLMConfig` credential field types)
- `packages/browseros-agent/apps/server/tests/lib/clients/llm/config.test.ts` (extend or create)

**Approach:**
1. Add a helper `resolveOmasealFields(config: LLMConfig): Promise<LLMConfig>` that inspects `apiKey`, `accessKeyId`, `secretAccessKey`, and `sessionToken`.
2. For any value beginning with `omaseal://`, parse `service` and `account` and call `resolveOmaseal`.
3. Replace resolved values in a shallow copy of the config; leave plaintext and undefined values unchanged.
4. Call `resolveOmasealFields` inside `resolveLLMConfig` before the provider-specific branches (OAuth/server-cred providers skip this path).
5. If a reference cannot be resolved, leave it in place so the provider factory produces a clear "missing apiKey" error rather than swallowing it.

**Test scenarios:**
- `LLMConfig.apiKey` is a real key and is returned unchanged.
- `LLMConfig.apiKey` is `omaseal://browseros/openrouter-default/apiKey` and is replaced with the resolved secret.
- All four credential fields can carry references and are resolved independently.
- A reference that fails resolution remains in the config and leads to a provider-specific error downstream.
- `SERVER_CREDENTIALED_PROVIDERS` (OAuth) are not passed to the resolver.

**Verification:** `bun run test:main` passes; `resolveLLMConfig` unit tests cover the above.

---

### U3. Provider write path stores keys in OmaSeal

**Goal:** When a user saves a provider and opts into OmaSeal, write real keys to OmaSeal and store references in SQLite.

**Requirements:** R2, R3, R4.

**Dependencies:** U1.

**Files:**
- `packages/browseros-agent/apps/server/src/api/routes/providers.ts` (update `UpsertProviderSchema` and PUT handler)
- `packages/browseros-agent/apps/server/src/lib/providers/provider-store.ts` (verify `upsert` accepts the reference string unchanged)
- `packages/browseros-agent/apps/server/tests/api/routes/providers.test.ts` (extend)
- `packages/browseros-agent/apps/server/tests/lib/providers/provider-store.test.ts` (extend)

**Approach:**
1. Add `storeInKeyring?: boolean` to `UpsertProviderSchema`.
2. In the `PUT /providers/:providerId` handler, if `storeInKeyring` is true and a credential field is non-empty and does not already start with `omaseal://`:
   - Compute `service = browseros`, `account = <providerId>/<field>`.
   - Spawn `printf '%s' '<value>' | omaseal set <service> <account>` (never pass the secret as a CLI argument).
   - On success, replace the field value with `omaseal://browseros/<providerId>/<field>`.
   - On failure, return `503 Service Unavailable` (or `400 Bad Request`) with a clear error and do not write anything.
3. If `storeInKeyring` is false or the field is empty/undefined, keep the existing `providerStore` semantics (`withoutAbsentCredentials` preserves the stored value).
4. `providerStore.upsert` stores the reference string in the existing text columns; `publicColumns` continues to expose only `hasApiKey` etc.

**Test scenarios:**
- PUT with `storeInKeyring: true` and a real `apiKey` calls `omaseal set` and stores `omaseal://browseros/<id>/apiKey`.
- PUT with `storeInKeyring: false` stores the plaintext key.
- PUT with `storeInKeyring: true` but `omaseal` unavailable returns an error and does not touch the row.
- Public GET after an OmaSeal write returns `hasApiKey: true` and no secret.
- `getWithCredentials` returns the reference; `resolveLLMConfig` (U2) resolves it for chat.

**Verification:** Provider route tests pass; manual end-to-end: create provider with OmaSeal, chat uses it.

---

### U4. Extension AI settings UI for "Store in OmaSeal"

**Goal:** Let the user opt into OmaSeal when adding or editing a provider.

**Requirements:** R2, R3.

**Dependencies:** U3.

**Files:**
- `packages/browseros-agent/apps/app/screens/ai-settings/provider-form-schema.ts`
- `packages/browseros-agent/apps/app/screens/ai-settings/NewProviderDialog.tsx`
- `packages/browseros-agent/apps/app/screens/ai-settings/NewProviderDialog.test.ts`
- `packages/browseros-agent/apps/app/modules/llm-providers/llm-providers.helpers.ts` (`toProviderPayload`)

**Approach:**
1. Add `storeInKeyring?: boolean` to `providerFormSchema` and `ProviderFormValues`.
2. In `NewProviderDialog`, render a "Store key in OmaSeal" checkbox near the credential fields.
3. Default `storeInKeyring` to `false` for v1 (opt-in). A future iteration can default it based on a `keySource` field.
4. Include `storeInKeyring` in the payload sent to the server by updating `toProviderPayload` and the `LlmProviderConfig` type if needed.
5. When `storeInKeyring` is checked and a credential field is non-empty, the server (U3) writes it to OmaSeal. If the user edits an existing provider and does not re-enter the key, leaving the field blank keeps the stored reference per `withoutAbsentCredentials`.

**Test scenarios:**
- Form validates with and without `storeInKeyring`.
- Submitting with `storeInKeyring: true` sends the flag and the key value in the PUT body.
- Submitting with `storeInKeyring: false` sends plaintext.
- UI tests do not need a running OmaSeal; mock the server response.

**Verification:** `cd packages/browseros-agent/apps/app && bun run test` and `bun run typecheck` pass; manual snapshot/screenshot confirms the new checkbox.

---

### U5. Dev/launcher PATH wiring

**Goal:** The BrowserOS server process launched from dev/dogfood can locate `omaseal`.

**Requirements:** R5.

**Dependencies:** U1, U3.

**Files:**
- `packages/browseros-agent/tools/dogfood/cmd/start.go` (`serverRuntimeEnv`)
- `packages/browseros-agent/tools/dev/proc/sidecar_config.go` (optional `omasealPath` field)
- `packages/browseros-agent/packages/shared/src/schemas/sidecar-config.ts`
- `packages/browseros-agent/apps/server/src/config.ts` (read optional `omasealPath`)
- `OmaSeal/README.md` (install PATH note)
- `OmaSeal/docs/integrations/browseros.md`

**Approach:**
1. In `serverRuntimeEnv`, append `~/.local/bin` to `PATH` if it exists, and/or read `OMASEAL_PATH` from the environment.
2. Optionally extend the sidecar config schema with `omasealPath` so dogfood can point at a non-standard binary location.
3. Make the U1 resolver prefer `OMASEAL_PATH`, then `which omaseal`, then a default fallback to `~/.local/bin/omaseal`.
4. Document in `OmaSeal/docs/integrations/browseros.md` that `omaseal` must be on `PATH` for production launches; `~/.local/bin` is the default install location.

**Test scenarios:**
- Resolver finds `omaseal` when `OMASEAL_PATH` is set.
- Resolver falls back to `PATH` lookup.
- Dev `bun run dev:watch` launches a server that can resolve `omaseal://` references.

**Verification:** Manual dogfood launch from a clean shell with `omaseal` in `~/.local/bin` succeeds in resolving a provider key.

---

### U6. OmaSeal integration documentation

**Goal:** Document the integration, migration path, and service/account conventions.

**Requirements:** R1, R2, R5.

**Dependencies:** U2, U3, U5.

**Files:**
- `OmaSeal/docs/integrations/browseros.md` (new)
- `OmaSeal/README.md` (add BrowserOS section)
- `OmaSeal/AGENTS.md` (update command examples to `omaseal`)

**Approach:**
1. Create `docs/integrations/browseros.md` covering:
   - The `omaseal://browseros/<providerId>/<field>` naming convention.
   - How the BrowserOS server resolves and writes keys.
   - How to migrate an existing plaintext key: edit the provider, re-enter the key, and check "Store in OmaSeal".
   - Troubleshooting (binary not on PATH, `omaseal list` to inspect).
2. Update `README.md` to list BrowserOS integration as a supported consumer.
3. Ensure `AGENTS.md` uses `omaseal` commands (already renamed in this repo).

**Verification:** Doc files render correctly; no broken internal links.

---

## Risks and Dependencies

- **Runtime availability:** If `omaseal` is not on `PATH`, reads degrade to `null` (fail later in provider factory) and writes fail with `storeInKeyring: true`. Mitigated by U5 dev wiring and install docs.
- **Bun `Bun.spawn` stdin handling:** `omaseal set` expects the secret on stdin. Use `printf '%s'` with no newline, or pipe through `Bun.stdin` / `ReadableStream`.
- **Secret logging:** Never log stdout, stderr, or command arguments. The resolver and writer must only log exit codes / spawn errors.
- **UI state v1 limitation:** Without a `keySource` column, the extension cannot tell from a public read whether an existing key is OmaSeal-backed. The v1 checkbox defaults to unchecked on edit; a future schema change can improve this.
- **Single-instance providers:** OAuth providers (`chatgpt-pro`, `github-copilot`, `qwen-code`) use server-side OAuth flows and do not pass through this keyring path. Defer.

---

## Verification Contract

- `cd /home/lukedaduke/Documents/github/personal/OmaSeal/engine && go test ./...`
- `cd packages/browseros-agent && bun run lint && bun run typecheck && bun run test:main`
- `cd packages/browseros-agent/apps/app && bun run typecheck && bun run test`
- Manual end-to-end:
  1. Install `omaseal` to `~/.local/bin` and ensure it is on `PATH`.
  2. Start BrowserOS dev server.
  3. Add an OpenRouter provider with "Store in OmaSeal" checked and a real key.
  4. Verify `omaseal list` shows `browseros/<providerId>/apiKey`.
  5. Send a chat message; verify the request succeeds and `GET /providers` returns `hasApiKey: true` with no key.

---

## Definition of Done

- U1–U6 are implemented and the relevant tests pass.
- The BrowserOS chat, provider test, and prompt-refine paths resolve OmaSeal references before calling the AI SDK.
- The settings UI can store a new provider key in OmaSeal.
- `GET /providers` never returns a resolved secret or plaintext key.
- Existing plaintext provider keys continue to work.
- `OmaSeal/docs/integrations/browseros.md` and `README.md` are updated.

---

## Sources & Research

- `packages/browseros-agent/apps/server/src/lib/providers/provider-store.ts` — provider storage and public projection.
- `packages/browseros-agent/apps/server/src/api/routes/providers.ts` — provider HTTP API.
- `packages/browseros-agent/apps/server/src/api/services/chat-provider-config.ts` — chat hydration boundary.
- `packages/browseros-agent/apps/server/src/lib/clients/llm/config.ts` — `resolveLLMConfig`.
- `packages/browseros-agent/apps/server/src/lib/clients/llm/provider.ts` — AI SDK provider factories.
- `packages/browseros-agent/apps/server/src/lib/clients/llm/test-provider.ts` — provider test path.
- `packages/browseros-agent/apps/server/src/api/routes/provider.ts` — `/test-provider` route.
- `packages/browseros-agent/apps/app/screens/ai-settings/provider-form-schema.ts` and `NewProviderDialog.tsx` — provider form UI.
- `packages/browseros-agent/apps/app/modules/llm-providers/llm-providers.api.ts` and `llm-providers.helpers.ts` — app-to-server provider API mapping.
- `OmaSeal/engine/main.go` and `OmaSeal/engine/mcp.go` — `omaseal` CLI and MCP surfaces.
- `OmaSeal/docs/integrations/dayflow.md` — prior art for provider key naming (`service = <provider-name>`, `account = default`).
- Subagent exploration summary for BrowserOS/OmaSeal integration (2026-09-06) — architecture, MCP transport limitations, CLI/launcher conventions, and Chromium password layer findings.
