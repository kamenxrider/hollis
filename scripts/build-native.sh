#!/bin/bash
# Build only; never invokes a model or installs into the user's runtime.
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
output="${1:-$root/dist/hollis-native}"
sdk_version="$(xcrun --sdk macosx --show-sdk-version)"
sdk_major="${sdk_version%%.*}"
if [[ "$sdk_major" -lt 27 ]]; then
  echo "Native Hollis requires an Apple macOS SDK version 27 or later (found $sdk_version)." >&2
  exit 1
fi
sdk_path="$(xcrun --sdk macosx --show-sdk-path)"
module_cache="$(mktemp -d "${TMPDIR:-/tmp}/hollis-swift-cache.XXXXXX")"
trap 'rm -rf "$module_cache"' EXIT
mkdir -p "$(dirname "$output")"
xcrun --sdk macosx swiftc -parse-as-library -O \
  -module-cache-path "$module_cache" -sdk "$sdk_path" -target arm64-apple-macosx27.0 \
  "$root/native/HollisNative.swift" "$root/native/SchemaSubset.swift" -o "$output"
codesign --force --sign - "$output"
# Verify without loading FoundationModels or invoking the target executable,
# so build-only hosts with SDK 27 need not run macOS 27.
codesign --verify --strict "$output"
xcrun vtool -show-build "$output"
