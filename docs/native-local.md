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
local supports text, including TXT/MD and extracted PDF document preparation.
Native image input requires the helper's advertised vision capability, never
just the model name; native image generation is not provided. Caller-provided
tools are covered under [Tools](#tools).

## Guided output

This section describes Hollis 0.7.0, with helper 0.7.0/protocol 4. Runtime
0.4.0 and plugin 0.2.0's runtime use protocol 1 and do not include `--schema`
or the protocol-2 and protocol-3 diagnostics described here. Use the CLI and
helper from the same release bundle; do not mix a 0.7.0 CLI with an older
helper.

Protocol 3 adds a `capabilities` object to the helper's status event, and every
operation negotiates before it runs. The helper accepts request formats 1, 2, 3 and
4, but stamps all events with version 0.7.0. Older CLIs reject that version;
accepting their request format does not make them compatible. This CLI speaks
protocol 4 only and checks strict version identity because the CLI and helper
ship as one bundle. A mismatched pair fails closed with a clear error asking
for the matching bundle, never a plain-text downgrade. A capability the helper
did not advertise (tool calling on an older build, for example) is refused by name as
`unsupported_capability`, which
is a component fault at exit 5 and HTTP 502, distinct from a caller mistake.

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

- the root is an object, and every object is closed: `"additionalProperties"`
  may be `false` or omitted, and omitted means `false`, so a returned object
  with an undeclared member is refused either way. `true` or a schema value is
  rejected. Nesting stops at three object levels, with arrays adding no object
  level. On top of that, at most sixteen schema nodes fit on one path, root
  included, so an array chain (`items` inside `items`) cannot nest without
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
- annotations are accepted on any schema node and change nothing about
  validation: `description`, `title`, `$schema`, `$id`, `$comment`, `default`,
  `examples`, `deprecated`, `readOnly`, `writeOnly` and any `x-` key.
  `description` (or `title` when there is no description) is passed to the
  on-device model as the meaning of that object or property; the others are
  not sent to the helper. **A `default` is never applied**: a property the
  model leaves out stays out, and a required one is still missing;
- string constraints are accepted on `string` properties and **not enforced**:
  `format`, `minLength`, `maxLength`, `pattern`, `contentEncoding` and
  `contentMediaType`. Hollis checks that the value is a string (and in the
  `enum`, when there is one); it does not check length, pattern or format.
  Their values must still be well formed (strings, or whole non-negative
  lengths);
- a property may be a nullable union: `anyOf` or `oneOf` whose branches are one
  supported schema plus `{"type": "null"}`. It is treated as that one schema,
  the property becomes optional even when `required` names it, and `null` is
  accepted for it. The union stands alone on the property, apart from
  annotations; a union of two non-null types, a union at the root or in
  `items`, and a union next to `type` are rejected;
- rejections include references, `allOf`/`not`, other uses of
  `anyOf`/`oneOf`, `const`, string constraints on non-string nodes,
  integer enums and contradictions such as `maxItems < minItems`. The 64 KiB
  cap applies to the whole normalized schema, annotations included, measured
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

The annotation, string-constraint, omitted-`additionalProperties` and
nullable-union rules are a compatible relaxation: every schema accepted before
them is still accepted, sent to the helper as the same bytes, and validated
identically.

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

Guided output requires helper 0.7.0/protocol 4 matched to the CLI. The helper is
asked which protocol it speaks before any operation spawns, so an older helper
costs no inference: it is refused, pointing at the matching bundle.
Pairing the CLI with any other helper release is a
mismatch for every operation, guided or not; the refusal names the cause instead
of degrading to plain text. The HTTP server carries the same rule: a startup
status probe that names another protocol or version is reported as 502
`native_protocol` for every `local` request, guided or plain, with a message
saying the helper does not match this Hollis version and naming the matching
bundle. A missing or unavailable helper stays 400 `model_unavailable`, and a
malformed payload from a matched helper keeps the shared `native_protocol`
sentence. Nothing reaches inference either way; see
[the API guide](api.md#native-local-helper-mismatch).

## Install the matching helper

<!-- updated at release tagging -->
The commands below download, verify and unpack one release bundle.

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

With protocol 2 and above, native availability results also expose measured `context_size`
when available. On 2026-11-01, the checked macOS 27.2 build reported 8,192 tokens;
this is an observation, not a fixed capacity guarantee. The 128 KiB prompt-byte
limit remains separate. See the [dated qualification summary](../EVIDENCE.md#dated-qualification-summary).

## API clients

Select `"model":"local"` and `"stream":true` in either Chat Completions or Responses.
The endpoints use their corresponding server-sent event formats. An interrupted
or failed stream does not become a successful completion. Other routes still
require `stream:false`. See [the API guide](api.md).

[Back to Hollis](../README.md)

## Tools

`hollis respond --model local --tools tools.json "..."` offers executable native
tools. The file uses Chat Completions' function-definition shape. Caller tools
are never executed by Hollis. The command prints one JSON document with either
`finish_reason:"tool_calls"` and validated calls, or `finish_reason:"stop"` and
final text. Native framework call IDs are preserved.

Tool choice accepts `auto`, `none`, `required` and `function:NAME`. Required means
at least one real offered function; its continuation allows dependencies.
Named choice offers only that function initially and disables further callbacks
after its result. A replayed structured call satisfies the obligation for the
current user transaction. A new user message resets it. None disables callbacks;
call-shaped prose is ordinary text and is never executed.

The helper registers real Swift tools. Their callbacks only capture a complete
batch and return control to Go. Go validates every call before approval or
execution. There is no CALL/JSON extraction, argument coercion, automatic repair
or Local text fallback. Unsupported capability, invalid arguments and incomplete
batches fail explicitly. Quoted calls in documents, results or assistant prose
are not dispatchable.

A native tool decision retries at most once after 500 ms for
`ModelManagerError` code 1012, only before any callback is captured or executed
in that decision. The retry uses the same input and deadline, never re-executes
a tool, and does not apply to guardrail, context-size or decoding errors.
A repeated busy failure returns `model_busy`, CLI exit 5 or HTTP 503.
Usage is absent when the failed attempt supplied no measured counts.

The default budget is eight model decisions, including visible prior tool
batches since the last user turn and final synthesis. A call on the final
decision fails before dispatch with `tool_round_cap` (exit 5). A batch has at
most 16 calls, with at most 32 caller tools and 128 combined caller/MCP offers.
Invalid native arguments fail `tool_call_invalid` (exit 5) without a repair.
`rounds` counts only new decisions in the current request.

Full typed history, framing and definitions share a 128 KiB byte budget.
SDK token counting checks the native transcript before inference and after
each completed MCP result, before any later approval or dispatch.
Nothing is shortened or dropped. Handoff usage is absent unless measured for
every decision. Tool schemas use the [guided-output subset](#guided-output).

`--tools` requires Local and is complete-only. It refuses `--stream`, `--schema`,
`--image`, `--code`, `--json` and `--agent`. CLI MCP tools use the existing
approval manager, exact `server__tool` names and sequential execution.
Ask-only server-validated schemas use a native JSON-string envelope and still
require human approval and server validation. The HTTP server executes no MCP
or caller tools. See [MCP servers](mcp.md).

## Sampling

Local `respond` accepts `--temperature`, `--top-p`, `--max-tokens` and `--seed`.
Omission retains SDK defaults. Temperature is finite in 0..2; zero selects
greedy. Top-p is in (0,1]. Seed is UInt64; seed alone selects top-p 1.
Greedy with seed or top-p is a usage error. Max tokens is a positive integer
per model decision, not a cumulative tool-loop allowance.

`--max-tokens`, HTTP `max_tokens` and `max_output_tokens` pass unchanged to
Apple's `GenerationOptions.maximumResponseTokens`. This is an approximate
limit, not an exact output-token cap. Reported completion usage can exceed
the requested value slightly. Hollis does not truncate the response or
rewrite measured usage to enforce an exact cap.

HTTP uses `temperature`, `top_p`, `seed` and `max_tokens` for Chat Completions,
or `max_output_tokens` for Responses. Sampling is Local-only and cannot accompany
image generation. Text, streaming, guided output and tool generation share the
same validated options. Reproducibility across OS/model versions is not promised.
