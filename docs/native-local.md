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

## Guided output

This section describes current source, checked at commit `8a2e4bd` on
2026-11-01 with helper 0.5.0/protocol 2. Runtime 0.4.0 and plugin 0.2.0's pinned
runtime use protocol 1 and do not include `--schema` or protocol-2 context
diagnostics. Build the CLI and helper from the same source revision for these
features; do not mix the current CLI with a released 0.4.0 helper.

`--schema` asks the native model for an object that follows a supported subset
of JSON Schema. The schema is validated before the helper is spawned, so an
unsupported schema costs no inference:

```sh
hollis respond --model local --schema person.json "Invent one person"
hollis respond --model local --schema person.json --json "Invent one person"
```

With `--json`, `response` is the decoded object rather than a string. Human
output prints the same object, indented. Usage keeps its existing three counts;
no field is added.

The subset is deliberately small, and anything outside it is rejected with the
offending JSON pointer instead of being approximated:

- the root is an object with `"additionalProperties": false`, as is every
  nested object; nesting stops at three object levels, with arrays adding none
  object level. On top of that, at most sixteen schema nodes fit on one path,
  root included, so an array chain (`items` inside `items`) cannot nest without
  bound;
- properties are `string`, `integer`, `number` or `boolean`; `required` names
  declared properties; at most 32 properties are declared across the schema;
- property names, `required` names and `enum` choices are printable ASCII.
  Foundation compares strings by Unicode canonical equivalence, so a
  precomposed `é` and a decomposed `e` + combining acute are one name there and
  two byte-distinct names here; that alone would make the same schema mean
  different things on each side. With ASCII-only names and choices the two
  comparisons agree exactly, and a model cannot answer with a byte-different
  but canonically equal value;
- `enum` accepts string choices only (Apple's dynamic schema accepts no other
  kind), and choices must be non-empty and unique;
- arrays declare `items`, with `minItems`/`maxItems` between 0 and 100;
- `minimum`/`maximum` are integral for integers and finite for numbers;
- rejections include references, `anyOf`/`oneOf`/`allOf`/`not`, patterns,
  formats, string bounds, defaults, descriptions and contradictions such as
  `maxItems < minItems`. The normalized schema is capped at 64 KiB, measured
  the way the helper measures it: sorted keys, `/` escaped, 17 significant
  digits.
- an object that repeats a key is rejected rather than resolved. Foundation
  keeps the first value of a duplicate key and Go keeps the last, so an
  ambiguous schema would be measured and enforced differently on each side.
  The repeated key is found after JSON escape decoding, so `"a/b"` and
  `"a\/b"`, or `"a"` and `"\u0061"`, are the same key.
- only strict JSON is accepted: a leading byte-order mark, a trailing comma in
  an object or array, or a second top-level value is refused as
  "schema is not valid JSON". Foundation's decoder is more permissive than
  Go's, so both sides run the same strictness check before decoding.

Every rule in that list is enforced by Hollis before the helper is spawned, and
by the helper's own parser as well. A schema Hollis accepts is therefore a
schema the helper accepts, and a rejection is a usage error (exit 2) naming the
offending JSON pointer with no inference and no process spawned.

The helper reaches that agreement by scanning the raw request bytes before
Foundation's decoder sees them: the request envelope is checked for strict JSON
and repeated keys, and the embedded `schema` member is scanned again as a schema
so its rejections carry the same pointer and sentence Hollis would report. The
scan compares keys by decoded UTF-8, which is what the Go decoder does. Without
that scan the helper's decoder would accept a byte-order mark or a trailing
comma and keep the first value of a repeated key.

One bound is the helper's own rather than a shared rule: Foundation's
`JSONDecoder` fails at roughly 512 levels of nesting. That is far above the
16-node nesting bound, so a schema deep enough to reach it is rejected by
Hollis first as a usage error and never reaches the helper.

Guided output is complete-only: `--schema` refuses `--stream`, `--image` and
every route except `local` before generation. The helper scans the SDK's JSON
text for the returned object, refuses it if it is not strict JSON or repeats a
key, validates the decoded value against the requested subset, and only then
emits the object, re-encoded from a dictionary, so the emitted bytes cannot
repeat a key. Hollis re-validates that emitted object independently, and the Go
check is the authority for the returned payload: the helper's echo has already
lost any duplicate by construction, so a payload fault can only be caught on
the text the SDK produced. A mismatch, a refusal, a timeout or a
context-capacity failure reports the existing error and never a partial object.
A helper that predates guided output
is refused before the request spawns, with a message to install the matching
bundle; that refusal is a component mismatch, so it reports `native_protocol`
with exit 5, the same class as a helper that speaks too new a protocol.

Structural validity is all this feature establishes. A conforming object is not
a claim that its content is correct.

The same guided-output path is reachable over HTTP: `/v1/chat/completions`
accepts `response_format` and `/v1/responses` accepts `text.format`, both with
OpenAI's `json_schema` shape. The HTTP surface validates the schema before
capacity or spawn, refuses every route but `local`, refuses `stream:true` and
image input alongside a schema, re-validates the returned object, and returns
the object as a JSON string. An old helper maps to HTTP 502 `native_protocol`,
the same class as the CLI's exit 5. Field handling, error codes and examples are
in [the API guide](api.md#guided-json-output).

Guided output requires helper 0.5.0/protocol 2 matched to the CLI. The helper is
asked which protocol it speaks before a schema request spawns, so an older helper
costs no inference: it is refused, pointing at the matching bundle.
Pairing the CLI with any other helper release is a
mismatch for every operation, guided or not; the refusal names the cause instead
of degrading to plain text.

## Install the matching helper

The commands below install released 0.4.0 native text/streaming support, not the
current-source guided-output and context-diagnostic additions described above.

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

With protocol 2, native availability results also expose measured `context_size`
when available. On 2026-11-01, the checked macOS 27.2 build reported 8,192 tokens;
this is an observation, not a fixed capacity guarantee. The 128 KiB prompt-byte
limit remains separate. See the [dated qualification summary](../EVIDENCE.md#dated-qualification-summary).

## API clients

Select `"model":"local"` and `"stream":true` in either Chat Completions or Responses.
The endpoints use their corresponding server-sent event formats. An interrupted
or failed stream does not become a successful completion. Other routes still
require `stream:false`. See [the API guide](api.md).

[Back to Hollis](../README.md)
