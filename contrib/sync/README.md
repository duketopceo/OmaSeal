# OmaSeal replica sync

One-way copy of the native store from the laptop (the only writer) to one
or more read-only replica machines over Tailscale SSH. Pair this with the
multi-recipient and replica mode work in pull request #72, and with the
requirements and plan in pull request #74. Three-device sync (primary + two
replicas) is covered below and in
`docs/plans/2026-11-14-0908-feat-omaseal-three-device-sync-plan.md`.

Sync copies only `store.json`. It never copies `identity.age`,
`identity.pub`, `store.lock`, `recipients.json`, or session files.
Copying a laptop identity onto a replica would collapse the keys into one.

## How the path is resolved

The engine roots the native store at `$XDG_STATE_HOME/omaseal/native`
(`newNativeStore` in `engine/nativestore.go`, via `svcXDGDir` in
`engine/svcfiles.go`). If `XDG_STATE_HOME` is unset, that is
`$HOME/.local/state/omaseal/native`. `omaseal-sync` and
`omaseal-replica-check` use the same rule. Override the local file with
`OMASEAL_STORE` if needed.

## One-time key exchange

Each replica must be able to decrypt a copied `store.json`. That requires
the laptop to encrypt every item to the laptop key **and** every replica
public key before the first sync.

1. On each replica, use a native backend in replica mode
   (`"replica": true` in the OmaSeal config, or `OMASEAL_REPLICA=1`).
   First TTY use (`omaseal get` or `omaseal recipients local`) creates
   this machine's `identity.age` / `identity.pub`.
2. On each replica, print the public key:

   ```sh
   omaseal recipients local
   ```

3. On the laptop, add each key (presence-gated; re-encrypts every item;
   `allow_ungated` does not bypass it). Use a distinct `--name` per device:

   ```sh
   omaseal recipients add age1example-b... --name replica-b
   omaseal recipients add age1example-c... --name replica-c
   ```

4. Confirm `omaseal recipients list` on the laptop shows the laptop key
   plus every replica name. Then enable the units below.

### Three devices (primary + two replicas)

Device count is primary + N sync targets. For two replica hosts, every
ciphertext must have at least three age recipient stanzas before
`omaseal-sync` will copy.

```sh
# On primary after both recipients are added:
omaseal recipients list

# Push to both replicas in one run:
export OMASEAL_REPLICA_HOSTS=replica-b,replica-c
omaseal-sync --dry-run
omaseal-sync
```

`OMASEAL_REPLICA_HOST` (singular) still works for a single replica.
Prefer `OMASEAL_REPLICA_HOSTS` when syncing more than one.

`omaseal-sync` refuses to copy a store whose items do not look
multi-recipient for the host count. It base64-decodes each `ct` and
counts age recipient stanzas (`-> …` lines in the header). Minimum
stanzas = `1 + number of replica hosts`. It does not decrypt. An empty
store has nothing to inspect; the script warns and continues. If any
item has too few stanzas, the copy is aborted for that run (already
updated hosts are left as-is; failed hosts keep their previous
`store.json`).

## Laptop setup (push)

1. Install the script somewhere on `PATH`. The systemd unit expects
   `%h/.local/bin/omaseal-sync`:

   ```sh
   mkdir -p ~/.local/bin ~/.config/systemd/user
   install -m 755 contrib/sync/omaseal-sync ~/.local/bin/omaseal-sync
   cp contrib/sync/omaseal-sync.service \
      contrib/sync/omaseal-sync.path \
      contrib/sync/omaseal-sync.timer \
      ~/.config/systemd/user/
   ```

2. SSH to each replica must already work in batch mode over Tailscale
   (no password prompt). Set `OMASEAL_REPLICA_HOSTS` (or singular
   `OMASEAL_REPLICA_HOST`) — there is no default — in a systemd user
   drop-in, or in `~/.config/environment.d/`:

   ```sh
   export OMASEAL_REPLICA_HOSTS=replica-b,replica-c
   for h in ${OMASEAL_REPLICA_HOSTS//,/ }; do
     ssh -o BatchMode=yes "$h" true
   done
   ```

   The default remote path is the engine path on each host; override
   with `OMASEAL_REPLICA_PATH` only if every replica's state dir shares
   that path. The path must still be a file named `store.json`.

3. If this machine's `XDG_STATE_HOME` is not `$HOME/.local/state`,
   edit the paths in `omaseal-sync.path` before enabling.

4. Enable the path unit and the hourly timer:

   ```sh
   systemctl --user daemon-reload
   systemctl --user enable --now omaseal-sync.path
   systemctl --user enable --now omaseal-sync.timer
   ```

5. A first dry run, then a real push:

   ```sh
   omaseal-sync --dry-run
   omaseal-sync
   ```

## Replica Mac setup (status check)

The launchd agent does not copy the store. It only parses the replica
`store.json` and writes `$XDG_STATE_HOME/omaseal/replica-status.json`
(override with `OMASEAL_REPLICA_STATUS`) for the menu bar app.

1. Install the checker and the agent:

   ```sh
   sudo install -m 755 contrib/sync/omaseal-replica-check /usr/local/bin/omaseal-replica-check
   mkdir -p ~/Library/LaunchAgents
   cp contrib/sync/com.duketopceo.omaseal-replica.plist \
      ~/Library/LaunchAgents/com.duketopceo.omaseal-replica.plist
   launchctl load ~/Library/LaunchAgents/com.duketopceo.omaseal-replica.plist
   ```

2. The agent runs at load and every 15 minutes. A successful status
   file includes `ok`, `last_sync` (mtime of `store.json`, UTC),
   `checked_at`, `checksum`, and `item_count`. It never writes
   `store.json` or identity files.

3. On the replica Mac, OmaSeal itself must stay in replica mode so a `Set` or
   migrate is refused.

## Rollback

On the laptop:

```sh
systemctl --user disable --now omaseal-sync.path omaseal-sync.timer
# optional: remove the units from ~/.config/systemd/user/ and the script
# from ~/.local/bin
```

On the replica Mac:

```sh
launchctl unload ~/Library/LaunchAgents/com.duketopceo.omaseal-replica.plist
rm -f ~/Library/LaunchAgents/com.duketopceo.omaseal-replica.plist
# optional: remove /usr/local/bin/omaseal-replica-check
```

Stopping sync does not delete the replica's store or its identity. To
drop the replica as a recipient on the laptop (presence-gated, re-encrypts
every item):

```sh
omaseal recipients rm replica-b
omaseal recipients rm replica-c
# or a single-host name from an older setup:
# omaseal recipients rm replica
```

To discard the replica copy only, delete `store.json` on the replica Mac.
Leave `identity.age` and `identity.pub` alone.

## Environment

| Variable | Role |
| --- | --- |
| `OMASEAL_STORE` | Local `store.json` path |
| `OMASEAL_REPLICA_HOSTS` | Comma/whitespace-separated SSH hosts (preferred for ≥2 replicas) |
| `OMASEAL_REPLICA_HOST` | Single SSH host when `HOSTS` is unset |
| `OMASEAL_REPLICA_PATH` | Remote `store.json` path (same on every host) |
| `OMASEAL_REPLICA_STATUS` | Status sidecar path for the checker |
| `OMASEAL_SSH` / `OMASEAL_SCP` | Override `ssh` / `scp` (used by tests) |

## Tests

```sh
bash contrib/sync/omaseal-sync_test.sh
```

The tests use two temp directories and a fake `ssh`/`scp` shim. They
do not touch a live store and must not be pointed at real secrets.
