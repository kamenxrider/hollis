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
  [[ -x /usr/bin/shortcuts ]] || fail 'Shortcuts is unavailable.'
}
read_lock() {
  regular "$RUNTIME_LOCK"
  [[ $(field "$RUNTIME_LOCK" schema_version) == 1 ]] || fail 'Unsupported runtime lock.'
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
  printf '%s\n' "$dir/hollis"
}
external_newer() {
  local candidate out ver
  candidate=$(PATH="$HOLLIS_CALLER_PATH" command -v hollis || true)
  [[ "$candidate" == /* && -x "$candidate" ]] || return 1
  out=$("$candidate" --version 2>/dev/null) || return 1
  ver=${out##* }
  newer "$ver" "$VERSION" || return 1
  printf '%s\n' "$candidate"
}
