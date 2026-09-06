#!/bin/bash
set -euo pipefail
source "$(dirname "$0")/common.sh"
platform; read_lock
MODE=${1:-status}; ROUTE=${2:-all}
[[ $# -le 3 ]] || fail 'Usage: import <route> [--reopen] or status [route].'
[[ -z ${3:-} || ( "$MODE" == import && "$3" == --reopen ) ]] || fail 'Only import accepts --reopen.'
case "$ROUTE" in cloud|cloud-pro|on-device|chatgpt|image|all) ;; *) fail 'Unknown bridge route.';; esac
[[ "$MODE" != import || "$ROUTE" != all ]] || fail 'Import one selected bridge at a time.'
BIN=$("$PLUGIN_ROOT/scripts/setup.sh" path)
if [[ "$MODE" == status ]]; then
  # Readiness is read-only: concurrent checks need neither a setup lock nor
  # permission to write inside the installed runtime directory.
  WORK_DIR=$(mktemp -d "${TMPDIR:-/tmp}/hollis-status.XXXXXX")
  trap cleanup EXIT
  trap 'exit 130' INT
  trap 'exit 143' TERM
else
  acquire setup
  WORK_DIR=$(mktemp -d "$PLUGIN_HOME/.bridge.XXXXXX")
fi
# Discovery is separate from inference. A helper failure does not mean missing.
if ! /usr/bin/shortcuts list > "$WORK_DIR/shortcuts.txt" 2> "$WORK_DIR/discovery-error.txt"; then
  report unknown 'Shortcuts discovery failed. Run from the local Mac with host access; do not reinstall or retry inference blindly.'
  exit 5
fi
"$BIN" config show --json > "$WORK_DIR/config.json"
"$BIN" doctor --json > "$WORK_DIR/doctor.json" 2> "$WORK_DIR/doctor-error.txt" || true
bridge_name() {
  case "$1" in
    cloud) printf 'AFM Bridge - Cloud';; cloud-pro) printf 'AFM Bridge - Cloud Pro';;
    on-device) printf 'AFM Bridge - On-Device';; chatgpt) printf 'AFM Bridge - ChatGPT';;
    image) printf 'Hollis Image - Reference Input v2';;
  esac
}
route_info() {
  local route=$1 i model
  ROUTE_REF=; ROUTE_VERIFIED=false; ROUTE_STATUS=missing; ROUTE_CONFIGURED=true
  if [[ "$route" == image ]]; then
    ROUTE_REF=$(field "$WORK_DIR/config.json" image_bridge || true)
    [[ -n "$ROUTE_REF" ]] || ROUTE_CONFIGURED=false
    [[ -n "$ROUTE_REF" ]] || ROUTE_REF=$(bridge_name image)
    if /usr/bin/grep -Fxq -- "$ROUTE_REF" "$WORK_DIR/shortcuts.txt"; then ROUTE_VERIFIED=true; ROUTE_STATUS=discovered; fi
  else
    for i in 0 1 2 3; do
      model=$(field "$WORK_DIR/doctor.json" "bridges.$i.model" || true)
      if [[ "$model" == "$route" ]]; then
        ROUTE_REF=$(field "$WORK_DIR/doctor.json" "bridges.$i.resolved_ref")
        ROUTE_VERIFIED=$(field "$WORK_DIR/doctor.json" "bridges.$i.verified")
        ROUTE_STATUS=$(field "$WORK_DIR/doctor.json" "bridges.$i.status")
        [[ "$ROUTE_VERIFIED" != true ]] || ROUTE_STATUS=discovered
        return
      fi
    done
    ROUTE_STATUS=unknown
  fi
}
if [[ "$MODE" == status ]]; then
  printf '{"inference_tested":false,"routes":['
  comma=
  for route in cloud cloud-pro on-device chatgpt image; do
    [[ "$ROUTE" == all || "$ROUTE" == "$route" ]] || continue
    route_info "$route"
    printf '%s{"route":' "$comma"; json_string "$route"; printf ',"status":'; json_string "$ROUTE_STATUS"
    printf ',"reference":'; json_string "$ROUTE_REF"; printf ',"configured":%s}' "$ROUTE_CONFIGURED"; comma=,
  done
  printf ']}\n'; exit 0
fi
[[ "$MODE" == import ]] || fail 'Expected import or status.'
route_info "$ROUTE"
if [[ "$ROUTE_VERIFIED" == true ]]; then
  if [[ "$ROUTE" == image && -z $(field "$WORK_DIR/config.json" image_bridge || true) ]]; then
    "$BIN" config set image-bridge "$ROUTE_REF" >/dev/null
  fi
  private_dir "$PLUGIN_HOME/imports"
  atomic_text "$PLUGIN_HOME/imports/$ROUTE.discovered" "$ROUTE_REF"
  if [[ -f "$PLUGIN_HOME/imports/pending" ]]; then
    regular "$PLUGIN_HOME/imports/pending"
    if [[ $(cat "$PLUGIN_HOME/imports/pending") == "$ROUTE" ]]; then rm "$PLUGIN_HOME/imports/pending"; fi
  fi
  report discovered "Existing $ROUTE bridge preserved. Its next requested call will test Apple access."
  exit 0
fi
# Do not overwrite a user's configured but currently unlisted bridge.
if [[ "$ROUTE" == image ]]; then custom=$(field "$WORK_DIR/config.json" image_bridge || true)
else custom=$(field "$WORK_DIR/config.json" "bridges.$ROUTE" || true); fi
if [[ -n "$custom" ]]; then
  # config show includes only explicit overrides; see runtime contract.
  report action_required "Configured $ROUTE reference is not verified: $custom. Preserve it and resolve discovery or confirm a replacement."
  exit 3
fi
if [[ -f "$PLUGIN_HOME/imports/pending" && ${3:-} != --reopen ]]; then
  regular "$PLUGIN_HOME/imports/pending"
  if [[ $(cat "$PLUGIN_HOME/imports/pending") == "$ROUTE" ]]; then
    report permission_pending 'An import is already pending. Complete Add Shortcut; if you closed it and want to try again, explicitly use import <route> --reopen.'
    exit 0
  fi
fi
dir="$PLUGIN_HOME/versions/$VERSION"
safe_path "$dir"
verify "$dir/hollis-bridges.zip" "$(field "$dir/runtime.lock.json" bridges.sha256)"
mkdir "$WORK_DIR/unsigned"
unzip -q "$dir/hollis-bridges.zip" -d "$WORK_DIR/unsigned"
name=$(bridge_name "$ROUTE")
private_dir "$PLUGIN_HOME/imports"
target="$PLUGIN_HOME/imports/$name.shortcut"
[[ ! -L "$target" ]] || fail 'Refusing symlinked import output.'
printf 'Hollis will open “%s”. Choose Add Shortcut. The first model call may then ask Allow.\n' "$name" >&2
/usr/bin/shortcuts sign --mode anyone --input "$WORK_DIR/unsigned/$name.shortcut" --output "$WORK_DIR/signed.shortcut"
mv "$WORK_DIR/signed.shortcut" "$target"
atomic_text "$PLUGIN_HOME/imports/pending" "$ROUTE"
/usr/bin/open "$target"
report permission_pending "Add $name in Shortcuts, then run import $ROUTE again to verify discovery."
