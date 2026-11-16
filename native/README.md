# Native local helper

`hollis-native` 0.7.1 uses protocol 4. It is an Apple Silicon/macOS 27 companion
to the matching Hollis executable. Build it with `scripts/build-native.sh OUTPUT`.
Hollis resolves its executable through symlinks and requires a regular executable
sibling helper. It never searches `PATH` or compiles during a request.

`--version` and `--protocol-version` print metadata without model access.
Each invocation reads one JSON request through stdin EOF and writes NDJSON.
The encoded request limit is 1 MiB; prompt text never enters process arguments.
Every event carries protocol, version and `model:"local"`. Clean EOF and a
successful exit are required after the terminal event.

## Operations and identity

Plain operations are `status`, `complete`, `stream`, `schema` and `extract`.
The helper accepts request formats 1, 2, 3 and 4, preserving their field shapes.
This does not provide mixed-bundle compatibility. The CLI negotiates protocol 4
and exact helper version before every operation. An old CLI, old helper or
mismatched version fails closed. No native failure falls back to text tools,
Shortcuts or another model.

`status` reports availability, measured `context_size` and capabilities.
The tool block includes `supported`, `tool_calling_mode`, `native_definitions`,
`hybrid_tools`, `executable` and `turn_handoff`. The last two must both be true
for native tool generation. Metadata support alone is insufficient.

`complete` returns final `text` and measured `usage` with `input_tokens`,
`output_tokens` and `reasoning_tokens`. `stream` emits cumulative `snapshot`
text then `complete`. Go forwards append-only deltas and rejects revisions.
Streaming usage is absent. Failed streams are not successful completions.

`schema` accepts the [guided-output subset](../docs/native-local.md#guided-output).
Both helper and Go validate the generated object. The terminal includes
`structured`; unsupported or invalid schemas fail before generation.

`extract` reads a PDF without inference. It carries an absolute `source.path`,
optional ascending unique `pages` and optional `media:"pdf"`. It rejects
generation fields. `Extraction.swift` performs bounded, symlink-free reading,
per-page text extraction and Vision OCR where text is absent. A failed page
fails the document. The terminal carries `media`, `page_count`, `text_bytes`
and pages with `page`, verbatim `text` and `method` (`text` or `ocr`).
Limits are 2,000 pages and 128 KiB extracted text. Typed failures include
`invalid_document`, `extract_no_text`, `unsupported_media`,
`extract_media_mismatch`, `page_out_of_range` and `context_capacity`.

`complete` and `stream` requests with images carry one `images` path.
`ImageInput.swift` checks direct regular PNG/JPEG files, EXIF orientation,
64 MiB input, 64 million pixels and a 4,096-pixel longest-side cap.
Images are real prompt attachments, not OCR. Images cannot accompany guided
output or tools. Prompt token counting does not estimate attachment tokens.

## Executable native tools

`--native-tools` gates `tool_step` and `tool_check`. Requests include
`request_id`, `step`, `remaining_steps`, `instructions`, `tool_choice`, `tools`
and closed typed `history`. Tool definitions have `name`, `description`,
`parameters`, `source` (`caller` or `mcp`) and `validation` (`strict` or `server`).
History contains user/assistant text, call batches and matching tool outputs.
Clients cannot supply opaque SDK transcripts. System messages become profile
instructions, not duplicated history.

Swift registers actual `Tool` values with runtime `GenerationSchema`.
Their callback only throws a handoff sentinel. With preserved transcripts,
the helper admits exactly one complete batch, including framework IDs, names
and JSON object arguments. Partial batches, cancellation inconsistencies and
non-sentinel exceptions are failures. Swift never executes a tool.

After status, `tool_step` emits exactly one terminal:

- `tool_calls` with matching `request_id`, `step` and nonempty `calls`.
- `complete` with final text.
- `error` with a fixed typed `kind`, never private diagnostic text.

Calls cannot coexist with text, snapshots or another terminal. IDs remain
unchanged, are unique, valid UTF-8, control-free and at most 128 bytes.
Go validates the whole batch before approval or execution. It does not
coerce numeric strings, extract quoted calls or request automatic repair.

Go executes approved MCP calls serially, preserves completed audits and checks
each result before the next approval. Caller calls remain pending for external
execution. The ask-only server-validated tier uses the same namespaced tool
name with an `arguments_json` string envelope. Go unwraps a strict JSON object
without rounding numbers. Human approval and server validation remain mandatory.

A resumed helper reconstructs `Transcript.ToolCall` and `ToolOutput` entries
with exact matching IDs and names, then calls `respond(to: "")`. It does not
repeat the last user prompt. Historical calls are never dispatched again.

`tool_check` only counts SDK tokens. Its `checked` terminal carries `input_tokens`
and the request ID, with no generation or callbacks. Go uses it for result
admission, reserving `max_tokens` when supplied. History, framing and definitions
share 128 KiB; the full transcript is never truncated or summarized.

With explicit `HOLLIS_DEBUG_BUDGET=1`, a tool operation may emit one numeric
`budget` event between status and its terminal, with the same request ID.
The parent admits only a closed vocabulary of byte/token counters, never
content or credentials, and writes it to stderr. It does not change admission,
generation options or callback handling. See [context diagnostics](../docs/mcp.md#context-diagnostics).

The default limit is eight model decisions, including final synthesis and
visible prior tool batches since the last user turn. A batch on the final
decision fails before dispatch. Stateless history is not a global quota.
There are at most 16 calls per batch, 32 caller tools and 128 total offers.
One deadline covers inference, approvals, execution and helper launches.

| Choice | Initial decision | After its call has a result |
| --- | --- | --- |
| `auto` | All tools allowed | All tools allowed |
| `none` | Callbacks disallowed | Callbacks disallowed |
| `required` | At least one real offered call | All tools allowed |
| `function` with exact name | Only the selected tool, required | Callbacks disallowed |

The obligation is per visible user transaction. A replayed required call
satisfies it; a new user turn resets it. Required has no synthetic exit tool.
Tool requests remain complete-only.

Legacy `--native-definitions` and `--hybrid-tools` metadata formats remain
separate compatibility inputs. Local production dispatch does not use them.

## Sampling

Protocol-4 generation accepts optional `generation_options`. Omitted fields
retain SDK defaults. `temperature` is finite in 0..2. Zero selects greedy.
`top_p` is in (0,1] and selects random sampling with `seed` when present.
A seed alone uses top-p 1. Seeds are UInt64. Greedy plus seed or top-p is
rejected, not ignored. Positive integral `max_tokens` maps to
`maximumResponseTokens` per decision, not a cumulative tool-loop quota.

Sampling applies to Local only. Reproducibility across model or OS versions
is not promised. Availability and schema conformance do not establish intent.
