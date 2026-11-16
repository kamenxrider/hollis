# Hollis workflows

All examples below are arguments to `bash "$KIT/scripts/run.sh"`; replace paths with actual absolute paths. Discover the complete flags using `agent-context`. Do not copy illustrative filenames into a live request without checking them.

## Documents and pictures

- Text documents: `respond --agent --model cloud --prompt-file instructions.txt --file brief.md --file notes.txt`. Regular UTF-8 text documents of any extension are accepted. When `agent-context` describes PDF support on `--file`, PDFs are extracted on the Mac by the native helper; `--pages` selects pages from one PDF. Cloud routes receive the extracted text, not the PDF. Instruction plus documents must fit 128 KiB. Documents cannot be mixed with images in one call. When `agent-context` describes `--prompt-file -`, the instruction can come from stdin instead: pipe it to `respond --agent --model cloud --prompt-file - --file brief.md`. Use exactly one instruction source; positional text plus piped stdin with `--file` or `--image` is a usage error, not a merge.
- Image understanding: `respond --agent --model cloud-pro --image screenshot.png --prompt-file question.txt --timeout 120s`. Cloud and Cloud Pro accept repeated PNG/JPEG images; ChatGPT accepts one. Native `local` accepts one when `models --model local --json` reports image capability. On-Device and auto are not supported for this input.
- Current CLI image bounds are 64 MiB / 64 million pixels per input. Use real regular files; the runtime rejects arbitrary symlinks. Do not bypass its checks. HTTP limits are different and do not apply to these CLI examples.

## Generate and revise

`image generate 'A tiny greenhouse on a brass turtle, detailed illustration' --style illustration --output /absolute/new-image.png --agent --timeout 120s`

The five supported styles are `animation`, `illustration`, `sketch`, `genmoji`, and `any`. Default to `animation` when the user has no style preference, matching Hollis. `any` does not guarantee photographs. ChatGPT **text and image understanding** work; ChatGPT **image generation** is unavailable through the tested Shortcut route.

For a follow-up: keep the previous returned local file path, then call `image generate` with the requested change and `--reference-image /absolute/previous.png`. This submits the previous image; reliable use of its pixels remains unproven, including subject preservation. Keep relevant subject and scene context in the prompt, and assess the returned result rather than treating successful attachment delivery as proof of an edit. Use a new output filename each time. An explicit fresh-scene request omits the reference.

Ratios and sizes are local processing after generation: `--aspect-ratio 16:9 --fit crop`, or `--size 1200x800 --fit pad`. Preserve native output when neither is requested. If the user requests a ratio without specifying a fit, explain crop versus padding and ask which preserves their intent. These flags do not control Apple's native sampling. No seed control is exposed.

For an explicitly persistent conversation, use `chat --agent --model cloud` initially and retain its returned ID. Image turns use `chat --continue <id> --generate-image --image-style illustration --output <new.png> <prompt>`, with `--image-reference auto|none|<path>` as appropriate. When the installed `agent-context` lists `--image` on `chat`, a one-shot chat turn can attach an image, but its pixels are not replayed on later turns. Otherwise use explicit `respond` calls with selected context instead.

## Guided output (native local only)

Offer this only for the on-device model. Check support first: `agent-context` lists `--schema` on `respond`, and `models --model local --json` reports `available: true` with `capabilities.guided_output: true`. An older runtime such as 0.4.0 cannot produce guided output; say so instead of degrading to plain text. Never use `--schema` to stand in for a requested Cloud/Cloud Pro tier: if the user asked for that tier, answer with it or explain the failure of that request.

Only after that check passes (`agent-context` lists `--schema` on `respond` and the model is available), write the schema to a private file, keep it for the call, and delete it afterwards. A `mktemp` file is the simplest choice:

```sh
schema=$(mktemp "${TMPDIR:-/tmp}/hollis-schema.XXXXXX")
# write the subset schema to "$schema"
bash "$KIT/scripts/run.sh" respond --agent --model local --schema "$schema" "Invent one person"
bash "$KIT/scripts/run.sh" respond --json --model local --schema "$schema" "Invent one person"
rm -f "$schema"
```

With `--json`, `response` is the decoded object; without it the object prints indented. Guided output is complete-only: `--stream` and `--image` are usage errors, and every non-local route rejects `--schema` before generation.

The accepted subset, matching the 0.7.1 runtime's own checks: an object root and closed objects (`additionalProperties` `false` or omitted); `string`, `integer`, `number` and `boolean` properties; `required` naming declared properties; string-only enums (non-empty, unique); arrays with `items` and `minItems`/`maxItems` between 0 and 100; integral integer bounds and finite number bounds; printable-ASCII property names, `required` names and enum choices; at most three object levels (arrays add none), sixteen schema nodes on any path, 32 properties across the schema and 64 KiB normalized. Annotations (`description`, `title`, `$schema`, `$id`, `$comment`, `default`, `examples`, `deprecated`, `readOnly`, `writeOnly`, `x-` keys) are accepted and ignored for validation; `description` and `title` reach the model, and a `default` is never applied. String constraints (`format`, `minLength`, `maxLength`, `pattern`, `contentEncoding`, `contentMediaType`) are accepted but not enforced, so check them yourself if they matter. A nullable `anyOf`/`oneOf` (one schema plus `null`) is that schema, optional. Rejected: references and recursion, `allOf`/`not`, other unions, `const`, `additionalProperties` other than `false`, integer enums, duplicate keys, non-strict JSON and contradictions such as `maxItems < minItems`.
Handle failures by error type. Exit 2 is a usage error naming the offending JSON pointer before any process spawns, so nothing was inferred. Both helper codes are exit 5 and both call for the matching bundle rather than a retry or repair, but they name different causes: `native_protocol` means the helper does not match the CLI (older or newer) or the returned payload failed validation, while `local_unavailable` means the paired helper is missing or not executable. `unsupported_capability`, also exit 5, means the matched helper did not advertise a capability the request needs. Refusal, timeout and context-capacity keep their existing errors and never produce a partial object. A conforming object is structurally valid, not semantically true.

Diagnostics are measured, not estimated: `models --model local --json` exposes `helper_protocol`, `helper_version`, `context_size` when available and the route's `capabilities`, and so does `doctor --model local --json` when `agent-context` lists `--model` on `doctor` (on an older runtime such as 0.4.0, read the `local` row of plain `doctor --json`); complete responses add Apple's `usage` counts. Streaming usage stays absent. When supported, `doctor --model cloud` or `cloud-pro` reports advisory PCC quota status and an optional reset date, not a remaining request count or a promise that Shortcuts will work. Use the window and usage to explain headroom; on a context-capacity error, agree on a smaller scope with the user.

## Hollis as the model behind a script or tool

Use these when the user wants Hollis to answer for another program, not only in this conversation. Each request is still an Apple model call on the selected route: the same concrete-model, no-fallback and no-automatic-retry rules apply.

From a script, call the runtime with a named command and machine output:

```sh
bash "$KIT/scripts/run.sh" respond --agent --model cloud --prompt-file "$prompt" --timeout 120s
bash "$KIT/scripts/run.sh" respond --json --model local --select response --prompt-file "$prompt"
```

`--agent` wraps the result as `{"meta":...,"results":{...}}` and never prompts; `--json` prints the same fields unwrapped. The fields are `model_requested`, `model_used`, `response` and, for complete native `local` answers, `usage`. Errors are JSON too, `{"error":{"code","message","exit_code"}}`, and the process exit matches `exit_code`; a script should branch on `code`, not on message text. `--select response` keeps only the answer. Going through `run.sh` keeps the plugin's verification and pacing; call the path from `setup.sh path` directly only when the user accepts that those checks are skipped.

As a local OpenAI-compatible endpoint for another tool, the user starts the server on this Mac, normally in their own terminal because it runs until stopped:

```sh
umask 077
openssl rand -base64 48 > "$HOME/.hollis.token"
"$(bash "$KIT/scripts/setup.sh" path)" serve --token-file "$HOME/.hollis.token"
```

Do not start a long-running server from this conversation unless the user asks for it, and do not route it through `run.sh`, which would hold the plugin's call lock for the server's lifetime. Configure the client with base URL `http://127.0.0.1:1978/v1`, the token file's contents as its API key, and an explicit `model` such as `cloud`, `cloud-pro` or `local`. `GET /v1/models` lists what is available; `/health` needs no token. A token is required by default even on loopback; `--no-auth` is a loopback-only opt-out that lets every local process use the user's Apple routes, so only suggest it when the user asks. In this mode Hollis refuses non-loopback Host and Origin headers to resist DNS rebinding; token authentication remains safer.

What a client must send, checked against the current server:

- `POST /v1/chat/completions` with `model`, `messages` and `stream`, or `POST /v1/responses` with `model`, `input`, optional `instructions` and `stream`.
- `stream: false` for every Shortcuts route; only `local` streams. `stream: true` elsewhere is a 400.
- On runtime 0.7.1, `local` accepts `temperature`, `top_p`, `seed`, `max_tokens` / `max_output_tokens`, and caller-executed function `tools`, `tool_choice`, `parallel_tool_calls`. Confirm support through `agent-context` and the model capabilities first. Shortcuts routes refuse these fields with 400 `unsupported_parameter`; unknown fields are refused on every route. The HTTP server never executes tools. Listing models alone does not prove calls will work.
- On runtime 0.7.1, Chat Completions accepts `n:1`, disabled `logprobs` and an ignored `user` string; Local can request a final stream usage chunk with `stream_options:{"include_usage":true}` (null when unavailable), or complete-only validated `response_format:{"type":"json_object"}` without a schema. Responses JSON still requires a schema. Check the installed runtime before configuring these options.
- Disable the client's automatic retries. A 429 `rate_limited` is Apple's limit (the advisory quota preflight may supply a reset date): stop the job rather than retrying, whatever `Retry-After` the runtime sends (a 0.4.0 server sends `Retry-After: 1`; a 0.7.1 server sends none). A 429 `server_busy` is local capacity with `Retry-After: 1`.

Guided JSON (`response_format` / `text.format` with `json_schema`) works only with `model: "local"` and the same runtime support as `--schema`; it returns the object as a JSON string. Never expose the server beyond loopback unless the user explicitly asks: that needs `--allow-remote`, a token and an encrypted path such as an SSH tunnel, because Hollis provides no TLS.

## Folders

Use `batch plan --input-dir <folder> --prompt-file <instructions> --model <concrete-tier> --output-dir <results> --job <manifest>`. Planning makes no model calls. Explain the resulting scope and apply an explicit user-authorized `--max-calls` budget to `batch run` or `batch resume`. A request to process N known inputs can authorize that bounded run; do not silently invent an unlimited budget.

Keep the manifest and output directory for resume. Existing successful results are checked and skipped. A failed or uncertain item needs investigation; do not add `--retry-failed` or `--retry-uncertain` automatically. This is a finite folder job, not a watcher.
