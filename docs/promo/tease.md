# OmaSeal launch tease

## X / 280-character

Your API keys finally get the macOS Keychain treatment on Omarchy. First-party, local, fingerprint-gated, and installable on x86_64 and ARM with one command. Meet OmaSeal — the Omarchy-native keyring.

`#Omarchy` `#OmaSeal` `#Linux` `#x86_64` `#aarch64` `#Keychain` `#OpenSource`

## LinkedIn

macOS users have had Keychain for decades. On Linux, API keys usually end up in plain text dotfiles or shared env files. OmaSeal changes that for Omarchy.

It is a first-party, local-only keyring built on the existing `gnome-keyring` / Secret Service stack, with a Quickshell panel, an `omaseal` CLI, `fprintd` fingerprint gating, and automatic fallback to 1Password or Bitwarden for imports.

OmaSeal is now shipping with multi-arch x86_64 and aarch64 releases, AUR packages, signed tarballs, and an atomic install/upgrade path that keeps the previous binary for rollback.

Install with `yay -S omaseal` or `yay -S omaseal-bin`.
