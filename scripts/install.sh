#!/bin/bash
# Install the published Apple Silicon bundle. Makes no model calls.
set -euo pipefail
umask 077
version=0.4.0
source_commit=1906721a1cd5594ce47be6d4d306d7daa1e6891f
repository=kamenxrider/hollis
bin_dir="${HOME}/.local/bin"
if [[ ${1:-} == --bin-dir && $# == 2 ]]; then
  bin_dir=$2
elif [[ $# != 0 ]]; then
  echo 'Usage: bash install.sh [--bin-dir /absolute/directory]' >&2
  exit 2
fi
[[ $bin_dir == /* ]] || { echo 'Choose an absolute installation directory.' >&2; exit 2; }
[[ $(uname -s) == Darwin && $(uname -m) == arm64 ]] || { echo 'Hollis inference requires an Apple silicon Mac.' >&2; exit 1; }
os_version=$(sw_vers -productVersion)
[[ ${os_version%%.*} -ge 27 ]] || { echo 'This bundle requires macOS 27 or later.' >&2; exit 1; }
for command in gh shasum unzip shortcuts codesign plutil install; do
  command -v "$command" >/dev/null || { echo "Required command missing: $command" >&2; exit 1; }
done
stage=$(mktemp -d "${TMPDIR:-/tmp}/hollis-install.XXXXXX")
trap 'rm -rf "$stage"' EXIT
asset="hollis-$version-darwin-arm64.zip"
gh release download "v$version" --repo "$repository" --dir "$stage" \
  --pattern "$asset" --pattern SHA256SUMS
(
  cd "$stage"
  awk -v asset="$asset" '$2 == asset { n++; print } END { if (n != 1) exit 1 }' \
    SHA256SUMS > SELECTED_SHA256SUMS
  shasum -a 256 -c SELECTED_SHA256SUMS
)
gh attestation verify "$stage/$asset" --repo "$repository" \
  --signer-workflow "$repository/.github/workflows/release.yml" \
  --source-ref "refs/tags/v$version" --source-digest "$source_commit" \
  --deny-self-hosted-runners
# Extract only after checksum and provenance verification.
unzip -q "$stage/$asset" -d "$stage/runtime"
codesign --verify --strict "$stage/runtime/hollis-native"
[[ $("$stage/runtime/hollis" --version) == "hollis $version" ]] || { echo "Runtime version mismatch." >&2; exit 1; }
[[ $("$stage/runtime/hollis-native" --version) == "hollis-native $version" ]] || { echo "Native helper version mismatch." >&2; exit 1; }
[[ $("$stage/runtime/hollis-native" --protocol-version) == 1 ]] || { echo "Native helper protocol mismatch." >&2; exit 1; }
unzip -q "$stage/runtime/hollis-bridges.zip" -d "$stage/bridges"
mkdir "$stage/signed"
for bridge in "$stage/bridges/"*.shortcut; do
  shortcuts sign --mode anyone --input "$bridge" --output "$stage/signed/${bridge##*/}"
done
# Do not follow a destination symlink to another executable.
[[ ! -L $bin_dir ]] || { echo 'Installation directory must not be a symlink.' >&2; exit 1; }
mkdir -p "$bin_dir"
for name in hollis hollis-native; do
  [[ ! -L $bin_dir/$name && ( ! -e $bin_dir/$name || -f $bin_dir/$name ) ]] || { echo "Unsafe destination: $name" >&2; exit 1; }
done
install -m 755 "$stage/runtime/hollis" "$stage/runtime/hollis-native" "$bin_dir/"
# Keep imports available while Shortcuts opens its asynchronous Add dialogs.
state_dir="${HOLLIS_STATE_DIR:-$HOME/Library/Application Support/hollis}"
imports="$state_dir/bridge-imports/$version"
mkdir -p "$imports"
for bridge in "$stage/signed/"*.shortcut; do
  [[ ! -L $imports/${bridge##*/} ]] || { echo 'Unsafe bridge import destination.' >&2; exit 1; }
  install -m 600 "$bridge" "$imports/${bridge##*/}"
done
"$bin_dir/hollis" config show --json > "$stage/config.json"
image_bridge=$(plutil -extract image_bridge raw -o - "$stage/config.json")
if [[ -z $image_bridge ]]; then
  "$bin_dir/hollis" config set image-bridge 'Hollis Image - Reference Input v2'
fi
for bridge in "$stage/signed/"*.shortcut; do open "$imports/${bridge##*/}"; done
printf '\nHollis %s installed in %s.\n' "$version" "$bin_dir"
printf 'Choose Add Shortcut for each of the five imports. Then run hollis doctor.\n'
case ":$PATH:" in
  *":$bin_dir:"*) ;;
  *) printf 'Add this directory to your shell PATH: %s\n' "$bin_dir" ;;
esac
