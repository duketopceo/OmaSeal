---
artifact_contract: ce-unified-plan/v1
artifact_readiness: implementation-ready
execution: code
product_contract_source: 2026-09-05-001-feat-omaseal-first-party-keyring-plan
plan_type: feat
title: "OmaSeal: multi-arch, unbreakable, safe deployment"
date: 2026-09-06
---

# OmaSeal: multi-arch, unbreakable, safe deployment

## Summary

Make `omaseal` installable and updatable the same way first-party software is on macOS: a signed, multi-architecture binary release, an AUR package for Arch/Omarchy users, an automated GitHub release workflow, and an install/upgrade path that never overwrites a working binary without a rollback option. This plan also ships a launch tease post.

## Problem Frame

Right now `omaseal` is built by hand with `go build` and copied into `~/.local/bin`. That works for developers, but it is not viable for general users:

- No versioned releases or checksums.
- No multi-architecture binaries (x86_64 and aarch64 laptops, plus the ARM devices Omarchy will run on).
- No safe update path: a bad install can leave the user without a working keyring CLI.
- No AUR package, which is how most Omarchy/Arch users install third-party tools.
- No reproducible build / signature story, so users cannot verify the binary they downloaded.

## Scope Boundaries

### In scope

1. Version-aware `omaseal` binary (`--version`, build-time version injection).
2. Cross-compilation build script for `linux/amd64` and `linux/arm64`.
3. GitHub Actions release workflow that builds, signs tarballs with a detached GPG signature, and publishes a GitHub Release.
4. AUR packaging: `omaseal` (source) and `omaseal-bin` (prebuilt multi-arch).
5. Safe install/upgrade helpers: `install.sh` and `omaseal upgrade` with atomic swap and rollback.
6. Multi-arch Omarchy plugin bundle (`omaseal` binary per architecture + QML + manifest).
7. Marketplace submission updates (`SUBMISSION.md`, `README.md`) with install instructions per architecture.
8. Launch tease post (`docs/promo/tease.md`).

### Out of scope

- macOS or Windows ports.
- Cloud sync or encrypted backup of secrets.
- Paid-tier license enforcement.
- Notarization (Linux does not have it; GPG + checksums are the equivalent).

## Requirements

### R1. Multi-arch builds
The project must produce a `linux/amd64` and `linux/arm64` static binary from a single `make build` or `go build` invocation.

### R2. Verifiable releases
Every release publishes:
- `omaseal-linux-x86_64.tar.gz`
- `omaseal-linux-aarch64.tar.gz`
- `sha256sums.txt`
- `sha256sums.txt.asc` (detached GPG signature)
- A GitHub Release with release notes.

### R3. AUR packages
Provide `packaging/aur/PKGBUILD` (builds from source, any arch Go can target) and `packaging/aur/PKGBUILD-bin` (downloads the prebuilt tarball matching `$CARCH`). Both produce `/usr/bin/omaseal`, install the QML files to `/usr/share/omarchy/plugins/io.github.duketopceo.omaseal/`, and create a `pacman` hook that runs `omarchy-restart-shell` on plugin change.

### R4. Safe install and upgrade
`install.sh` and `omaseal upgrade` must:
- Download the latest release for the detected architecture.
- Verify the checksum (and signature if `gpg` is available).
- Stage the new binary next to the old one and only swap after `omaseal --version` succeeds.
- Keep the previous binary as `~/.local/bin/omaseal.previous` for rollback.
- Fail with a clear message and not leave a broken install.

### R5. Version command
`omaseal --version` prints the semantic version, commit, and target architecture.

### R6. Plugin bundle
The `omaseal` binary is architecture-specific; the QML files are not. The release tarball includes the binary plus `BarWidget.qml`, `Panel.qml`, and `manifest.json` in the plugin layout.

### R7. Marketplace docs
`README.md` and `SUBMISSION.md` list the AUR, manual tarball, and `omarchy plugin add` install paths.

### R8. Tease post
`docs/promo/tease.md` is a short, voice-matched launch teaser for X/LinkedIn that focuses on "macOS Keychain-grade secret management, now native on Omarchy/Linux, for x86 and ARM."

## Key Technical Decisions

### KTD1. Go cross-compilation, no CGO
`github.com/zalando/go-keyring` and `godbus/dbus` are pure Go. Cross-compiling `GOOS=linux GOARCH=amd64/arm64 CGO_ENABLED=0` from any Linux or macOS host produces a static binary. This avoids per-arch build runners and keeps the release workflow simple.

### KTD2. Detached GPG signatures, not checksum-only
Checksums verify integrity; GPG signatures verify origin. The release workflow signs `sha256sums.txt` with the maintainer key. `install.sh` verifies the signature only when `gpg` is present and the public key is in the user's keyring, otherwise it falls back to checksums and prints a notice.

### KTD3. AUR source + -bin packages
`omaseal` (source) builds on the user's machine and is the preferred Arch package. `omaseal-bin` is for users who want the prebuilt binary; it selects the correct tarball by `$CARCH`.

### KTD4. Atomic binary swap with previous-binary rollback
The install/upgrade commands stage to a temp path, run `omaseal --version` to validate, then `mv` into place. The old binary is renamed to `omaseal.previous` instead of deleted, so a broken upgrade can be undone with `mv ~/.local/bin/omaseal.previous ~/.local/bin/omaseal`.

### KTD5. Version embedded at link time
`engine/version.go` reads from `go build -ldflags "-X main.version=$VERSION -X main.commit=$COMMIT"`. The default `go build` still works and reports `dev`.

## Implementation Units

### U1. Version command and build metadata

**Goal:** Add `--version` support and build-time version injection.

**Files:**
- `engine/version.go`
- `engine/main.go`
- `Makefile`

**Approach:**
- Define `var version = "dev"`, `var commit = "unknown"`, `var target = runtime.GOOS + "/" + runtime.GOARCH`.
- Add `case "--version", "-v", "version":` to `main()` that prints `omaseal <version> (<commit>) <target>`.
- Add a top-level `Makefile` with `build`, `build-all`, `test`, `clean` targets.

**Test scenarios:**
- `go build -ldflags "-X main.version=0.3.0" ./engine && ./engine/omaseal --version` prints `0.3.0`.
- Plain `go build ./engine` prints `dev`.

**Verification:** `make test` passes and `./engine/omaseal --version` works.

---

### U2. Cross-compilation and release build scripts

**Goal:** Produce multi-arch tarballs locally and in CI.

**Files:**
- `Makefile`
- `scripts/build-release.sh`
- `scripts/package-release.sh`

**Approach:**
- `make build-all` builds `dist/omaseal-linux-x86_64/omaseal` and `dist/omaseal-linux-aarch64/omaseal`.
- Each tarball contains: `bin/omaseal`, `BarWidget.qml`, `Panel.qml`, `manifest.json`, `README.md`, `LICENSE`.
- `scripts/package-release.sh` writes `dist/sha256sums.txt` and `dist/sha256sums.txt.asc` when `GPG_KEY_ID` is set.

**Test scenarios:**
- `make build-all` succeeds on x86_64 host and produces both tarballs.
- `tar -tzf` shows the expected layout.
- `file dist/omaseal-linux-aarch64/omaseal` reports `aarch64`.

**Verification:** Run `make build-all` and inspect `dist/`.

---

### U3. GitHub Actions release workflow

**Goal:** Cut a release on tag push with multi-arch artifacts and signed checksums.

**Files:**
- `.github/workflows/release.yml`

**Approach:**
- Trigger on `push: tags: ['v*']`.
- Job `build`: checkout, set up Go, run `make build-all`, upload artifacts.
- Job `release`: download artifacts, create GitHub Release, attach tarballs, checksums, and signature.
- Use `GITHUB_TOKEN` for release; assume `GPG_KEY_ID` and `GPG_PRIVATE_KEY` secrets for signing.
- Set `GITHUB_TOKEN` permissions to `contents: write`.

**Test scenarios:**
- A `v0.3.0-test.1` tag produces a draft/valid release in a fork.
- Artifacts contain both architectures.

**Verification:** Push a pre-release tag and inspect the GitHub Release.

---

### U4. AUR packaging

**Goal:** Provide AUR source and -bin PKGBUILDs with `.SRCINFO` generation instructions.

**Files:**
- `packaging/aur/PKGBUILD`
- `packaging/aur/PKGBUILD-bin`
- `packaging/aur/.install` (optional, to restart the shell)

**Approach:**
- `omaseal` PKGBUILD:
  - `pkgbase=omaseal`, `pkgname=(omaseal)`
  - Source from `https://github.com/duketopceo/OmaSeal/archive/refs/tags/v${pkgver}.tar.gz`
  - `build()` runs `make build GOARCH=$CARCH` in `engine`.
  - `package()` installs `omaseal` to `/usr/bin`, QML/manifest to plugin dir.
- `omaseal-bin` PKGBUILD:
  - `source_x86_64` and `source_aarch64` point to release tarballs.
  - `sha256sums_x86_64` and `sha256sums_aarch64` pinned to release hashes.
  - `package()` installs the prebuilt `omaseal` and QML files.
- Include `install` script that runs `omarchy-restart-shell` after install/upgrade.

**Test scenarios:**
- `makepkg --printsrcinfo` succeeds for both.
- `namcap` has no critical warnings.

**Verification:** Build in a clean chroot or `makepkg -si` locally.

---

### U5. Safe install and upgrade helpers

**Goal:** A `curl | bash` install path and an in-place `omaseal upgrade` command that are atomic and rollback-safe.

**Files:**
- `install.sh`
- `engine/main.go` (`upgrade` subcommand)
- `engine/upgrade.go`
- `engine/upgrade_test.go`

**Approach:**
- `install.sh`:
  - Detects `uname -m` and maps `x86_64`/`aarch64` to release tarball names.
  - Downloads the latest release tarball and `sha256sums.txt`.
  - Verifies checksum (and signature if `gpg` and key present).
  - Extracts `bin/omaseal` to `~/.local/bin/omaseal.new`, runs `omaseal --version`, then swaps.
  - Renames old binary to `~/.local/bin/omaseal.previous`.
- `omaseal upgrade`:
  - Queries the GitHub API for the latest release.
  - Downloads the matching tarball, verifies, stages, validates, swaps.
  - On failure, leaves the original binary untouched.

**Test scenarios:**
- `install.sh --dry-run` shows what it would do without writing.
- `omaseal upgrade --dry-run` lists the target version and URL.
- A failed `omaseal upgrade` (corrupt tarball) does not overwrite `~/.local/bin/omaseal`.
- Rollback: `omaseal.previous` exists after a successful upgrade and can be swapped back.

**Verification:** Run `install.sh --dry-run` and a real upgrade in a temp `HOME`.

---

### U6. Marketplace documentation updates

**Goal:** `README.md` and `SUBMISSION.md` describe the new install paths.

**Files:**
- `README.md`
- `SUBMISSION.md`

**Approach:**
- Add sections for AUR (`yay -S omaseal` or `omaseal-bin`), manual tarball, and `omaseal upgrade`.
- Update install snippet to use `make build` or `install.sh`.
- Note architecture support (`x86_64`, `aarch64`).

**Test scenarios:**
- `git diff --check` passes.
- `omarchy plugin validate .` still passes.

---

### U7. Launch tease post

**Goal:** Produce a short, shareable teaser.

**Files:**
- `docs/promo/tease.md`

**Approach:**
- One-paragraph hook: "Your API keys finally get the macOS Keychain treatment on Omarchy — first-party, local, fingerprint-gated, and now installable on x86_64 and ARM with a single command."
- Include X/LinkedIn variants and hashtags.
- Mention `omaseal`, `omarchy`, `#linux`, `#aarch64`.

**Test scenarios:**
- Post fits in 280 characters (X variant) or 500 (LinkedIn variant).

**Verification:** Word count check.

## Output Structure

```
omaseal/
├── Makefile
├── install.sh
├── .github/workflows/release.yml
├── packaging/
│   └── aur/
│       ├── PKGBUILD
│       ├── PKGBUILD-bin
│       └── omaseal.install
├── engine/
│   ├── version.go
│   ├── upgrade.go
│   └── upgrade_test.go
├── dist/
│   ├── omaseal-linux-x86_64.tar.gz
│   ├── omaseal-linux-aarch64.tar.gz
│   ├── sha256sums.txt
│   └── sha256sums.txt.asc
├── docs/
│   ├── plans/2026-09-06-002-feat-omaseal-multiarch-safe-deployment-plan.md
│   └── promo/tease.md
├── README.md
└── SUBMISSION.md
```

## Risks & Dependencies

### Risks

1. **GPG signing secrets.** The release workflow needs a private key in repository secrets. If the key is not configured, signing is skipped and the release still works (checksums only). Mitigation: make `GPG_PRIVATE_KEY` optional and document how to set it.
2. **`omarchy-restart-shell` not available in AUR build environment.** The install script should not assume `omarchy` commands. Mitigation: the `.install` script only restarts the shell if `omarchy-restart-shell` is on `PATH`.
3. **AUR maintenance burden.** Every release needs updated `PKGBUILD-bin` hashes and `.SRCINFO`. Mitigation: include the `makepkg --printsrcinfo` commands in `docs/RELEASING.md` and in the release workflow comments.
4. **Manual `~/.local/bin` not on PATH.** `install.sh` warns the user and offers to append it.
5. **Multi-arch `go-keyring` behavior.** Pure Go cross-compilation should produce identical behavior, but `godbus/dbus` only runs on Linux with D-Bus. Mitigation: `install.sh` refuses non-Linux; runtime errors are surfaced to the user.

### Dependencies

- Go 1.27+ toolchain.
- `gpg` and a maintainer key for signed releases.
- AUR access for `duketopceo` or a Trusted User to push packages.
- `omarchy plugin validate` for marketplace verification.

## Open Questions

1. Should the release workflow auto-push AUR packages, or is manual AUR push acceptable for v1?
2. Should `install.sh` default to `~/.local/bin` or `/usr/local/bin` when run with `sudo`?
3. Should the AUR `-bin` package be submitted to the AUR before the source package, or vice versa?
