# Local OpenAI-shaped API

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
