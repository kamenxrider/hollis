# Native local: your Mac has more to say

Hollis 0.4.0 adds `local`, an explicit route to Apple's on-device Foundation
Models SDK. It needs Apple Silicon, macOS 27, Apple Intelligence enabled and
Apple's model available. It does not require a Shortcuts bridge. The existing
`on-device` selection still uses Shortcuts; `auto` does not select native local.

## Use it

```sh
hollis respond --model local "Explain this error."
hollis chat --model local
hollis respond --model local --stream=false "Return a short answer."
hollis respond --model local --agent "Return a short answer."
```

Human output streams when stdout is a terminal. `--stream` explicitly enables
streaming to a pipe; `--stream=false` requests a complete answer. JSON and agent
output never stream, even in a terminal. Combining them with `--stream=true` is
an error before generation.

Streamed terminal text displays control characters safely rather than letting
them move the cursor, change the clipboard or execute terminal escape sequences.
This presentation does not rewrite the raw response saved in successful chats
or encoded in API/JSON output.

The complete rendered conversation is submitted each turn. Hollis does not
silently shorten, summarise or drop history to fit the model. Its existing
128 KiB prompt limit is a byte safeguard, not the model's context capacity.
Requests that exceed either supported input limits or Apple's context capacity
fail clearly. Start a new conversation or explicitly provide a shorter request.
Failed or interrupted assistant turns are not saved as successful answers.

Complete native responses include Apple's measured token usage. Streamed usage
is unavailable; Hollis does not estimate it or report invented zeros. Native
local supports text, including the existing TXT/MD document preparation. It does
not add image input, image generation or tool/function calls to this route.

## Install the matching helper

The Apple Silicon release bundle contains `hollis`, `hollis-native`, the existing
bridge archive, a file-hash manifest and the licence. The native helper is
precompiled; end users do not need Xcode or a developer certificate to run it.
Follow the existing platform consent prompts when applicable.

Download and verify the bundle from the same pinned runtime release before
extracting it. This requires the GitHub CLI for provenance verification:

```sh
(
set -eu
umask 077
HOLLIS_BUNDLE=hollis-0.4.0-darwin-arm64.zip
HOLLIS_STAGE="$(mktemp -d)"
cd "$HOLLIS_STAGE"
gh release download v0.4.0 --repo kamenxrider/hollis \
  --pattern "$HOLLIS_BUNDLE" --pattern SHA256SUMS
awk -v asset="$HOLLIS_BUNDLE" '$2 == asset { count++; print } END { if (count != 1) exit 1 }' \
  SHA256SUMS > SELECTED_SHA256SUMS
shasum -a 256 -c SELECTED_SHA256SUMS
gh attestation verify "$HOLLIS_BUNDLE" --repo kamenxrider/hollis
unzip "$HOLLIS_BUNDLE" -d runtime
printf 'Verified runtime directory: %s/runtime\n' "$HOLLIS_STAGE"
)
```

Install `hollis` and `hollis-native` together in the same directory. Do not mix
helper versions or take a helper from an arbitrary PATH location. Existing raw
CLI downloads remain available, but native local also needs the matching helper.
The plugin manages the matched pair, verifies both and retains verified rollback
material. Updating it preserves configuration, bridges and conversations.

`hollis models --json` and `hollis doctor --json` describe availability. A successful
availability check is not proof of successful inference or first-use consent.
Missing Shortcuts do not prevent selecting `local`.

## API clients

Select `"model":"local"` and `"stream":true` in either Chat Completions or Responses.
The endpoints use their corresponding server-sent event formats. An interrupted
or failed stream does not become a successful completion. Other routes still
require `stream:false`. See [the API guide](api.md).

[Back to Hollis](../README.md)
