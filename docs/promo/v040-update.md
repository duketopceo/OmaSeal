# OmaSeal v0.4.0 update post

## X / 280-character (primary — presence gate, repo link)

OmaSeal is a keyring for Omarchy — one safe place for API keys instead of dotfiles.

v0.4.0: when an AI agent wants to unlock it, you have to be there to approve. Fingerprint, or an on-screen prompt. Neither available? It just refuses.

github.com/duketopceo/OmaSeal

`#Omarchy` `#OmaSeal`

> (swap in plugins.omarchy.org for the repo URL once the listing goes live — submission is still in security attestation)

## X / alternative (Jev angle)

OmaSeal now audits its own access policy — dead rules, uncovered keys, stale secrets. Optional Jev layer proposes fixes that only a human can apply. It sees metadata, never secret values, and is off by default.

`#Omarchy` `#Linux` `#OmaSeal`

## X / thread version (2 posts)

1/ OmaSeal v0.4.0: agents can request an unlock, but approval requires real user presence — fingerprint, else a GUI confirm on your display, else deny. The calling agent's stdin is never consulted; it can't confirm itself.

2/ Also in: `manifest audit` lints your secrets policy locally, `keyring migrate` re-encrypts plaintext keyrings, provider misses are cached so pollers stop hammering 1Password. Off by default where it phones home; fail-closed where it gates.

## LinkedIn

OmaSeal v0.4.0 — the fail-closed agent update.

An MCP-connected agent can run `omaseal agent unlock` on its own. That's the point — but it must not be able to *approve* it. v0.4.0 makes the gate real:

- fingerprint verification when a reader exists,
- GUI confirm (pinentry/zenity) when it doesn't,
- deny when neither exists — never "best effort".

Weakening the policy (`agent mode open`, `ask --ungated`) runs the same presence check. The caller's stdin is never read, so a prompt-injected agent can't click its own dialog.

Also in: `omaseal manifest audit` (local policy linter: dead rules, uncovered items, stale secrets), `omaseal keyring migrate` (in-product re-encryption of plaintext keyrings), and provider negative caching so status pollers stop spawning `op` every tick.

Optional Jev decision layer: proposes manifest fixes from metadata only — never secret values — and only a human applies them.

## Mastodon / Bluesky

OmaSeal v0.4.0 for Omarchy: agent unlock now needs YOU — fingerprint or GUI confirm, fail-closed otherwise. Plus a local manifest linter, keyring re-encryption, and an opt-in Jev advisor that sees metadata only.

`#Omarchy` `#OmaSeal` `#Linux`
