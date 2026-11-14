#!/usr/bin/env bash
# Tests for omaseal-sync and omaseal-replica-check.
# Uses two temp dirs and a fake ssh/scp shim. No live store, no secrets.
set -euo pipefail

ROOT="$(CDPATH='' cd -- "$(dirname -- "$0")/../.." && pwd)"
SYNC="$ROOT/contrib/sync/omaseal-sync"
CHECK="$ROOT/contrib/sync/omaseal-replica-check"

if [ ! -x "$SYNC" ] || [ ! -x "$CHECK" ]; then
  echo "test: missing executable scripts under contrib/sync" >&2
  exit 1
fi

failures=0
passes=0

assert() {
  local name="$1"
  shift
  if "$@"; then
    passes=$((passes + 1))
    echo "ok - $name"
  else
    failures=$((failures + 1))
    echo "not ok - $name" >&2
  fi
}

assert_eq() {
  local name="$1" got="$2" want="$3"
  if [ "$got" = "$want" ]; then
    passes=$((passes + 1))
    echo "ok - $name"
  else
    failures=$((failures + 1))
    echo "not ok - $name: got '$got' want '$want'" >&2
  fi
}


no_temp_leftover() {
  local dir="$1"
  local found
  found="$(find "$dir" -maxdepth 1 -name "store.json.omaseal-sync.*.tmp" 2>/dev/null | wc -l | tr -d " ")"
  [ "$found" = "0" ]
}

output_has() {
  printf '%s' "$1" | grep -q "$2"
}

tmpdir="$(mktemp -d "${TMPDIR:-/tmp}/omaseal-sync-test.XXXXXX")"
trap 'rm -rf "$tmpdir"' EXIT

SRC="$tmpdir/src"
DST="$tmpdir/dst"
BIN="$tmpdir/bin"
mkdir -p "$SRC" "$DST" "$BIN"

# Fake ssh/scp: treat the host as this machine and run against real paths
# under DST (the "remote"). Optional SHA256_LIE makes checksum verification
# fail without changing bytes.
cat > "$BIN/ssh" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
# Drop ssh options and an optional --, then host, then the remote command.
while [ "$#" -gt 0 ]; do
  case "$1" in
    --) shift; break ;;
    -o|-l|-p|-F|-i|-w) shift 2 ;;
    -*) shift ;;
    *) break ;;
  esac
done
# host
if [ "$#" -lt 1 ]; then
  echo "fake-ssh: missing host" >&2
  exit 2
fi
shift
if [ "$#" -eq 0 ]; then
  echo "fake-ssh: missing command" >&2
  exit 2
fi
# Remaining is the remote command. Run it locally.
if [ -n "${SHA256_LIE:-}" ]; then
  # Wrap sha256sum/shasum so a verify step sees the wrong digest.
  liebin="$(dirname -- "$0")/lie"
  mkdir -p "$liebin"
  if [ ! -x "$liebin/sha256sum" ]; then
    printf '%s\n' '#!/usr/bin/env bash' "echo ${SHA256_LIE}  \"\$2\"" > "$liebin/sha256sum"
    printf '%s\n' '#!/usr/bin/env bash' "echo ${SHA256_LIE}  \"\$3\"" > "$liebin/shasum"
    chmod +x "$liebin/sha256sum" "$liebin/shasum"
  fi
  PATH="$liebin:$PATH" eval "$@"
else
  eval "$@"
fi
SH
cat > "$BIN/scp" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
# Drop scp options and an optional --, then src and host:dest.
while [ "$#" -gt 0 ]; do
  case "$1" in
    --) shift; break ;;
    -o|-P|-F|-i|-c|-l) shift 2 ;;
    -*) shift ;;
    *) break ;;
  esac
done
if [ "$#" -ne 2 ]; then
  echo "fake-scp: expected src dest, got: $*" >&2
  exit 2
fi
src=$1
dest=$2
# strip host:
dest="${dest#*:}"
cp -- "$src" "$dest"
SH
chmod +x "$BIN/ssh" "$BIN/scp"

export PATH="$BIN:$PATH"
export OMASEAL_SSH="$BIN/ssh"
export OMASEAL_SCP="$BIN/scp"
export OMASEAL_REPLICA_HOST="replica-host"
export OMASEAL_REPLICA_PATH="$DST/store.json"

# Build a store.json whose ct values are base64(raw age v1) with N stanzas.
make_store() {
  local out="$1" n="${2:-2}" items="${3:-1}"
  python3 - "$out" "$n" "$items" <<'PY'
import base64, json, sys
from pathlib import Path

out, n, items = Path(sys.argv[1]), int(sys.argv[2]), int(sys.argv[3])
n = max(n, 0)

def ct(stanzas: int) -> str:
    lines = ["age-encryption.org/v1"]
    for i in range(stanzas):
        lines.append("-> X25519 " + ("A" * 43))
        lines.append("BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB")
    lines.append("--- DUMMYMAC")
    raw = "\n".join(lines).encode("ascii") + b"\n" + b"\x00fake"
    return base64.b64encode(raw).decode("ascii")

doc = {"items": {}}
for i in range(items):
    doc["items"][f"svc{i}/acct"] = {
        "ct": ct(n),
        "created": 1,
        "updated": 1,
    }
out.write_text(json.dumps(doc, indent=2) + "\n", encoding="utf-8")
import os
os.chmod(out, 0o600)
PY
}

hash_of() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum -- "$1" | awk '{print $1}'
  else
    shasum -a 256 -- "$1" | awk '{print $1}'
  fi
}

# --- fixtures ---
make_store "$SRC/store.json" 2 2
# Identity and lock files must never be copied.
printf 'FAKE-IDENTITY\n' > "$SRC/identity.age"
printf 'age1fake\n' > "$SRC/identity.pub"
printf 'lock\n' > "$SRC/store.lock"
printf '{}\n' > "$SRC/recipients.json"
# A previous replica store, to prove a failed transfer leaves it in place.
printf '{"items":{"old/one":{"ct":"YQ==","created":0,"updated":0}}}\n' > "$DST/store.json"
# Identity files already on the replica must also stay untouched.
printf 'REPLICA-IDENTITY\n' > "$DST/identity.age"
printf 'age1mini\n' > "$DST/identity.pub"
mini_id_hash="$(hash_of "$DST/identity.age")"

export OMASEAL_STORE="$SRC/store.json"

# 0. A replica host is required — there is no default host
set +e
host_out="$(
  env -u OMASEAL_REPLICA_HOST -u OMASEAL_REPLICA_HOSTS "$SYNC" --dry-run 2>&1
)"
host_rc=$?
set -e
assert_eq "unset replica host exits 1" "$host_rc" "1"
assert "unset replica host names OMASEAL_REPLICA_HOST" grep -q "OMASEAL_REPLICA_HOST" <<<"$host_out"

# 1. dry-run does not change the destination or copy identities
before_dst="$(hash_of "$DST/store.json")"
set +e
dry_out="$("$SYNC" --dry-run 2>&1)"
dry_rc=$?
set -e
assert_eq "dry-run exits 0" "$dry_rc" "0"
assert_eq "dry-run leaves dest store unchanged" "$(hash_of "$DST/store.json")" "$before_dst"
assert "dry-run mentions dry-run" grep -q "dry-run" <<<"$dry_out"
assert "dry-run did not create a temp leftover" \
  no_temp_leftover "$DST"
assert "dry-run did not copy identity.age" \
  bash -c 'grep -q REPLICA-IDENTITY "'"$DST"'/identity.age"'

# 2. atomic swap: dest becomes the source, no temp leftover, identities stay
set +e
"$SYNC" >/dev/null
sync_rc=$?
set -e
assert_eq "sync exits 0" "$sync_rc" "0"
assert_eq "atomic swap: dest matches source hash" "$(hash_of "$DST/store.json")" "$(hash_of "$SRC/store.json")"
assert "atomic swap: dest is valid JSON with two items" \
  python3 - "$DST/store.json" <<'PY'
import json, sys
from pathlib import Path
doc = json.loads(Path(sys.argv[1]).read_text())
assert "svc0/acct" in doc["items"] and "svc1/acct" in doc["items"]
PY
assert "atomic swap leaves no temp file" \
  no_temp_leftover "$DST"
assert_eq "identity.age on dest was not overwritten" "$(hash_of "$DST/identity.age")" "$mini_id_hash"
assert "identity.pub on dest was not overwritten" \
  bash -c 'grep -q age1mini "'"$DST"'/identity.pub"'
assert "recipients.json was not copied to dest" \
  bash -c '[ ! -f "'"$DST"'/recipients.json" ]'
assert "store.lock was not copied to dest" \
  bash -c '[ ! -f "'"$DST"'/store.lock" ]'
# source identities still present (not consumed)
assert "source identity.age still in place" \
  bash -c 'grep -q FAKE-IDENTITY "'"$SRC"'/identity.age"'

# 3. checksum mismatch: dest left unchanged, non-zero exit, no temp leftover
printf '{"items":{"keep/me":{"ct":"YQ==","created":0,"updated":0}}}\n' > "$DST/store.json"
keep_hash="$(hash_of "$DST/store.json")"
set +e
mismatch_out="$(SHA256_LIE=0000000000000000000000000000000000000000000000000000000000000000 "$SYNC" 2>&1)"
mismatch_rc=$?
set -e
assert "checksum mismatch exits non-zero" bash -c "[ $mismatch_rc -ne 0 ]"
assert "checksum mismatch message mentions checksum" \
  output_has "$mismatch_out" "checksum mismatch"
assert_eq "checksum mismatch leaves dest unchanged" "$(hash_of "$DST/store.json")" "$keep_hash"
assert "checksum mismatch leaves no temp file" \
  no_temp_leftover "$DST"
assert_eq "checksum mismatch did not touch dest identity.age" "$(hash_of "$DST/identity.age")" "$mini_id_hash"

# 4. single-recipient store is refused
make_store "$SRC/store.json" 1 1
printf '{"items":{"keep/me":{"ct":"YQ==","created":0,"updated":0}}}\n' > "$DST/store.json"
keep_hash="$(hash_of "$DST/store.json")"
set +e
single_out="$("$SYNC" 2>&1)"
single_rc=$?
set -e
assert "single-recipient store exits non-zero" bash -c "[ $single_rc -ne 0 ]"
assert "single-recipient message mentions multi-recipient" \
  output_has "$single_out" "multi-recipient"
assert_eq "single-recipient refuse leaves dest unchanged" "$(hash_of "$DST/store.json")" "$keep_hash"

# 5. refusing a non-store.json local path
set +e
bad_out="$(OMASEAL_STORE="$SRC/identity.age" "$SYNC" 2>&1)"
bad_rc=$?
set -e
assert "refuse identity.age as OMASEAL_STORE" bash -c "[ $bad_rc -ne 0 ]"
assert "refuse identity.age mentions store.json" \
  output_has "$bad_out" "store.json"

# 6. replica-check writes status, never writes the store
make_store "$DST/store.json" 2 2
dest_hash_before="$(hash_of "$DST/store.json")"
id_before="$(hash_of "$DST/identity.age")"
STATUS="$tmpdir/status/replica-status.json"
set +e
OMASEAL_STORE="$DST/store.json" OMASEAL_REPLICA_STATUS="$STATUS" "$CHECK" >/dev/null
check_rc=$?
set -e
assert_eq "replica-check exits 0" "$check_rc" "0"
assert "replica-check wrote status file" test -f "$STATUS"
assert "replica-check status ok=true and has last_sync" \
  python3 - "$STATUS" <<'PY'
import json, sys
from pathlib import Path
doc = json.loads(Path(sys.argv[1]).read_text())
assert doc["ok"] is True
assert doc["error"] is None
assert doc["last_sync"]
assert doc["checksum"]
assert doc["item_count"] == 2
PY
assert_eq "replica-check did not write the store" "$(hash_of "$DST/store.json")" "$dest_hash_before"
assert_eq "replica-check did not write identity.age" "$(hash_of "$DST/identity.age")" "$id_before"

# 7. replica-check fails closed on a broken store, still does not write it
printf 'not-json\n' > "$DST/store.json"
broken_hash="$(hash_of "$DST/store.json")"
set +e
OMASEAL_STORE="$DST/store.json" OMASEAL_REPLICA_STATUS="$STATUS" "$CHECK" >/dev/null 2>&1
badcheck_rc=$?
set -e
assert "replica-check on broken store exits non-zero" bash -c "[ $badcheck_rc -ne 0 ]"
assert "replica-check records ok=false" \
  python3 - "$STATUS" <<'PY'
import json, sys
from pathlib import Path
doc = json.loads(Path(sys.argv[1]).read_text())
assert doc["ok"] is False
assert doc["error"]
PY
assert_eq "replica-check did not repair or replace a broken store" "$(hash_of "$DST/store.json")" "$broken_hash"

# --- three-device path: primary + two replicas ---
DST_B="$tmpdir/dst-b"
DST_C="$tmpdir/dst-c"
mkdir -p "$DST_B" "$DST_C"
printf 'REPLICA-B-IDENTITY\n' > "$DST_B/identity.age"
printf 'age1replica-b\n' > "$DST_B/identity.pub"
printf 'REPLICA-C-IDENTITY\n' > "$DST_C/identity.age"
printf 'age1replica-c\n' > "$DST_C/identity.pub"
printf '{"items":{"old/b":{"ct":"YQ==","created":0,"updated":0}}}\n' > "$DST_B/store.json"
printf '{"items":{"old/c":{"ct":"YQ==","created":0,"updated":0}}}\n' > "$DST_C/store.json"
id_b_hash="$(hash_of "$DST_B/identity.age")"
id_c_hash="$(hash_of "$DST_C/identity.age")"

# Host-routed ssh/scp: formula resolve and file ops land under the mapped root.
cat > "$BIN/ssh" <<SH
#!/usr/bin/env bash
set -euo pipefail
while [ "\$#" -gt 0 ]; do
  case "\$1" in
    --) shift; break ;;
    -o|-l|-p|-F|-i|-w) shift 2 ;;
    -*) shift ;;
    *) break ;;
  esac
done
if [ "\$#" -lt 1 ]; then
  echo "fake-ssh: missing host" >&2
  exit 2
fi
host="\$1"
shift
case "\$host" in
  replica-b) root="$DST_B" ;;
  replica-c) root="$DST_C" ;;
  replica-host) root="$DST" ;;
  *) echo "fake-ssh: unknown host \$host" >&2; exit 2 ;;
esac
if [ "\$#" -eq 0 ]; then
  echo "fake-ssh: missing command" >&2
  exit 2
fi
# Path resolution only: the sync script asks for the remote formula via printf.
# Do not match mkdir/mv paths that merely contain store.json.
cmd="\$*"
case "\$cmd" in
  printf*'omaseal/native/store.json'*)
    printf '%s\n' "\$root/store.json"
    exit 0
    ;;
esac
if [ -n "\${SHA256_LIE:-}" ]; then
  liebin="\$(dirname -- "\$0")/lie"
  mkdir -p "\$liebin"
  if [ ! -x "\$liebin/sha256sum" ]; then
    printf '%s\n' '#!/usr/bin/env bash' "echo \${SHA256_LIE}  \\"\\\$2\\"" > "\$liebin/sha256sum"
    printf '%s\n' '#!/usr/bin/env bash' "echo \${SHA256_LIE}  \\"\\\$3\\"" > "\$liebin/shasum"
    chmod +x "\$liebin/sha256sum" "\$liebin/shasum"
  fi
  PATH="\$liebin:\$PATH" eval "\$@"
else
  eval "\$@"
fi
SH
cat > "$BIN/scp" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
while [ "$#" -gt 0 ]; do
  case "$1" in
    --) shift; break ;;
    -o|-P|-F|-i|-c|-l) shift 2 ;;
    -*) shift ;;
    *) break ;;
  esac
done
if [ "$#" -ne 2 ]; then
  echo "fake-scp: expected src dest, got: $*" >&2
  exit 2
fi
src=$1
dest=$2
dest="${dest#*:}"
cp -- "$src" "$dest"
SH
chmod +x "$BIN/ssh" "$BIN/scp"

# 8. three-stanza store → two hosts (primary + 2 replicas)
make_store "$SRC/store.json" 3 2
export OMASEAL_STORE="$SRC/store.json"
unset OMASEAL_REPLICA_PATH || true
export OMASEAL_REPLICA_HOSTS="replica-b, replica-c"
unset OMASEAL_REPLICA_HOST || true

set +e
three_dry="$("$SYNC" --dry-run 2>&1)"
three_dry_rc=$?
set -e
assert_eq "three-device dry-run exits 0" "$three_dry_rc" "0"
assert "three-device dry-run mentions 2 hosts" grep -q "hosts  2" <<<"$three_dry"
assert "three-device dry-run mentions min stanzas 3" grep -q "min stanzas 3" <<<"$three_dry"
before_b="$(hash_of "$DST_B/store.json")"
before_c="$(hash_of "$DST_C/store.json")"
assert_eq "three-device dry-run leaves dst-b unchanged" "$(hash_of "$DST_B/store.json")" "$before_b"
assert_eq "three-device dry-run leaves dst-c unchanged" "$(hash_of "$DST_C/store.json")" "$before_c"

set +e
"$SYNC" >/dev/null
three_rc=$?
set -e
assert_eq "three-device sync exits 0" "$three_rc" "0"
assert_eq "three-device: dst-b matches source" "$(hash_of "$DST_B/store.json")" "$(hash_of "$SRC/store.json")"
assert_eq "three-device: dst-c matches source" "$(hash_of "$DST_C/store.json")" "$(hash_of "$SRC/store.json")"
assert_eq "three-device: dst-b identity untouched" "$(hash_of "$DST_B/identity.age")" "$id_b_hash"
assert_eq "three-device: dst-c identity untouched" "$(hash_of "$DST_C/identity.age")" "$id_c_hash"
assert "three-device: recipients.json not on dst-b" \
  bash -c '[ ! -f "'"$DST_B"'/recipients.json" ]'
assert "three-device: recipients.json not on dst-c" \
  bash -c '[ ! -f "'"$DST_C"'/recipients.json" ]'

# 9. two-stanza store refused when syncing to two hosts (need ≥3)
make_store "$SRC/store.json" 2 1
keep_b="$(hash_of "$DST_B/store.json")"
keep_c="$(hash_of "$DST_C/store.json")"
set +e
short_out="$("$SYNC" 2>&1)"
short_rc=$?
set -e
assert "two-stanza vs two hosts exits non-zero" bash -c "[ $short_rc -ne 0 ]"
assert "two-stanza vs two hosts names stanza need" \
  output_has "$short_out" "need ≥3"
assert_eq "two-stanza refuse leaves dst-b" "$(hash_of "$DST_B/store.json")" "$keep_b"
assert_eq "two-stanza refuse leaves dst-c" "$(hash_of "$DST_C/store.json")" "$keep_c"

# 10. OMASEAL_REPLICA_HOSTS back-compat: single host via HOSTS still works
make_store "$SRC/store.json" 2 1
export OMASEAL_REPLICA_HOSTS="replica-host"
export OMASEAL_REPLICA_PATH="$DST/store.json"
printf '{"items":{"keep/me":{"ct":"YQ==","created":0,"updated":0}}}\n' > "$DST/store.json"
set +e
"$SYNC" >/dev/null
hostsonly_rc=$?
set -e
assert_eq "HOSTS with one host exits 0" "$hostsonly_rc" "0"
assert_eq "HOSTS with one host updates dest" "$(hash_of "$DST/store.json")" "$(hash_of "$SRC/store.json")"

echo
echo "passed: $passes  failed: $failures"
if [ "$failures" -ne 0 ]; then
  exit 1
fi

