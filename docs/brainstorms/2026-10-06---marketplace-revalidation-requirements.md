# Requirements — Marketplace revalidation (audit + drift fix)

Date: 2026-10-06. Route: lfg "next phase" → brainstorm. Selected order: this phase,
then panel extraction, then the different-uid daemon spike.

## Scope decision (user)

**Audit + drift fix only.** No release tag, no AUR publish — releasing stays a
deliberate separate step (a tag triggers release.yml → bump-packaging PR →
AUR sync, and should not ride inside a validation pass).

## Background

The marketplace surface last validated at `547ff45` (Sep 27). Since then the
native-backend arc landed and shipped: #35 Store seam, #38 native backend,
#39 fprintd sender, #40 migrate, #41 key escaping, #42 session hygiene,
#43 agent-unlock lazy-init, #44 sanitizeField/doctor/roadmap. Every
user-facing claim about storage, commands, and capabilities must be re-checked
against HEAD.

## Verified during brainstorm (do not re-derive)

- `manifest.json` `entryPoints.barWidget` is the only entry point needed —
  `Panel.qml` is loaded by `BarWidget.qml` via `Loader` (`source:
  Qt.resolvedUrl("Panel.qml")`), matching the `neo`/`pplx`/`bumblebee`
  convention where a single `barWidget` kind covers the panel UI.
- `manifest.json` `version` is auto-stamped from the git tag by
  `release.yml` (sed in the build job) — leave `0.4.0` as-is; it tracks
  releases, not HEAD.
- Plugin install path is `omarchy plugin add <git-url>` → clone into
  `~/.config/omarchy/plugins/io.github.duketopceo.omaseal/`.

## Requirements

R1. `manifest.json` — fix stale claims. `description` says "built on the
existing gnome-keyring / libsecret stack"; the native age-encrypted backend
is now a first-class opt-in (`backend: native`). Description must reflect
both. Verify every other field (`kinds`, `activation`, `entryPoints`,
`barWidget.*`) still matches shipped reality.

R2. `packaging/aur/` — audit `PKGBUILD`, `PKGBUILD-bin`, `*.install`,
`.SRCINFO`: `pkgdesc` wording, `depends`/`optdepends`, install-hook text.
`org.freedesktop.secrets` stays a hard dep — Secret Service remains the
default backend; native is opt-in. Note (don't implement) whether native
could ever make the dep soft — it can't be dropped while default.

R3. `README.md` + `docs/onboarding.md` — audit user-facing claims: storage
backend, command inventory (`migrate`, `agent` subcommands, `logs
verify/seal`, `sudo`, `run`, `ipc`, doctor checks), install steps, and the
agent-confinement language (`docs/onboarding.md` scope paragraph should
reflect the red-team finding: a live native session file is
plaintext-equivalent key material for same-uid readers — verify current
wording is honest).

R4. `docs/packaging.md` — verify the documented release/AUR flow still
matches `release.yml` + `bump.sh` at HEAD (namcap caveat, SKIP-sums rule,
bump PR flow).

R5. No code changes beyond docs/metadata claims. Any genuine functional gap
surfaced during the audit gets recorded as a follow-up, not fixed inline.

## Non-goals

- Cutting v0.5.0 or any release/tag.
- AUR publish or registration (blocked on human portal step, issue #12).
- omarchy-mac first-party port (separate phase).
- Panel extraction refactor (next phase after this one).
- New features, schema changes, or marketplace listing restructuring.

## Success criteria

- Every user-facing claim in `manifest.json`, `packaging/`, `README.md`,
  `docs/onboarding.md`, `docs/packaging.md` is verifiably true at HEAD.
- A PR lands the drift fixes; anything unverifiable is either corrected or
  recorded as a named follow-up.
