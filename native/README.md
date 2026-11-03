# Native local helper

`hollis-native` is the precompiled Apple Silicon/macOS 27 companion to Hollis.
Build it with `scripts/build-native.sh OUTPUT`; install it beside the
matching Hollis executable. Hollis resolves its own executable through symlinks,
then requires a regular executable sibling helper. It does not search `PATH` or
compile code during a request.

`--version` and `--protocol-version` print metadata without initializing a model.
Normal operation reads exactly one JSON request from stdin and writes NDJSON to
stdout. Prompt text never appears in process arguments.

`Extraction.swift` is the document library behind the `extract` operation: PDF text per
page with Vision OCR for pages that have none, content-based detection, and
bounded, symlink-free file input. It performs no inference and knows nothing
about the protocol. A page that fails to render or whose OCR request fails
makes the whole document unreadable; only an OCR pass that succeeds and finds
nothing counts as an empty page. A document with no non-whitespace text in the
selected pages is a no-text failure, not an empty success. A requested page past
the end is refused with the document's real page count, before any page is
read. `scripts/native` builds and tests it offline.

The accepted protocol set is explicit: `[1, 2, 3]`. A protocol-3 request has
`protocol`, `operation` (`status`, `complete`, `stream`, `schema`, or `extract`), and a
`prompt` for generation; a `schema` request also carries `schema`, and a
generation request may carry `tool_calling` (`"allowed"` or `"disallowed"`).
Older request formats retain their field shapes: protocol-1 requests have three
operations and no `context_size`/`structured`; protocol-2 requests add `schema`.
This is not cross-version CLI compatibility. This source build supports
`tool_calling` on protocol-3 `complete` and `stream` operations: `"allowed"`
and `"disallowed"` select the corresponding SDK generation mode. Other
values (including `"required"`), older protocols and schema operations are
refused as `native_failed` before generation. The CLI and HTTP tool surfaces
remain complete-only. Definitions are native transcript metadata, not
executable SDK tools. This
describes the source build, not capabilities added to already released bundles.

Each event is stamped with the request's protocol, `version: "0.7.0"`, and
`model: "local"`. The CLI and helper ship as a matched bundle. A mismatched pair,
including a 0.4.0 or 0.5.0 CLI with this helper, fails closed on strict version
identity with a clear mismatch error, before generation. Install the matching
bundle; accepting an older request format does not bypass this check.

- `status`: `available` Boolean, `reason` string, `context_size` on protocol 2
  and above (the measured token budget of the local model), and, on protocol 3,
  `capabilities`: `vision`, `macos_major`, and an `extract` block (`pdf`,
  `ocr`, `pages`, `max_pages` 2000, `max_text_bytes` 131072), with a block per
  capability that a later build may add. `images` reports `supported`,
  `max_images` 1, `formats` (`png`, `jpeg`) and `max_side_px` 4096, or a fixed
  negative reason (`macos_26` or `no_vision`).
  `tools: {supported: true, tool_calling_mode: true, native_definitions: true}`
  when the available model supports tool calling. `supported` and
  `native_definitions` are false otherwise; mode handling is implemented.
  An unimplemented capability is absent rather than
  zero-filled, and clients read the object leniently so a new flag needs no
  protocol bump. Status operations stop here without creating a session or
  generating a response.
- Image-bearing `complete` and `stream` requests carry
  `images: [{"path": "/absolute/private/staged.png"}]`, protocol 3 only.
  `ImageInput.swift` opens direct regular files without following a final
  symlink or blocking on a FIFO, caps input at 64 MiB and 64 million pixels,
  and decodes PNG/JPEG with EXIF orientation and a 4096-pixel longest-side cap.
  The resulting pixels enter a `Prompt` attachment, never OCR text. Images
  cannot accompany `schema`, `extract`, `status`, or tool calling.
  Prompt-only token preflight does not estimate attachment tokens.
  The public local route admits one image through CLI and HTTP; runtime vision
  availability is still checked, and support is not proof of successful inference.
- `snapshot`: cumulative `text` from streaming. The Go runner suppresses duplicate
  snapshots and emits append-only differences; revisions fail the stream.
- `complete`: final `text`, plus measured `usage` for complete requests. Usage
  includes integer `input_tokens`, `output_tokens`, and `reasoning_tokens`.
  Streaming completions omit usage because it has not been qualified. A `schema`
  completion also carries `structured`, the generated object.
- `schema` (protocol 2 and above): complete-only guided generation. The helper parses
  and enforces the supported JSON Schema subset before any model access, builds a
  dynamic `GenerationSchema`, generates the object, re-validates the returned
  object against the same subset, and only then emits `complete` with `structured`
  and measured usage. Annotations are ignored for validation, with `description`
  (or `title`) carried into the `GenerationSchema`; a `default` is never
  applied; string constraints are
  accepted and not enforced; an omitted `additionalProperties` means `false`;
  and a nullable `anyOf`/`oneOf` property collapses to its non-null branch as an
  optional property. Unsupported schemas (references, other unions, `const`,
  integer enums, non-object roots, `additionalProperties` other than `false`, objects
  nested deeper than 3, more than 16 schema nodes on a path including the root,
  property/required names or enum choices outside printable ASCII, more than 32
  properties, more than 64 KiB, or contradictory bounds) and schema-plus-`stream`
  fail closed with an `error` event and no partial object. The caller re-validates
  the object independently; structural validity never claims semantic truth.
  See the [full subset rules](../docs/native-local.md#guided-output).
- `extract` (protocol 3): reads one PDF without any model access. The request
  carries `source.path` (absolute; the Go runner passes a private staged
  copy), optional `pages` (ascending, unique, 1-based, at most 2000) and an
  optional `media` hint (`pdf`). It accepts no `prompt`, `schema` or
  `tool_calling`, and generation requests accept no `source`, `pages` or
  `media`. After the status event the helper emits `complete` with `media`,
  `page_count`, `text_bytes` and `pages` (`page`, verbatim `text`, and
  `method` `text` or `ocr`), or an `error`: `invalid_document`,
  `extract_no_text`, `unsupported_media`, `extract_media_mismatch`,
  `page_out_of_range` with `page` and `page_count`, or `context_capacity`;
  ceiling failures add integer `limit` and `actual`.
- `error`: a stable `kind` without input or framework diagnostic text.
  Protocol-3 capability refusals also carry closed-set `capability` and `reason`
  identifiers, preserved in the CLI error (exit 5) and HTTP 502 message.

A generation starts with status, then either complete or snapshots followed by
complete. A schema request follows the same status-then-complete shape. Clean EOF and successful process exit are required; a complete event
alone is insufficient. A failed stream may have displayed partial text but is
never a successful completion. The runner owns deadlines and process-group
cancellation; the terminal presentation layer owns safe display escaping.

Each generation uses one fresh SDK session and the entire rendered prompt.

Metadata tool generation requires the `--native-definitions` mode argument,
protocol 3, `complete`, `tool_calling: "allowed"`, and both `tool_instructions`
and `tool_definitions`. Definitions carry `name`, `description` and `parameters`;
the shared `SchemaSubset` converter builds each `Transcript.ToolDefinition`.
One native instructions entry holds the fixed format text and definitions.
The executable `tools` list stays empty. `none` and plain requests omit both
members and the mode argument, preserving the ordinary session path.

Go performs a bounded non-generative status preflight before sending metadata,
checking version 0.7.0 and `tools.native_definitions` under the same deadline.
An old helper swapped in afterward rejects the mode argument before model
access. The matched helper independently validates metadata and runtime tool
support, refusing unsupported capability or conversion without text fallback.
The 128 KiB aggregate bound includes prompt, instructions, names, descriptions
and normalized schemas; the encoded stdin request is capped at 1 MiB.
The single token preflight counts native instructions plus one pending prompt.
Only instructions seed the session, and `respond` adds the prompt exactly once.

Neither helper nor runner removes conversation entries, summarizes history,
retries transport failures, or changes model. The Go tool loop can request
one validation correction within its three-call budget; the helper never retries.
Every new dispatch, including correction, final synthesis and `none`, consumes
budget, as do visible prior tool-call turns since the last user message.
Stateless replay cannot recover hidden earlier corrections, so this is not a
global cross-request quota.
The helper checks Apple's token count against the
reported context size before generation and separately maps the SDK's typed
context-size error. Hollis also enforces its 128 KiB rendered-input limit. These
are explicit failures; a caller must deliberately change its input or route.

The helper uses the default local system model, with no executable SDK tools
or custom persona. Validated caller-tool requests are returned to the caller;
the CLI and server do not execute them.
It does not provide native PCC access. Availability checks describe configuration,
not successful inference. Model identity is not inferred from a successful call.
