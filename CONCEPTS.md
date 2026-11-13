# Concepts

Shared domain vocabulary for this project — entities, named processes, and status concepts with project-specific meaning. Seeded with core domain vocabulary, then accretes as ce-compound and ce-compound-refresh process learnings; direct edits are fine. Glossary only, not a spec or catch-all.

## Secret

A single stored credential addressed by a `(service, account)` pair — `service` names what the credential is for, `account` names which identity within it. The pair is the canonical key everywhere a secret is stored, listed, or resolved.

## OmaSeal agent session

The unlocked window in which `omaseal` answers secret reads without prompting. Unlocking starts a session with a finite lifetime; when it lapses, reads return a locked error while metadata operations such as listing and deleting may still succeed — the lock gates secret disclosure, not store structure.

## Secrets panel

The Omarchy shell plugin (`Panel.qml` plus sibling components) that fronts the secret store: it lists, filters, copies to the clipboard, adds, edits, and deletes secrets, and hosts the inline unlock field that opens an agent session. It is a client of `omaseal`, not an authority — all mutations go through the CLI.

## Secret Service

The `org.freedesktop.secrets` D-Bus API that desktop apps and agents call for credential storage. Only one daemon can own the bus name at a time; on this system that owner is what actually answers every libsecret consumer, so which provider holds the name decides which store is live.

## Rotation flag

A mark on a stored secret saying the credential needs rotation — e.g. the value leaked into a log or transcript. Metadata only: reads keep working, but every read surface warns until a new value is stored (`set` clears it) or the flag is removed with `unflag`.

## Different-UID helper daemon

The planned enforcement principal: a daemon running as a different user ID that owns the store identity and brokers secret access, making manifest policy real for every lane rather than advisory. The provider daemon becomes a thin Secret Service adapter in front of it. Named in `docs/decisions/setec-design-notes.md` and the product roadmap plan.

## Store recipient

An additional age X25519 public key that every native-store secret is
encrypted to, alongside this machine's local identity. Added and removed
with `omaseal recipients`, which re-encrypts the vault behind the strict
user-presence gate. The other machine holds the matching private key,
wrapped in its own `identity.age` — the primary never holds it.

## Read-only replica

A native-store instance that serves `get`/`list` from a synced copy of
`store.json` and refuses every write (`set`, `del`, `import`, `migrate`,
recipient rotation) with `code=replica_readonly`. Enabled by
`"replica": true` in `config.json` or the `OMASEAL_REPLICA` environment
variable. Used on a second machine (a Mac mini) so agents can read
secrets while the primary is offline.

