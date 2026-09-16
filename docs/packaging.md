# AUR packaging: `omaseal-bin`

`packaging/aur/` carries two PKGBUILDs plus their generated `.SRCINFO`:

| In-repo file | Published as | Purpose |
|---|---|---|
| `PKGBUILD-bin` | `PKGBUILD` in the `omaseal-bin` AUR repo | Repackages CI-built release tarballs |
| `omaseal-bin.SRCINFO` | `.SRCINFO` | AUR metadata, generated — never hand-edit |
| `omaseal-bin.install` | `omaseal-bin.install` | Post-install hook (referenced by `install=`) |
| `PKGBUILD` / `.SRCINFO` / `omaseal.install` | — | Source-build package, maintained but **not yet published** |

Dependencies: the binary is pure-Go D-Bus (`ldd` shows libc/libresolv
only), so the sole hard dep is the `org.freedesktop.secrets` virtual
provide (gnome-keyring satisfies it on Omarchy). `fprintd`,
`wl-clipboard`, and `pinentry` are optdepends. namcap will report
`org.freedesktop.secrets` as "not needed" — a known false positive for
D-Bus runtime deps; do not drop it to silence the linter.

## One-time setup (maintainer)

```sh
# AUR account + SSH key already created at aur.archlinux.org
git clone ssh://aur@aur.archlinux.org/omaseal-bin.git
cd omaseal-bin
cp /path/to/OmaSeal/packaging/aur/PKGBUILD-bin PKGBUILD
cp /path/to/OmaSeal/packaging/aur/omaseal-bin.SRCINFO .SRCINFO
cp /path/to/OmaSeal/packaging/aur/omaseal-bin.install omaseal-bin.install
git add PKGBUILD .SRCINFO omaseal-bin.install
git commit -m "omaseal-bin 0.2.2-1"
git push
```

## Per-release flow

1. Tag and push `vX.Y.Z` — `release.yml` builds both arches, publishes
   the release, then the `bump-packaging` job runs `bump.sh` and opens a
   pin-bump PR automatically.
2. Review and merge that PR — check the pinned hashes against the
   release assets.
3. Sync the AUR clone and push:

   ```sh
   cd omaseal-bin
   cp /path/to/OmaSeal/packaging/aur/PKGBUILD-bin PKGBUILD
   cp /path/to/OmaSeal/packaging/aur/omaseal-bin.SRCINFO .SRCINFO
   cp /path/to/OmaSeal/packaging/aur/omaseal-bin.install omaseal-bin.install
   git commit -am "omaseal-bin X.Y.Z-1" && git push
   ```

Hard rule: never publish `SKIP` sums — both PKGBUILDs carry that comment
for a reason. `bump.sh` never writes `SKIP`; it fails instead.

Manual equivalent of the CI job, on any Arch host:

```sh
make release-bump TAG=vX.Y.Z            # or: packaging/aur/bump.sh vX.Y.Z
# unsigned releases require the explicit flag:
packaging/aur/bump.sh vX.Y.Z --allow-unsigned
```

`bump.sh` verifies `sha256sums.txt.asc` against
`OMASEAL_SIGNING_FINGERPRINT` when the release is signed. The
`--allow-unsigned` flag exists so unsigned releases are a deliberate
choice, not a silent default.

## Local verification before pushing to AUR

```sh
tmp=$(mktemp -d)
cp packaging/aur/PKGBUILD-bin "$tmp/PKGBUILD"
cp packaging/aur/omaseal-bin.install "$tmp/"
cd "$tmp" && makepkg -f          # builds omaseal-bin-<ver>-<rel>-<arch>.pkg.tar.zst
pacman -Qip omaseal-bin-*.pkg.tar.zst   # dep list, files
namcap omaseal-bin-*.pkg.tar.zst        # when installed; expect the D-Bus false positive
```

`makepkg -si` / `pacman -U` install locally but need sudo — run them
yourself rather than through an agent.

## Honest notes for users switching install methods

- The package installs `/usr/bin/omaseal`, which can shadow an
  `install.sh`-installed `~/.local/bin/omaseal` — PATH order decides
  which runs.
- The packaged plugin `io.github.duketopceo.omaseal` installs to
  Omarchy's first-party dir (`/usr/share/omarchy/shell/plugins`), which
  the shell prefers over a same-ID copy in `~/.config/omarchy/plugins`.
  Installing the package silently switches which copy runs; removing it
  resurrects the stale user copy.
- Removal of a packaged install is `sudo pacman -R omaseal-bin`, not the
  `rm ~/.local/bin/omaseal` path that applies to `install.sh` installs.
- It coexists with upstream Omarchy's `secrets` panel under a different
  plugin ID.
