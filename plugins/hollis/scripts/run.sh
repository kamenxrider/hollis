#!/bin/bash
# Resolve a verified runtime and preserve its exact stdout, stderr and exit code.
set -euo pipefail
source "$(dirname "$0")/common.sh"
platform
read_lock
BIN=$("$PLUGIN_ROOT/scripts/setup.sh" path)
[[ $# -gt 0 ]] || fail 'Pass a Hollis command; use agent-context for its contract.'
# Metadata and local-state commands do not acquire the provider lock.
case "$1" in
  agent-context|doctor|models|config|chats|--version|--help) exec "$BIN" "$@";;
  image) if [[ "${2:-}" == styles ]]; then exec "$BIN" "$@"; fi;;
  batch) if [[ "${2:-}" == plan ]]; then exec "$BIN" "$@"; fi;;
esac
acquire inference
# Every serialized call waits at least five seconds after the previous call.
# Cloud Pro has the established longer interval. Batch also enforces its own pacing.
delay=5
selected=; job=; expect=
for arg in "$@"; do
  if [[ "$expect" == model ]]; then selected=$arg; expect=; continue; fi
  if [[ "$expect" == job ]]; then job=$arg; expect=; continue; fi
  case "$arg" in
    --model) expect=model;; --model=*) selected=${arg#--model=};;
    --job) expect=job;; --job=*) job=${arg#--job=};;
  esac
done
# A batch's concrete tier is stored in its manifest. A resumed chat's tier is
# opaque here, so preserve the conservative interval for that case.
case "$1" in
  batch) selected=$(field "$job" model || printf unknown);;
  chat) [[ -n "$selected" ]] || selected=unknown;;
esac
case "$selected" in cloud-pro|unknown) delay=45;; esac
now=$(date +%s)
if [[ -f "$PLUGIN_HOME/last-call-ended" ]]; then
  regular "$PLUGIN_HOME/last-call-ended"
  ended=$(cat "$PLUGIN_HOME/last-call-ended")
  [[ "$ended" =~ ^[0-9]+$ ]] || fail 'Invalid pacing record; inspect it before starting another call.'
  # Epoch timestamps have whole-second precision. One guard second prevents
  # rounding down from shortening the promised minimum quiet interval.
  remaining=$((ended + delay + 1 - now))
  if ((remaining > 0)); then sleep "$remaining"; fi
fi
code=0
"$BIN" "$@" <&0 &
child=$!
cancel_call() {
  kill -TERM "$child" 2>/dev/null || true
  wait "$child" 2>/dev/null || true
  atomic_text "$PLUGIN_HOME/last-call-ended" "$(date +%s)"
  exit "$1"
}
trap 'cancel_call 130' INT
trap 'cancel_call 143' TERM
wait "$child" || code=$?
atomic_text "$PLUGIN_HOME/last-call-ended" "$(date +%s)"
exit "$code"
