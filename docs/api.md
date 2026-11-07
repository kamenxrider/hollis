# OpenAI-compatible API (Chat Completions and Responses)

```bash
umask 077
openssl rand -base64 48 > hollis.token
{ printf 'Authorization: Bearer '; cat hollis.token; } > hollis.headers
hollis serve --token-file hollis.token          # http://127.0.0.1:1978
curl -s localhost:1978/health
curl -s localhost:1978/v1/models -H @hollis.headers
curl -s localhost:1978/v1/chat/completions \
  -H @hollis.headers \
  -H 'Content-Type: application/json' \
  -d '{"model":"cloud-pro","stream":false,"messages":[{"role":"user","content":"Explain closures in Go."}]}'

curl -s localhost:1978/v1/responses \
  -H @hollis.headers \
  -H 'Content-Type: application/json' \
  -d '{"model":"cloud-pro","stream":false,"input":"Explain closures in Go."}'
```

The Responses reply text is at `output[0].content[0].text`. Its `input` may be a string or a message array, with optional `instructions`. `/v1/models` advertises available routes, including `local` when its native helper and Apple model are available. Shortcut tiers depend on their bridges; native local does not. Each route's `capabilities` object includes `max_images`, the most images one request may attach (0 when the route refuses images).

## Model capability names

`/v1/models`, `hollis models --json` and `hollis doctor --json` share the
capability names `backend`, `streaming`, `complete_usage`, `stream_usage`,
`images`, `max_images`, `guided_output` and `tools`. Local reports `tools:true`
only when a successful availability probe advertises executable tools with
turn handoff. For 0.7.0 the API also keeps `usage`, `streaming_usage` and
`image_input` as exact copies of `complete_usage`, `stream_usage` and `images`.

Both Chat Completions and Responses text content parts accept and ignore
`annotations` and `logprobs` only when absent, null or empty arrays. Non-empty
values are refused with 400 `unsupported_parameter`; unknown fields remain
unsupported.

## Guided JSON output

Both endpoints can ask native `local` for an object that follows a JSON Schema
subset. It needs the helper from the same release (protocol 4, helper 0.7.0),
and it is not available on any Shortcut route.

Chat Completions uses the OpenAI `response_format` shape:

```sh
curl -s localhost:1978/v1/chat/completions \
  -H @hollis.headers -H 'Content-Type: application/json' \
  -d '{"model":"local","messages":[{"role":"user","content":"Invent one person"}],
       "response_format":{"type":"json_schema","json_schema":{
         "name":"person",
         "schema":{"type":"object","additionalProperties":false,
                   "properties":{"name":{"type":"string"},"age":{"type":"integer"}},
                   "required":["name"]}}}}'
```

Responses uses `text.format`:

```sh
curl -s localhost:1978/v1/responses \
  -H @hollis.headers -H 'Content-Type: application/json' \
  -d '{"model":"local","input":"Invent one person",
       "text":{"format":{"type":"json_schema","name":"person",
         "schema":{"type":"object","additionalProperties":false,
                   "properties":{"name":{"type":"string"},"age":{"type":"integer"}},
                   "required":["name"]}}}}'
```

The validated object is returned as a JSON **string**: Chat Completions puts it
in `choices[0].message.content`, so `message.content` is
`"{\"name\":\"Ada\",\"age\":36}"` rather than an object. Responses puts the same
string in `output[0].content[0].text` with `"type":"output_text"`. Measured
usage is reported exactly as for a plain complete response. There is no
`refusal` field and no partial object: a failure is an error response, never a
truncated value.

### Field handling

- `name` is required and must be a non-empty string, as upstream requires. It is
  accepted and ignored: it is a label, not a validation input.
- `description` is optional, accepted and ignored for the same reason.
- `strict` is optional and may be `true` or `false`. Both are accepted and
  ignored: Apple's guided generation always enforces the schema, and Hollis
  re-validates the returned object either way. `strict:false` does not buy a
  looser JSON mode, and a non-conforming object is still refused.
- `type:"text"` keeps the existing plain-text behavior.
- Chat Completions also accepts `response_format:{"type":"json_object"}` on
  `model:"local"`. It instructs Local to return one JSON object and independently
  validates the complete text. Invalid output is **502** `invalid_json_output`,
  never a broken or partial object. Like schema output, it is complete-only and
  cannot accompany tools or images. Other routes get **400**
  `unsupported_parameter` naming `local`. Responses still requires a schema.
- Unknown fields inside `response_format`, `json_schema`, `text` or `format` are
  refused with **400** `unsupported_parameter`; the top level already rejects
  unknown fields.

### Limits and refusals

- The schema is parsed and validated before any capacity is taken or helper
  spawned, so an unsupported schema costs no inference. It must fit the same
  subset as `hollis respond --schema`, including the 64 KiB normalized bound.
- Schema rejection is **400** `invalid_schema`. HTTP returns the fixed
  diagnostic reason, such as `unsupported schema keyword`, without the CLI's
  JSON pointer: property names can contain caller content.
- Local usage and tool-loop failures use fixed HTTP messages, not underlying
  error text. Validation errors omit caller tool names and invalid role/type
  values; tool-definition refusals use a fixed message. Statuses and machine
  codes are unchanged.
- Guided output is complete-only. `stream:true` with a schema is **400**
  `unsupported_parameter`, matching the CLI's refusal to combine `--schema` and
  `--stream`.
- Guided output cannot be combined with image input or image generation;
  that is also **400** `unsupported_parameter`.
- Only `model:"local"` supports it. Any other model, including an omitted model
  (which means `auto`) and an explicit `auto`, is **400** `unsupported_parameter`
  with a message that a schema requires `local`. There is never a silent
  fallback to plain text. If `local` is marked unavailable because the helper or
  its Apple model is missing, the request is **400** `model_unavailable`; a
  paired helper that answered with a different protocol is **502**
  `native_protocol` instead, on every local request, guided or plain (see
  [Native local helper mismatch](#native-local-helper-mismatch)). A server whose
  runner has no guided-output interface answers **503** `model_unavailable`
  rather than answering in plain text.
- Pairing with a helper that speaks another protocol is refused before the
  request spawns and reported as **502** `native_protocol`, the same class as the
  CLI's `native_protocol` / exit 5. A request that needs a capability the helper
  did not advertise is **502** `unsupported_capability` instead.
- A returned object that fails Hollis's independent re-validation is **502**
  `native_protocol`; no partial or repaired value is written.
- The usual HTTP limits still apply: an 8 MiB request body and a 128 KiB
  rendered prompt return **413** `context_too_large` before any model work.

## Chat Completions app compatibility

`n:1`, `logprobs:false` or `null`, and absent/null `top_logprobs` are accepted.
Other values are **400** `unsupported_parameter`. A `user` string is accepted
and ignored; it is never retained or logged. Unknown fields remain strict.

With `stream:true`, `stream_options:{"include_usage":true}` adds a final chunk
before `[DONE]` with empty `choices` and measured `usage`. If the helper cannot
provide counts, that chunk has `usage:null`; counts are never estimated.
Absent or false `include_usage` preserves the original stream. Other option
keys, or `stream_options` without `stream:true`, are **400**. Only Local can
stream; Shortcut routes still reject streaming.

## Native local helper mismatch

`local` requests need a paired `hollis-native` helper that speaks the protocol
and version this build expects. At startup `hollis serve` asks the helper for
its status without generating anything. When that answer names a different
protocol or version, the server keeps the classification, and every request
that selects `local` - guided or plain, complete or streamed - is **502**
`native_protocol`:

```json
{"error":{"type":"server_error","code":"native_protocol","message":"the native local helper does not match this Hollis version; install the matching bundle"}}
```

That is the same class as the CLI's `native_protocol` / exit 5, and the remedy
is the same: install the matching bundle. The message names the cause because
the helper answered validly, in another protocol; it is not the shared
`native_protocol` sentence for a malformed or incomplete payload, which keeps
describing that separate failure. The request stops before inference, and no
route falls back to another model or to plain text.

Availability still describes the model. A helper that is missing, not
executable, or whose Apple model is unavailable keeps the availability
contract: **400** `model_unavailable`, the status `/v1/models` reports. Only a
helper that answered and disagreed about its protocol is a mismatch. The same
unavailability discovered after admission, when a request already reached the
helper and it could not run, is **503** `model_unavailable`; it is not a
protocol mismatch and does not fall back either.

## Inline image input

For exactly one inline image, the final user text receives the same fixed
English singular-wording adjustment as the [CLI](cli.md#one-attached-image).
History, system instructions and normal transcript framing are unchanged.
Send `X-Hollis-Keep-Prompt: 1` to preserve the original wording.
An adjusted request returns `X-Hollis-Prompt-Adjusted: true`; successful
non-streaming responses also include the optional top-level
`prompt_adjusted: true`. Streaming discloses through the header without
changing SSE event shapes. No prompt text is included in these headers.
Singular prompts, text-only requests and multiple-image requests are unchanged.

Both endpoints accept PNG/JPEG images as base64 data URLs in the final user message, alongside nonempty text. Earlier messages must remain text-only. Remote URLs and server file paths are rejected.

Chat Completions uses this message content shape:

```json
[{"type":"text","text":"Describe this image"},{"type":"image_url","image_url":{"url":"data:image/png;base64,..."}}]
```

Responses uses this input message content shape:

```json
[{"type":"input_text","text":"Describe this image"},{"type":"input_image","image_url":"data:image/png;base64,..."}]
```

Omitting `model` on an image request selects `cloud`. Explicit `auto` and `on-device` are rejected. Cloud and Cloud Pro accept up to three images; ChatGPT and native `local` accept one. Local requires macOS 27 and a vision-capable model, supports complete responses and SSE on both endpoints, and refuses images combined with tools or guided output. Complete local responses retain measured usage; streamed responses do not invent usage. The HTTP request body remains limited to 8 MiB. Decoded images together may occupy at most 4 MiB, with at most 16 million pixels per image and 24 million pixels combined. These are Hollis limits, not reported Apple quotas. Invalid formats return 400 `invalid_image`. More images than the route accepts return 400 `unsupported_parameter` ("model \"chatgpt\" accepts at most 1 image(s)") on every route. Byte and pixel limits return 413 `context_too_large`. All of these are refused before a model runs.

Hollis validates and stages image bytes privately, holds a concurrency slot through staging and execution, then removes staged files. Responses remain complete text, without streaming or invented usage counts.

Port **1978**, not 1976 — `fm serve` uses 1976 throughout Apple's own examples, and two servers cannot share a port.

Model work is serialized by default. `--max-concurrency` accepts 1–4; work beyond the limit is rejected immediately with HTTP 429 and `Retry-After: 1`. `/health` remains available independently of model capacity. Request headers have 5 seconds, reads 15 seconds, writes 125 seconds, idle connections 60 seconds, and shutdown gets five seconds to finish.

Local capacity rejection has code `server_busy`. Provider throttling has code
`rate_limited` and HTTP 429, for both Shortcuts and native local, but omits
`Retry-After` because the provider reset time is unknown. Do not interpret
that absence as permission to retry immediately. Stop the job and preserve
its state; another attempt requires an explicit decision, not an automatic
retry or model swap. Disable SDK/client automatic retries when configuring
Hollis calls, since removing the header does not disable a client's own policy.
After native SSE starts, a provider limit is a stream failure, not a new HTTP
429 or a successful completion.

Shortcuts failures distinguish `rate_limited` (HTTP 429, no `Retry-After`),
`content_refused` (HTTP 422) and `access_required` (HTTP 403). They never retry
or swap models automatically. Code 1508 means a device limit that may last
hours; 900 is a refusal, not a limit; 1511 means Cloud Pro account access is
required. Usage-limit text on stdout with exit 0 is an error, never an answer.
After a failed run only, a five-second, 1 MiB bounded macOS unified-log check
can classify the run; overlapping/ambiguous runs or unavailable logs fall back
to text. `error.message` preserves bounded sanitized Apple text, and optional
`error.details` includes `apple_stderr`, `apple_stdout` and
`shortcut_failure` (domain/code/known keys only). Prompt text, paths,
credentials and raw log messages are not included. Unknown failures remain
HTTP 502 `shortcut_failed`. See [failures](errors.md).

## Clients: listing is not the same as working

Point an OpenAI-compatible client at `http://127.0.0.1:1978/v1` and it will usually **see** the models. That does not mean it can **call** them.

For `cloud`, `cloud-pro`, `on-device`, `chatgpt` and `auto`, set `stream: false`
in the **JSON body**. These routes cannot stream. For native `local`, either
complete responses or `stream: true` are supported. A custom HTTP header does
not select streaming. Model discovery describes capabilities; it is not proof
that every client supports the chosen request and response format.

```sh
curl -N localhost:1978/v1/chat/completions \
  -H @hollis.headers -H 'Content-Type: application/json' \
  -d '{"model":"local","stream":true,"messages":[{"role":"user","content":"Explain closures briefly."}]}'
curl -N localhost:1978/v1/responses \
  -H @hollis.headers -H 'Content-Type: application/json' \
  -d '{"model":"local","stream":true,"input":"Explain closures briefly."}'
```

Native complete responses carry measured token usage in the endpoint's usage
fields. Streamed usage is not promised. Stream events are flushed incrementally;
errors after streaming starts are reported as stream errors, never a fabricated
successful completion. Disconnecting cancels the local helper.

Tools are available on native `local` only; see [Tools on `local`](#tools-on-local).
Shortcuts routes refuse `tools` with **400** `unsupported_parameter` before the
tool definitions are checked, so the refusal names the route rather than a
definition problem. Hollis never executes tools server-side.

## Tools on `local`

Both endpoints accept caller tools on `model:"local"`, `tool_choice` and
`parallel_tool_calls` absent or true. The server never executes tools or adds
tools of its own. Run returned calls yourself and replay their results.

Chat Completions definitions use
`{"type":"function","function":{"name":"get_weather","description":"Get weather","parameters":{...},"strict":true}}`.
Responses definitions use
`{"type":"function","name":"get_weather","description":"Get weather","parameters":{...},"strict":true}`.
`strict` may be omitted, true or false. The supported
[guided-output subset](native-local.md#guided-output) is always validated.
There are at most 32 caller tools and 16 calls per batch. Names are 1 to 64
letters, digits, underscores or hyphens. Descriptions must be nonempty.

`tool_choice` accepts `"auto"` (default), `"none"` and `"required"`.
Named Chat Completions choice is
`{"type":"function","function":{"name":"get_weather"}}`.
Named Responses choice is `{"type":"function","name":"get_weather"}`.
The exact name must be offered. Required forces at least one real call, then
allows dependencies. Named permits only the selected tool initially and disables
callbacks after its result. Structured history satisfies the obligation for that
user transaction; a new user turn resets it. No synthetic exit function is added.
An unoffered named function returns **400** `unsupported_parameter` before
inference with the fixed message `tool_choice function was not offered`;
HTTP does not echo function names.

The helper uses executable Swift tools, not text extraction. Callbacks capture
one complete native batch and return it to Go for independent validation.
Framework IDs and argument bytes are preserved. Invalid arguments are **502**
`tool_call_invalid` with no automatic repair or coercion. Call-shaped text,
including quoted JSON or CALL text, remains ordinary content. Invalid names,
partial batches and callback/choice violations fail closed.

One native tool decision can retry once after 500 ms for an in-process
`ModelManagerError` with code 1012 or an `NSError` with exact domain
`FoundationModels.LanguageModelError` and code -1, before any callback is
captured or executed in that decision. Both errors share one retry, preserving
the exact input and original deadline. A later synthesis decision can retry
after earlier tools completed; no tool execution is retried. Other
`LanguageModelError` codes, guardrail, context-size and decoding errors never
retry. A repeated allowlisted failure returns **503** `model_busy` (CLI exit 5).
Measured usage is omitted if the failed attempt supplied no counts.

Chat Completions returns `finish_reason:"tool_calls"` with an assistant
`tool_calls` array and null content. Each call has `id`, `type:"function"` and
`function` containing `name` and string `arguments`. Replay that assistant message
followed by `role:"tool"` messages with matching `tool_call_id` and string
content. Responses returns `function_call` items with `call_id`, `name` and string
`arguments`; replay `function_call_output` with the same `call_id` and string
`output`. Empty results are valid, but missing or null results are not.
Duplicate, orphan or mismatched IDs fail before inference, and so does a call
left unanswered before the next message or at the end of the history.
Responses output items can be replayed as returned: message items may keep
`type:"message"`, `id` and `status`, and text parts may keep empty
`annotations` and `logprobs`. Non-empty annotations or logprobs are refused.

The default budget is eight decisions, including final synthesis and visible
prior call batches since the most recent user turn. A batch on the final
decision fails before dispatch with **502** `tool_round_cap`. Stateless replay
is not a tamper-proof global quota.

Results are untrusted data. Full typed history, fixed instructions and
definitions share 128 KiB, plus the SDK context-capacity check. Oversize is
**413** `context_too_large`, without truncation. Native handoff usage is absent
unless all decisions have qualified measured counts. One capacity slot and one
deadline cover the whole loop.

Tools refuse `stream:true`, images, image generation, guided output,
`parallel_tool_calls:false` and non-Local routes with **400**
`unsupported_parameter`. Helper 0.7.0/protocol 4 must advertise both
`tools.executable` and `tools.turn_handoff`. Metadata-only support does not qualify.

## Local sampling

Both endpoints accept optional `temperature`, `top_p` and UInt64 `seed`.
Chat Completions accepts positive integral `max_tokens`; Responses uses
`max_output_tokens`. Omitted fields retain SDK defaults. Temperature is finite
in 0..2; zero selects greedy. Top-p is in (0,1]. Seed alone selects top-p 1.
Greedy with seed or top-p is rejected. Token limits apply per model decision,
not across a whole tool loop.

Hollis passes `max_tokens` and `max_output_tokens` unchanged to Apple's
`GenerationOptions.maximumResponseTokens`. The limit is approximate.
Reported completion usage can exceed the requested value slightly.
Hollis does not truncate output or rewrite measured usage to enforce an exact cap.
The SDK exposes no stop/finish reason. When measured output tokens are at or
above an explicit limit, Hollis reports Chat Completions `finish_reason:"length"`
or Responses `status:"incomplete"` with
`incomplete_details:{"reason":"max_output_tokens"}`, including the final stream
chunk or `response.incomplete` event. This is a best-effort signal: Apple's
approximate limit can overshoot the requested value, so a naturally finished
answer at the boundary can also be marked incomplete. Short answers well below
the limit remain `stop` / `completed`. Public streaming usage stays absent.

Sampling applies to Local text, streaming, guided output and tool generation.
Other routes and image generation reject sampling before model access.
Invalid types, out-of-range values and conflicting options are **400**
`unsupported_parameter`. Reproducibility across OS/model versions is not promised.
## Generate an image through the API

After [image setup](image-generation.md#setup), restart the server and call
`POST /v1/images/generations` with a prompt and style. It returns PNG bytes as
base64. Chat Completions and Responses also support explicit image generation
and replaying a generated image in a follow-up. See the complete
[API image examples](image-references.md#api-image-generation).

## What it deliberately does not do

Shortcuts routes return a complete response, so `stream: true` on those routes returns **400** instead of a faked stream. Only explicit native `local` streams. Apple exposes no token counts through this path, so no `usage` field is invented. Tools are caller-provided, `local` only, and never executed by Hollis. `system` and `instructions` are advisory prompt content, not hard isolation boundaries. Both endpoints reject malformed or trailing JSON, unknown fields, unsupported parameters/content, empty input, and prompts over 128 KiB before calling a model. Native context-capacity failures are reported explicitly; history is never silently shortened or retried with fewer messages.

## Authentication and remote access

- Authentication is required by default and is configured with `--token-file <private-file>` or `HOLLIS_API_TOKEN`; the token must contain at least 32 bytes and is never printed.

- `/v1/*` expects `Authorization: Bearer <token>`, while `/health` stays unauthenticated.

- Hollis deliberately has no command-line `--token`, because process arguments can be visible through `ps`.

- `--no-auth` is an explicit opt-out for a resolved loopback listener only; it lets every local process invoke the configured models and cannot be combined with `--allow-remote`. Unauthenticated requests require Host `localhost`, `127.0.0.1` or `[::1]` (optionally with a port), and any Origin must also be loopback. Foreign hosts/origins get 403 to resist DNS rebinding. Prefer token authentication.

Binding outside loopback requires **both** `--allow-remote` and authentication. Hollis does not provide TLS: expose it only through an encrypted trusted path such as Tailscale, WireGuard, or an SSH tunnel.

## Run it in the background

`hollis serve` stays in the foreground. To start it at login with a private
token file and a loopback bind, use the LaunchAgent template described in
[running the local API in the background](serve.md).

[Back to Hollis](../README.md)
