# OmaSeal menu bar app

A SwiftUI `MenuBarExtra` client for macOS 14+. It talks to OmaSeal only by
shelling out to the `omaseal` CLI (`list --json` and `get <service>
<account>`). It never links the engine.

Reveal and Copy are gated by LocalAuthentication (Touch ID, falling back
to the device password). Copied values are cleared from the pasteboard
after 30 seconds if it still holds that value. Secret values are never
logged, never written to disk, and are dropped from memory when the
popover closes.

A status line reads the sidecar written by `contrib/sync/omaseal-replica-check`
(see pull request #75): `$XDG_STATE_HOME/omaseal/replica-status.json`,
or `$HOME/.local/state/omaseal/replica-status.json` when `XDG_STATE_HOME`
is unset. If that file is missing, the line is `not a replica`.

A runnable macOS `omaseal` binary depends on pull request #70.

## Build

From this directory:

```sh
swift build --product OmaSealBar
# or
make build
```

## Test

```sh
swift test
# or
make test
```

The tests use a fake CLI, a fake authenticator, and an injectable
pasteboard and clock. They do not call a live `omaseal` or Touch ID.

## Run

```sh
swift run OmaSealBar
```

The executable looks up `omaseal` on `PATH`, including `/opt/homebrew/bin`
and `/usr/local/bin`. Set `OmaSealBar.binaryPath` in user defaults, or
put `omaseal` on `PATH`, if it lives somewhere else.

`swift run` will show a Dock icon. For a menu-bar-only agent, bundle
the app (below) so `LSUIElement` is set.

## Bundle

```sh
bash Scripts/bundle-app.sh
# or
make app
```

This produces `dist/OmaSealBar.app` with `LSUIElement` set so the app
does not appear in the Dock. Open it with:

```sh
open dist/OmaSealBar.app
```
