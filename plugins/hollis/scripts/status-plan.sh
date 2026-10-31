#!/bin/bash
# Selected-route adapter. Exit 64 delegates only to explicitly legacy status.
set -euo pipefail
source "$(dirname "$0")/common.sh"
ROUTE=${1:-}
case "$ROUTE" in local|cloud|cloud-pro|on-device|chatgpt|image) ;; *) fail 'Select one concrete setup route.';; esac
WORK_DIR=$(mktemp -d "${TMPDIR:-/tmp}/hollis-plan.XXXXXX")
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

unknown() {
  printf '{"source":"bootstrap","status":"unknown","reason_code":"runtime_unverified","inference_tested":false,"message":"The selected runtime setup contract could not be validated. Preserve the installation and inspect its owner."}\n'
  exit 10
}
record_code=0
"$PLUGIN_ROOT/scripts/setup.sh" runtime-record > "$WORK_DIR/runtime.json" 2>/dev/null || record_code=$?
# Compatibility with an older plugin source that has no record producer.
if [[ "$record_code" == 0 ]] && /usr/bin/grep -q '^Usage: setup.sh ' "$WORK_DIR/runtime.json"; then exit 64; fi
[[ "$record_code" == 0 || "$record_code" == 10 ]] || unknown
[[ $(field "$WORK_DIR/runtime.json" contract_version || true) == 1 ]] || unknown
state=$(field "$WORK_DIR/runtime.json" state || true)
if [[ "$state" != verified ]]; then
  # External runtimes retain independent legacy availability diagnostics, not
  # authenticated readiness. Do not pass them into the Go planner.
  if [[ "$state" == unknown && $(field "$WORK_DIR/runtime.json" reason_code || true) == external_runtime_unverified ]]; then exit 64; fi
  unknown
fi
[[ "$record_code" == 0 && $(field "$WORK_DIR/runtime.json" artifact.verification || true) == verified ]] || unknown
BIN=$(field "$WORK_DIR/runtime.json" runtime.executable_path || true)
[[ "$BIN" == /* && -f "$BIN" && ! -L "$BIN" && -x "$BIN" ]] || unknown

set --
if [[ -f "$PLUGIN_HOME/imports/pending" && ! -L "$PLUGIN_HOME/imports/pending" ]]; then
  value=$(cat "$PLUGIN_HOME/imports/pending")
  case "$value" in cloud|cloud-pro|on-device|chatgpt|image) set -- --pending-import "$value";; esac
fi
code=0
"$BIN" setup --model "$ROUTE" --runtime-record-command "$PLUGIN_ROOT/scripts/setup.sh" "$@" --json > "$WORK_DIR/plan.json" 2>/dev/null || code=$?
if [[ "$code" == 2 && $(field "$WORK_DIR/plan.json" error.code || true) == usage ]]; then
  message=$(field "$WORK_DIR/plan.json" error.message || true)
  case "$message" in
    'unknown command "setup"'*|'invalid command flags: unknown flag: --runtime-record-command') exit 64;;
  esac
fi
[[ $(field "$WORK_DIR/plan.json" schema_version || true) == 1 ]] || unknown
[[ $(field "$WORK_DIR/plan.json" route || true) == "$ROUTE" ]] || unknown
[[ $(field "$WORK_DIR/plan.json" inference_tested || true) == false ]] || unknown
source=$(field "$WORK_DIR/plan.json" source || true)
[[ "$source" == planner-v1 || "$source" == bootstrap ]] || unknown
status=$(field "$WORK_DIR/plan.json" status || true)
case "$status" in ready|import_required|approval_required|model_not_ready|unknown) ;; *) unknown;; esac
case "$code:$status" in 0:ready|3:import_required|5:unknown|7:unknown|10:unknown|10:approval_required|10:model_not_ready) ;; *) unknown;; esac
# Preserve the old routes[] view while exposing the exact shared plan. Neither
# this translation nor discovery claims successful inference.
legacy_status=$status
case "$status" in ready) legacy_status=discovered;; import_required) legacy_status=missing;; esac
printf '{"source":'; json_string "$source"
printf ',"inference_tested":false,"runtime_home":'; json_string "$PLUGIN_HOME"
printf ',"runtime_path":'; json_string "$BIN"
printf ',"config_path":null,"plan":'; cat "$WORK_DIR/plan.json"
printf ',"routes":[{"route":'; json_string "$ROUTE"
printf ',"status":'; json_string "$legacy_status"
printf ',"reference":'; json_string "$(field "$WORK_DIR/plan.json" bridge.reference || true)"
printf ',"configured":'
configured=$(field "$WORK_DIR/plan.json" bridge.configured || true)
if [[ "$configured" == true ]]; then printf true; else printf false; fi
printf '}]}\n'
exit "$code"
