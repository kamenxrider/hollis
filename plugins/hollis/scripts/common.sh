#!/bin/bash
# Shared by the installer and launcher. macOS bash 3.2 and built-in utilities only.
set -euo pipefail
umask 077
HOLLIS_CALLER_PATH=${HOLLIS_CALLER_PATH:-$PATH}
export HOLLIS_CALLER_PATH
export PATH=/usr/bin:/bin:/usr/sbin:/sbin
PLUGIN_ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)
PLUGIN_HOME=${HOLLIS_PLUGIN_HOME:-"$HOME/Library/Application Support/hollis/plugin"}
# Canonical macOS aliases; arbitrary symlinked installation paths are rejected.
case "$PLUGIN_HOME" in /tmp/*) PLUGIN_HOME="/private$PLUGIN_HOME";; /var/*) PLUGIN_HOME="/private$PLUGIN_HOME";; esac
RUNTIME_LOCK="$PLUGIN_ROOT/runtime.lock.json"
LOCK_DIR=
WORK_DIR=

fail() { printf 'Hollis: %s\n' "$*" >&2; exit 10; }
# A receipt-pinned native protocol is the authority: a positive integer no
# greater than 99. The record emitter interpolates it unquoted and the Go
# decoder requires an integer, so a non-integer or out-of-range pin is an
# invalid receipt, never a protocol era to guess at.
valid_protocol() {
  case "$1" in ''|*[!0-9]*) return 1;; esac
  [[ ${#1} -le 2 ]] && (( 10#$1 >= 1 && 10#$1 <= 99 ))
}
json_string() {
  local s=$1
  s=${s//\\/\\\\}; s=${s//\"/\\\"}; s=${s//$'\n'/\\n}; s=${s//$'\r'/\\r}; s=${s//$'\t'/\\t}
  printf '"%s"' "$s"
}
report() {
  printf '{"status":'; json_string "$1"
  printf ',"message":'; json_string "$2"
  printf ',"runtime_home":'; json_string "$PLUGIN_HOME"
  printf '}\n'
}
rollback_state() {
  ROLLBACK_STATUS=none
  ROLLBACK_VERSION=
  ROLLBACK_MESSAGE='No rollback candidate is recorded.'
  local pointer="$PLUGIN_HOME/previous" candidate dir receipt binary receipt_version expected actual
  if [[ ! -e "$pointer" && ! -L "$pointer" ]]; then return 0; fi
  if [[ ! -f "$pointer" || -L "$pointer" ]]; then
    ROLLBACK_STATUS=unavailable
    ROLLBACK_MESSAGE='The rollback pointer is missing or unsafe.'
    return 0
  fi
  if ! candidate=$(cat "$pointer" 2>/dev/null) || ! version_ok "$candidate"; then
    ROLLBACK_STATUS=unavailable
    ROLLBACK_MESSAGE='The rollback version is invalid.'
    return 0
  fi
  ROLLBACK_VERSION=$candidate
  dir="$PLUGIN_HOME/versions/$candidate"
  if [[ ! -d "$PLUGIN_HOME/versions" || -L "$PLUGIN_HOME/versions" || ! -d "$dir" || -L "$dir" ]]; then
    ROLLBACK_STATUS=unavailable
    ROLLBACK_MESSAGE='The retained runtime directory is missing or unsafe.'
    return 0
  fi
  receipt="$dir/runtime.lock.json"
  if [[ ! -f "$receipt" || -L "$receipt" ]]; then
    ROLLBACK_STATUS=unavailable
    ROLLBACK_MESSAGE='The retained runtime receipt is missing or unsafe.'
    return 0
  fi
  if ! receipt_version=$(field "$receipt" version) || [[ "$receipt_version" != "$candidate" ]]; then
    ROLLBACK_STATUS=unavailable
    ROLLBACK_MESSAGE='The retained runtime receipt is invalid.'
    return 0
  fi
  if ! expected=$(field "$receipt" binary.sha256) || [[ ! "$expected" =~ ^[0-9a-f]{64}$ ]]; then
    ROLLBACK_STATUS=unavailable
    ROLLBACK_MESSAGE='The retained runtime receipt has an invalid SHA-256.'
    return 0
  fi
  binary="$dir/hollis"
  if [[ ! -f "$binary" || -L "$binary" || ! -x "$binary" ]]; then
    ROLLBACK_STATUS=unavailable
    ROLLBACK_MESSAGE='The retained runtime executable is missing or unsafe.'
    return 0
  fi
  if ! actual=$(digest "$binary" 2>/dev/null) || [[ "$actual" != "$expected" ]]; then
    ROLLBACK_STATUS=unavailable
    ROLLBACK_MESSAGE='The retained runtime failed integrity verification.'
    return 0
  fi
  if ! (verify_native "$dir" "$receipt") >/dev/null 2>&1; then
    ROLLBACK_STATUS=unavailable
    ROLLBACK_MESSAGE='The retained native helper failed verification.'
    return 0
  fi
  ROLLBACK_STATUS=available
  ROLLBACK_MESSAGE='The retained runtime was verified and can be restored.'
  return 0
}
managed_report() {
  rollback_state
  printf '{"status":'; json_string "$1"
  printf ',"message":'; json_string "$2"
  printf ',"runtime_home":'; json_string "$PLUGIN_HOME"
  printf ',"rollback":{"status":'; json_string "$ROLLBACK_STATUS"
  printf ',"version":'
  if [[ -n "$ROLLBACK_VERSION" ]]; then json_string "$ROLLBACK_VERSION"; else printf 'null'; fi
  printf ',"message":'; json_string "$ROLLBACK_MESSAGE"
  printf '}}\n'
}
field() { /usr/bin/plutil -extract "$2" raw -o - "$1" 2>/dev/null; }
version_ok() { [[ "$1" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; }
newer() {
  local left=$1 right=$2 a b c x y z
  version_ok "$left" && version_ok "$right" || return 1
  IFS=. read -r a b c <<< "$left"; IFS=. read -r x y z <<< "$right"
  ((10#$a > 10#$x || (10#$a == 10#$x && 10#$b > 10#$y) || (10#$a == 10#$x && 10#$b == 10#$y && 10#$c > 10#$z)))
}
regular() { [[ -f "$1" && ! -L "$1" ]] || fail "Expected a regular file: $1"; }
safe_path() {
  local p=$1 cur= part
  [[ "$p" == /* && "$p" != / && "$p" != *$'\n'* && "$p" != *$'\r'* && "$p" != *$'\t'* ]] || fail 'Use an absolute installation path without control characters.'
  while IFS= read -r part; do
    [[ -z "$part" ]] && continue
    [[ "$part" != . && "$part" != .. ]] || fail 'Installation paths cannot contain dot components.'
    cur="$cur/$part"
    [[ ! -L "$cur" ]] || fail "Installation path contains a symlink: $cur"
  done < <(printf '%s\n' "$p" | tr / '\n')
}
private_dir() {
  safe_path "$1"
  if ! mkdir -p "$1"; then
    report access_required "Cannot create $1. Allow local filesystem access in the host and rerun this setup step."
    exit 5
  fi
  [[ -d "$1" && $(stat -f %u "$1") == "$(id -u)" ]] || fail "Directory is not owned by this user: $1"
  if ! chmod 700 "$1"; then
    report access_required "Cannot protect $1. Allow local filesystem access in the host and rerun this setup step."
    exit 5
  fi
}
digest() { /usr/bin/shasum -a 256 "$1" | /usr/bin/awk '{print $1}'; }
verify() {
  regular "$1"
  [[ "$2" =~ ^[0-9a-f]{64}$ ]] || fail 'Invalid pinned SHA-256.'
  [[ $(digest "$1") == "$2" ]] || fail "Integrity check failed for $(basename "$1"); nothing will be installed or run."
}
atomic_text() {
  local dest=$1 value=$2 tmp
  [[ ! -L "$dest" ]] || fail "Refusing symlink: $dest"
  tmp=$(mktemp "${dest}.XXXXXX"); printf '%s\n' "$value" > "$tmp"; mv -f "$tmp" "$dest"
}
cleanup() {
  [[ -z "$WORK_DIR" ]] || rm -rf "$WORK_DIR"
  if [[ -n "$LOCK_DIR" ]]; then rm -f "$LOCK_DIR/pid"; rmdir "$LOCK_DIR"; fi
  return 0
}
acquire() {
  private_dir "$PLUGIN_HOME"
  LOCK_DIR="$PLUGIN_HOME/$1.lock"
  if ! mkdir "$LOCK_DIR" 2>/dev/null; then
    LOCK_DIR=
    if [[ -e "$PLUGIN_HOME/$1.lock" || -L "$PLUGIN_HOME/$1.lock" ]]; then
      fail "Another $1 operation is active or was interrupted. Inspect $PLUGIN_HOME/$1.lock/pid before removing a stale lock; do not start a duplicate call."
    fi
    report access_required "Cannot create $1 lock. Allow local filesystem access to $PLUGIN_HOME in the host; no existing lock was found."
    exit 5
  fi
  printf '%s\n' "$$" > "$LOCK_DIR/pid"
  trap cleanup EXIT
  trap 'exit 130' INT
  trap 'exit 143' TERM
}
platform() {
  [[ $(uname -s) == Darwin ]] || { report unsupported 'A local Apple-silicon Mac is required.'; exit 3; }
  local arm virtual
  virtual=$(sysctl -n kern.hv_vmm_present 2>/dev/null || true)
  [[ "$virtual" != 1 ]] || { report unsupported 'This setup supports physical Apple-silicon Macs, not virtual macOS guests.'; exit 3; }
  arm=$(sysctl -n hw.optional.arm64 2>/dev/null || true)
  if [[ $(uname -m) != arm64 && "$arm" != 1 ]]; then
    if [[ "$arm" == 0 ]]; then report unsupported 'Apple Intelligence requires Apple silicon; an Intel binary cannot add it.'; exit 3
    else report unknown 'Hardware discovery was restricted. Check from the local Mac before deciding compatibility.'; exit 5; fi
  fi
  local os
  os=$(sw_vers -productVersion)
  [[ ${os%%.*} == 27 ]] || { report unsupported "This plugin supports the tested macOS 27 route; found $os."; exit 3; }
}
read_lock() {
  regular "$RUNTIME_LOCK"
  [[ $(field "$RUNTIME_LOCK" schema_version) == 1 || $(field "$RUNTIME_LOCK" schema_version) == 2 ]] || fail 'Unsupported runtime lock.'
  VERSION=$(field "$RUNTIME_LOCK" version)
  version_ok "$VERSION" || fail 'Invalid runtime version.'
  BINARY_NAME=$(field "$RUNTIME_LOCK" binary.name)
  BRIDGES_NAME=$(field "$RUNTIME_LOCK" bridges.name)
  [[ "$BINARY_NAME" == hollis-darwin-arm64 && "$BRIDGES_NAME" == hollis-bridges.zip ]] || fail 'Unexpected runtime asset names.'
}
runtime_path() {
  safe_path "$PLUGIN_HOME"
  regular "$PLUGIN_HOME/current"
  local ver dir
  ver=$(cat "$PLUGIN_HOME/current"); version_ok "$ver" || fail 'Invalid installed version pointer.'
  dir="$PLUGIN_HOME/versions/$ver"; safe_path "$dir"
  regular "$dir/runtime.lock.json"
  [[ $(field "$dir/runtime.lock.json" version) == "$ver" ]] || fail 'Installed runtime receipt does not match its version.'
  verify "$dir/hollis" "$(field "$dir/runtime.lock.json" binary.sha256)"
  verify_native "$dir" "$dir/runtime.lock.json"
  printf '%s\n' "$dir/hollis"
}
external_newer() {
  external_cli || return 1
  newer "$EXTERNAL_VERSION" "$VERSION" || return 1
  printf '%s\n' "$EXTERNAL_CANDIDATE"
}
external_cli() {
  # Any standalone hollis on the caller's PATH (Homebrew, direct, source or go
  # install) is an external runtime. A successful --version identifies the
  # candidate even when its last token is not an x.y.z version: callers treat
  # it as neither newer than the managed runtime nor a missing installation.
  EXTERNAL_CANDIDATE= EXTERNAL_VERSION=
  local candidate out ver
  candidate=$(PATH="$HOLLIS_CALLER_PATH" command -v hollis || true)
  [[ "$candidate" == /* && -x "$candidate" ]] || return 1
  out=$("$candidate" --version 2>/dev/null) || return 1
  ver=${out##* }
  EXTERNAL_CANDIDATE=$candidate
  EXTERNAL_VERSION=$ver
  version_ok "$ver" || return 1
}

# The helper belongs to the selected runtime directory, never to PATH. Older
# retained receipts do not contain a native helper and remain valid for rollback.
verify_native() {
  local dir=$1 receipt=$2 name version protocol
  name=$(field "$receipt" native.name || true)
  if [[ -z "$name" ]]; then
    [[ $(field "$receipt" schema_version) != 2 ]] || fail 'Native helper pin is missing.'
    return 0
  fi
  [[ "$name" == hollis-native-darwin-arm64 ]] || fail 'Unexpected native helper asset.'
  protocol=$(field "$receipt" native.protocol_version || true)
  valid_protocol "$protocol" || fail 'Invalid native helper protocol receipt.'
  version=$(field "$receipt" native.version || field "$receipt" version)
  verify "$dir/hollis-native" "$(field "$receipt" native.sha256)"
  [[ -x "$dir/hollis-native" ]] || fail 'Native helper is not executable.'
  [[ $("$dir/hollis-native" --version) == "hollis-native $version" ]] || fail 'Native helper version does not match runtime.'
  [[ $("$dir/hollis-native" --protocol-version) == "$protocol" ]] || fail 'Native helper protocol does not match runtime.'
}

canonical_executable() {
  local path=$1 target count=0 parent
  [[ "$path" == /* && "$path" != *$'\n'* && "$path" != *$'\r'* && "$path" != *$'\t'* ]] || return 1
  while [[ -L "$path" ]]; do
    count=$((count + 1)); [[ $count -le 40 ]] || return 1
    target=$(/usr/bin/readlink "$path") || return 1
    if [[ "$target" == /* ]]; then path=$target; else path="$(dirname "$path")/$target"; fi
  done
  parent=$(cd "$(dirname "$path")" && pwd -P) || return 1
  printf '%s/%s\n' "$parent" "$(basename "$path")"
}

emit_runtime_record() {
  # Defense in depth: the protocol is the record's only unquoted
  # interpolation. The producer gate below rejects a corrupt pin with exit 10
  # before this runs; this keeps a future caller from emitting invalid JSON.
  if [[ -n "$RR_HELPER_PROTOCOL" ]] && ! valid_protocol "$RR_HELPER_PROTOCOL"; then
    RR_HELPER_PROTOCOL= RR_HELPER_VERSION=
  fi
  printf '{"contract_version":1,"state":'; json_string "$RR_STATE"
  printf ',"reason_code":'; json_string "$RR_REASON"
  # Frozen contract key: optional message, no raw subprocess output.
  [[ -z "$RR_DETAIL" ]] || { printf ',"message":'; json_string "$RR_DETAIL"; }
  printf ',"runtime":{"owner":'; json_string "$RR_OWNER"
  [[ -z "$RR_VERSION" ]] || { printf ',"version":'; json_string "$RR_VERSION"; }
  [[ -z "$RR_COMMIT" ]] || { printf ',"source_commit":'; json_string "$RR_COMMIT"; }
  [[ -z "$RR_CLI" ]] || { printf ',"executable_path":'; json_string "$RR_CLI"; }
  [[ -z "$RR_ROOT" ]] || { printf ',"install_root":'; json_string "$RR_ROOT"; }
  printf '},"artifact":{"verification":'; json_string "$RR_ARTIFACT"
  [[ -z "$RR_NAME" ]] || { printf ',"name":'; json_string "$RR_NAME"; }
  [[ -z "$RR_HASH" ]] || { printf ',"sha256":'; json_string "$RR_HASH"; }
  printf '},"helper":{"verification":'; json_string "$RR_HELPER_STATE"
  printf ',"reason_code":'; json_string "$RR_HELPER_REASON"
  [[ -z "$RR_HELPER_PATH" ]] || { printf ',"path":'; json_string "$RR_HELPER_PATH"; }
  [[ -z "$RR_HELPER_VERSION" ]] || { printf ',"version":'; json_string "$RR_HELPER_VERSION"; }
  [[ -z "$RR_HELPER_PROTOCOL" ]] || printf ',"protocol":%s' "$RR_HELPER_PROTOCOL"
  printf '},"platform":'
  if [[ "$RR_OWNER" == plugin ]]; then
    # minimum_macos is an unquoted JSON integer (the tested macOS major
    # version, matching runner.MeasuredOSMajor); the Go record decoder
    # rejects a string.
    printf '{"os":"darwin","architecture":"arm64","minimum_macos":27}'
  else printf '{}'; fi
  printf ',"bridges":{"verification":'; json_string "$RR_BRIDGES_STATE"
  [[ -z "$RR_BRIDGES_PATH" ]] || { printf ',"archive_path":'; json_string "$RR_BRIDGES_PATH"; }
  [[ -z "$RR_BRIDGES_HASH" ]] || { printf ',"archive_sha256":'; json_string "$RR_BRIDGES_HASH"; }
  printf ',"members":%s},"receipt":{"schema_version":1,"owner":' "$RR_MEMBERS"; json_string "$RR_OWNER"
  [[ -z "$RR_CURRENT" ]] || { printf ',"current_version":'; json_string "$RR_CURRENT"; }
  [[ -z "$RR_PREVIOUS" ]] || { printf ',"previous_version":'; json_string "$RR_PREVIOUS"; }
  printf '}}\n'
}

emit_external_unverified() {
  # Contract: a standalone, source, go-install, direct or Homebrew runtime
  # without plugin verification is unknown/external_runtime_unverified with
  # owner external, whatever its version relation to the managed runtime.
  RR_OWNER=external RR_NAME= RR_ARTIFACT=unknown
  RR_STATE=unknown RR_REASON=external_runtime_unverified
  RR_CLI=$(canonical_executable "$EXTERNAL_CANDIDATE" 2>/dev/null || true)
  RR_VERSION=$EXTERNAL_VERSION
  emit_runtime_record
}

runtime_record() {
  # This read-only child may run while its parent holds setup.lock. Never
  # acquire that lock or call setup recursively here.
  RR_STATE=action_required RR_REASON=runtime_integrity_failed RR_OWNER=plugin
  RR_VERSION= RR_COMMIT= RR_CLI= RR_ROOT= RR_HASH= RR_CURRENT= RR_PREVIOUS=
  RR_NAME=hollis-darwin-arm64 RR_ARTIFACT=failed
  RR_HELPER_STATE=unknown RR_HELPER_REASON=helper_unverified
  RR_HELPER_PATH= RR_HELPER_VERSION= RR_HELPER_PROTOCOL=
  RR_BRIDGES_STATE=unknown RR_BRIDGES_PATH= RR_BRIDGES_HASH= RR_MEMBERS='[]'
  RR_DETAIL=
  local candidate out ver dir receipt schema expected actual native_name native_hash
  local bridge_hash i name expected_names actual_names members= separator= member_hash previous
  local bridge_names=()
  if ! (read_lock; safe_path "$PLUGIN_HOME") >/dev/null 2>&1; then
    emit_runtime_record; return 10
  fi
  VERSION=$(field "$RUNTIME_LOCK" version)
  external_cli || true
  if [[ -n "$EXTERNAL_CANDIDATE" ]] && newer "$EXTERNAL_VERSION" "$VERSION"; then
    emit_external_unverified; return 10
  fi
  if [[ ! -e "$PLUGIN_HOME/current" && ! -L "$PLUGIN_HOME/current" ]]; then
    if [[ -n "$EXTERNAL_CANDIDATE" ]]; then
      # No plugin receipt: a same-version or older standalone CLI is still an
      # unverified external runtime, never a missing managed installation.
      emit_external_unverified; return 10
    fi
    RR_REASON=runtime_not_installed RR_ARTIFACT=unknown
    emit_runtime_record; return 10
  fi
  if [[ ! -f "$PLUGIN_HOME/current" || -L "$PLUGIN_HOME/current" ]]; then
    emit_runtime_record; return 10
  fi
  ver=$(cat "$PLUGIN_HOME/current")
  if ! version_ok "$ver"; then emit_runtime_record; return 10; fi
  RR_CURRENT=$ver
  dir="$PLUGIN_HOME/versions/$ver"; receipt="$dir/runtime.lock.json"
  if ! (safe_path "$dir") >/dev/null 2>&1 || [[ ! -f "$receipt" || -L "$receipt" ]]; then
    emit_runtime_record; return 10
  fi
  schema=$(field "$receipt" schema_version || true)
  expected=$(field "$receipt" binary.sha256 || true)
  RR_COMMIT=$(field "$receipt" source_commit || true)
  bridge_hash=$(field "$receipt" bridges.sha256 || true)
  if [[ ( "$schema" != 1 && "$schema" != 2 ) ||
        $(field "$receipt" version || true) != "$ver" ||
        $(field "$receipt" repository || true) != kamenxrider/hollis ||
        $(field "$receipt" source_ref || true) != "refs/tags/v$ver" ||
        ! "$RR_COMMIT" =~ ^[0-9a-f]{40}$ ||
        $(field "$receipt" binary.name || true) != hollis-darwin-arm64 ||
        ! "$expected" =~ ^[0-9a-f]{64}$ ||
        $(field "$receipt" bridges.name || true) != hollis-bridges.zip ||
        ! "$bridge_hash" =~ ^[0-9a-f]{64}$ ]]; then
    RR_COMMIT= RR_DETAIL='Installed runtime receipt does not match its version.'
    emit_runtime_record; return 10
  fi
  for i in 0 1 2 3 4; do
    name=$(field "$receipt" "bridge_files.$i" || true)
    if [[ -z "$name" || "$name" == */* || "$name" == *\\* ||
          "$name" == *'*'* || "$name" == *'?'* || "$name" == *'['* || "$name" == *']'* || "$name" == *$'\n'* ||
          "$name" == *$'\r'* || "$name" == *$'\t'* || "$name" != *.shortcut ]]; then
      emit_runtime_record; return 10
    fi
    bridge_names[$i]=$name
  done
  expected_names=$(printf '%s\n' "${bridge_names[@]}" | LC_ALL=C sort)
  if [[ $(printf '%s\n' "${bridge_names[@]}" | LC_ALL=C sort -u | wc -l | tr -d ' ') != 5 ]] ||
      field "$receipt" bridge_files.5 >/dev/null 2>&1; then
    emit_runtime_record; return 10
  fi
  native_name=$(field "$receipt" native.name || true)
  if [[ -n "$native_name" ]]; then
    native_hash=$(field "$receipt" native.sha256 || true)
    RR_HELPER_VERSION=$(field "$receipt" native.version || field "$receipt" version)
    RR_HELPER_PROTOCOL=$(field "$receipt" native.protocol_version || true)
    if [[ "$native_name" != hollis-native-darwin-arm64 || ! "$native_hash" =~ ^[0-9a-f]{64}$ ]] ||
        ! valid_protocol "$RR_HELPER_PROTOCOL" || ! version_ok "$RR_HELPER_VERSION"; then
      RR_HELPER_VERSION= RR_HELPER_PROTOCOL=
      emit_runtime_record; return 10
    fi
  elif [[ "$schema" == 2 ]]; then
    emit_runtime_record; return 10
  fi
  RR_VERSION=$ver RR_ROOT=$dir RR_CLI="$dir/hollis" RR_HASH=$expected
  if [[ ! -f "$RR_CLI" || -L "$RR_CLI" || ! -x "$RR_CLI" ]] ||
      ! actual=$(digest "$RR_CLI" 2>/dev/null) || [[ "$actual" != "$expected" ]]; then
    RR_DETAIL="Integrity check failed for $(basename "$RR_CLI"); nothing will be installed or run."
    emit_runtime_record; return 10
  fi
  if ! out=$("$RR_CLI" --version 2>/dev/null) || [[ "$out" != "hollis $ver" ]]; then
    emit_runtime_record; return 10
  fi
  RR_STATE=verified RR_REASON=runtime_verified RR_ARTIFACT=verified
  if [[ -f "$PLUGIN_HOME/previous" && ! -L "$PLUGIN_HOME/previous" ]]; then
    previous=$(cat "$PLUGIN_HOME/previous")
    if version_ok "$previous"; then RR_PREVIOUS=$previous; fi
  fi
  if [[ -z "$native_name" ]]; then
    RR_HELPER_STATE=missing RR_HELPER_REASON=helper_not_pinned
  else
    RR_HELPER_PATH="$dir/hollis-native"
    if [[ ! -f "$RR_HELPER_PATH" || -L "$RR_HELPER_PATH" ]]; then
      RR_HELPER_STATE=missing RR_HELPER_REASON=helper_missing
    elif [[ ! -x "$RR_HELPER_PATH" ]] ||
        ! actual=$(digest "$RR_HELPER_PATH" 2>/dev/null) || [[ "$actual" != "$native_hash" ]]; then
      RR_HELPER_STATE=failed RR_HELPER_REASON=helper_integrity_failed
    elif ! out=$("$RR_HELPER_PATH" --version 2>/dev/null); then
      RR_HELPER_STATE=failed RR_HELPER_REASON=helper_check_failed
    elif [[ "$out" != "hollis-native $RR_HELPER_VERSION" ]]; then
      RR_HELPER_STATE=mismatch RR_HELPER_REASON=helper_version_mismatch
    elif ! out=$("$RR_HELPER_PATH" --protocol-version 2>/dev/null); then
      RR_HELPER_STATE=failed RR_HELPER_REASON=helper_check_failed
    elif [[ "$out" != "$RR_HELPER_PROTOCOL" ]]; then
      RR_HELPER_STATE=mismatch RR_HELPER_REASON=helper_protocol_mismatch
    else RR_HELPER_STATE=verified RR_HELPER_REASON=helper_verified; fi
  fi
  RR_BRIDGES_PATH="$dir/hollis-bridges.zip" RR_BRIDGES_HASH=$bridge_hash
  if [[ ! -f "$RR_BRIDGES_PATH" || -L "$RR_BRIDGES_PATH" ]]; then
    RR_BRIDGES_STATE=missing
  elif ! actual=$(digest "$RR_BRIDGES_PATH" 2>/dev/null) || [[ "$actual" != "$bridge_hash" ]]; then
    RR_BRIDGES_STATE=failed
  elif ! actual_names=$(/usr/bin/unzip -Z1 "$RR_BRIDGES_PATH" 2>/dev/null | LC_ALL=C sort) ||
      [[ "$actual_names" != "$expected_names" ]]; then
    RR_BRIDGES_STATE=mismatch
  else
    RR_BRIDGES_STATE=verified
    while IFS= read -r name; do
      if ! member_hash=$(/usr/bin/unzip -p "$RR_BRIDGES_PATH" "$name" 2>/dev/null |
          /usr/bin/shasum -a 256 | /usr/bin/awk '{print $1}'); then
        RR_BRIDGES_STATE=failed; break
      fi
      members="$members$separator{\"name\":$(json_string "$name"),\"sha256\":$(json_string "$member_hash")}"
      separator=,
    done <<< "$expected_names"
    if [[ "$RR_BRIDGES_STATE" == verified ]]; then RR_MEMBERS="[$members]"; fi
  fi
  emit_runtime_record
  return 0
}
