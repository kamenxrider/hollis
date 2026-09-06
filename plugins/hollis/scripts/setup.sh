#!/bin/bash
set -euo pipefail
source "$(dirname "$0")/common.sh"

fetch_asset() {
  local key=$1 destination=$2 name expected url
  name=$(field "$RUNTIME_LOCK" "$key.name")
  expected=$(field "$RUNTIME_LOCK" "$key.sha256")
  if [[ -e "$PLUGIN_ROOT/assets/runtime/$name" ]]; then
    verify "$PLUGIN_ROOT/assets/runtime/$name" "$expected"
    cp "$PLUGIN_ROOT/assets/runtime/$name" "$destination"
  else
    url=$(field "$RUNTIME_LOCK" release_url)
    [[ "$url" == "https://github.com/kamenxrider/hollis/releases/download/v$VERSION" ]] || fail 'Unexpected release URL.'
    /usr/bin/curl --fail --location --proto '=https' --proto-redir '=https' --connect-timeout 20 --max-time 300 \
      --user-agent 'OpenAI File Downloader, XaiImageApiFetch/1.0' --output "$destination" "$url/$name"
  fi
  verify "$destination" "$expected"
}
install_runtime() {
  platform; read_lock
  local external= dest stage_names expected_names i current= keep_current=false
  external=$(external_newer || true)
  acquire setup
  if [[ -e "$PLUGIN_HOME/current" || -L "$PLUGIN_HOME/current" ]]; then
    regular "$PLUGIN_HOME/current"; current=$(cat "$PLUGIN_HOME/current")
    version_ok "$current" || fail 'Invalid installed version pointer.'
    if newer "$current" "$VERSION"; then
      runtime_path >/dev/null
      keep_current=true
    fi
  fi
  private_dir "$PLUGIN_HOME/versions"
  dest="$PLUGIN_HOME/versions/$VERSION"
  if [[ ! -e "$dest" ]]; then
    WORK_DIR=$(mktemp -d "$PLUGIN_HOME/versions/.stage.XXXXXX")
    fetch_asset binary "$WORK_DIR/hollis"
    fetch_asset bridges "$WORK_DIR/hollis-bridges.zip"
    stage_names=$(unzip -Z1 "$WORK_DIR/hollis-bridges.zip" | LC_ALL=C sort)
    expected_names=$(for i in 0 1 2 3 4; do field "$RUNTIME_LOCK" "bridge_files.$i"; done | LC_ALL=C sort)
    [[ "$stage_names" == "$expected_names" ]] || fail 'The archive must contain exactly the five pinned bridges, with no extra or duplicate entries.'
    mkdir "$WORK_DIR/bridges"
    unzip -q "$WORK_DIR/hollis-bridges.zip" -d "$WORK_DIR/bridges"
    for i in 0 1 2 3 4; do regular "$WORK_DIR/bridges/$(field "$RUNTIME_LOCK" "bridge_files.$i")"; done
    cp "$RUNTIME_LOCK" "$WORK_DIR/runtime.lock.json"
    chmod 700 "$WORK_DIR/hollis"
    [[ $("$WORK_DIR/hollis" --version) == "hollis $VERSION" ]] || fail 'Downloaded executable reports an unexpected version.'
    mv "$WORK_DIR" "$dest"; WORK_DIR=
  else
    safe_path "$dest"
    verify "$dest/hollis" "$(field "$RUNTIME_LOCK" binary.sha256)"
    verify "$dest/hollis-bridges.zip" "$(field "$RUNTIME_LOCK" bridges.sha256)"
    cmp -s "$dest/runtime.lock.json" "$RUNTIME_LOCK" || fail 'Existing runtime receipt differs; preserve and inspect it.'
  fi
  if [[ "$keep_current" == true ]]; then
    managed_report existing_newer "The pinned bridge kit is available. The newer installed Hollis was preserved and will be used."
    return 0
  fi
  if [[ -n "$external" ]]; then
    report existing_newer "The pinned bridge kit is available. The newer installed Hollis was preserved and will be used."
    return 0
  fi
  if [[ -n "$current" && "$current" != "$VERSION" ]]; then atomic_text "$PLUGIN_HOME/previous" "$current"; fi
  atomic_text "$PLUGIN_HOME/current" "$VERSION"
  managed_report runtime_installed "Hollis $VERSION is installed. Select bridges next; inference and Apple permissions have not been tested."
}
rollback() {
  platform; acquire setup
  rollback_state
  if [[ "$ROLLBACK_STATUS" != available ]]; then
    managed_report action_required 'Rollback was refused because the retained runtime could not be verified.'
    exit 10
  fi
  regular "$PLUGIN_HOME/current"
  local previous=$ROLLBACK_VERSION current
  current=$(cat "$PLUGIN_HOME/current")
  version_ok "$current" || fail 'Invalid rollback version.'
  atomic_text "$PLUGIN_HOME/current" "$previous"
  atomic_text "$PLUGIN_HOME/previous" "$current"
  managed_report runtime_installed "Restored runtime $previous. Configuration, bridges and conversations were preserved."
}

case "${1:-help}" in
  check)
    platform; read_lock
    safe_path "$PLUGIN_HOME"
    if candidate=$(external_newer); then report existing_newer "Use the newer Hollis at $candidate."
    elif [[ -e "$PLUGIN_HOME/current" || -L "$PLUGIN_HOME/current" ]]; then runtime_path >/dev/null; managed_report runtime_installed 'Runtime verified. Bridge discovery and an explicit first call establish route readiness.'
    else report setup_required 'Install the runtime, select bridges, and enable Apple Intelligence in System Settings.'; fi;;
  install) [[ $# == 1 ]] || fail 'Usage: setup.sh install'; install_runtime;;
  rollback) [[ $# == 1 ]] || fail 'Usage: setup.sh rollback'; rollback;;
  import|status) exec /bin/bash "$PLUGIN_ROOT/scripts/bridges.sh" "$@";;
  path) platform; read_lock; if candidate=$(external_newer); then printf '%s\n' "$candidate"; else runtime_path; fi;;
  *) printf 'Usage: setup.sh check | install | status [cloud|cloud-pro|on-device|chatgpt|image] | import <route> | path | rollback\n';;
esac
