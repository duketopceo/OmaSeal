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
export OMASEAL_REPLICA_HOST="mac-mini"
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
printf 'MINI-IDENTITY\n' > "$DST/identity.age"
printf 'age1mini\n' > "$DST/identity.pub"
mini_id_hash="$(hash_of "$DST/identity.age")"

export OMASEAL_STORE="$SRC/store.json"

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
  bash -c 'grep -q MINI-IDENTITY "'"$DST"'/identity.age"'

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

echo
echo "passed: $passes  failed: $failures"
if [ "$failures" -ne 0 ]; then
  exit 1
fi
