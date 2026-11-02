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

The Responses reply text is at `output[0].content[0].text`. Its `input` may be a string or a message array, with optional `instructions`. `/v1/models` advertises available routes, including `local` when its native helper and Apple model are available. Shortcut tiers depend on their bridges; native local does not.

## Guided JSON output

Both endpoints can ask native `local` for an object that follows a JSON Schema
subset. This is current source, not in the pinned 0.4.0 runtime: it needs the
helper from the same release (protocol 2, helper 0.5.0), and it is not
available on any Shortcut route.

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
- `type:"json_object"` and any other type are refused with **400**
  `unsupported_parameter`. Hollis cannot promise a schema-less JSON object on
  this route, so it refuses instead of pretending.
- Unknown fields inside `response_format`, `json_schema`, `text` or `format` are
  refused with **400** `unsupported_parameter`; the top level already rejects
  unknown fields.

### Limits and refusals

- The schema is parsed and validated before any capacity is taken or helper
  spawned, so an unsupported schema costs no inference. It must fit the same
  subset as `hollis respond --schema`, including the 64 KiB normalized bound.
- Schema rejection is **400** `invalid_schema`, and the message is the CLI's
  sentence with the same JSON pointer, for example
  `schema at /properties/p/pattern: unsupported schema keyword` or
  `schema at /type: schema must not repeat an object key`. Schema values never
  appear in the response beyond the pointer.
- Guided output is complete-only. `stream:true` with a schema is **400**
  `unsupported_parameter`, matching the CLI's refusal to combine `--schema` and
  `--stream`.
- Guided output cannot be combined with image input or image generation;
  that is also **400** `unsupported_parameter`.
- Only `model:"local"` supports it. Any other model, including an omitted model
  (which means `auto`) and an explicit `auto`, is **400** `unsupported_parameter`
  with a message that a schema requires `local`. There is never a silent
  fallback to plain text. If `local` is marked unavailable, the request is
  **400** `model_unavailable`; a server whose runner has no guided-output
  interface answers **503** `model_unavailable` rather than answering in plain
  text.
- Pairing with a helper older than protocol 2 is refused before the request
  spawns and reported as **502** `native_protocol`, the same class as the CLI's
  `native_protocol` / exit 5.
- A returned object that fails Hollis's independent re-validation is **502**
  `native_protocol`; no partial or repaired value is written.
- The usual HTTP limits still apply: an 8 MiB request body and a 128 KiB
  rendered prompt return **413** `context_too_large` before any model work.

## Inline image input

Both endpoints accept PNG/JPEG images as base64 data URLs in the final user message, alongside nonempty text. Earlier messages must remain text-only. Remote URLs and server file paths are rejected.

Chat Completions uses this message content shape:

```json
[{"type":"text","text":"Describe this image"},{"type":"image_url","image_url":{"url":"data:image/png;base64,..."}}]
```

Responses uses this input message content shape:

```json
[{"type":"input_text","text":"Describe this image"},{"type":"input_image","image_url":"data:image/png;base64,..."}]
```

Omitting `model` on an image request selects `cloud`. Explicit `auto`, `on-device` and `local` are rejected. Cloud and Cloud Pro accept up to three images; ChatGPT accepts one. The HTTP request body remains limited to 8 MiB. Decoded images together may occupy at most 4 MiB, with at most 16 million pixels per image and 24 million pixels combined. These are Hollis limits, not reported Apple quotas. Invalid formats return 400 and oversized input returns 413 before a model runs.

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

The API does not accept `tools` or return native function calls. The
underlying Shortcut returns one block of text. Separate live probes show that
the models can sometimes follow a prompt-defined, client-executed tool protocol,
including consuming a supplied tool result, but emitting the call was not
reliable enough to ship. That path remains explicitly experimental work for a
later release; Hollis never executes tools server-side.

## Generate an image through the API

After [image setup](image-generation.md#setup), restart the server and call
`POST /v1/images/generations` with a prompt and style. It returns PNG bytes as
base64. Chat Completions and Responses also support explicit image generation
and replaying a generated image in a follow-up. See the complete
[API image examples](image-references.md#api-image-generation).

## What it deliberately does not do

Shortcuts routes return a complete response, so `stream: true` on those routes returns **400** instead of a faked stream. Only explicit native `local` streams. Apple exposes no token counts through this path, so no `usage` field is invented. The HTTP contract has no `tools` / function calls. `system` and `instructions` are advisory prompt content, not hard isolation boundaries. Both endpoints reject malformed or trailing JSON, unknown fields, unsupported parameters/content, empty input, and prompts over 128 KiB before calling a model. Native context-capacity failures are reported explicitly; history is never silently shortened or retried with fewer messages.

## Authentication and remote access

- Authentication is required by default and is configured with `--token-file <private-file>` or `HOLLIS_API_TOKEN`; the token must contain at least 32 bytes and is never printed.

- `/v1/*` expects `Authorization: Bearer <token>`, while `/health` stays unauthenticated.

- Hollis deliberately has no command-line `--token`, because process arguments can be visible through `ps`.

- `--no-auth` is an explicit opt-out for a resolved loopback listener only; it lets every local process invoke the configured models and cannot be combined with `--allow-remote`.

Binding outside loopback requires **both** `--allow-remote` and authentication. Hollis does not provide TLS: expose it only through an encrypted trusted path such as Tailscale, WireGuard, or an SSH tunnel.

[Back to Hollis](../README.md)
