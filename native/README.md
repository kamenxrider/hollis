# Native local helper

`hollis-native` is the precompiled Apple Silicon/macOS 27 companion to Hollis.
Build it with `scripts/build-native.sh OUTPUT`; install it beside the
matching Hollis executable. Hollis resolves its own executable through symlinks,
then requires a regular executable sibling helper. It does not search `PATH` or
compile code during a request.

`--version` and `--protocol-version` print metadata without initializing a model.
Normal operation reads exactly one JSON request from stdin and writes NDJSON to
stdout. Prompt text never appears in process arguments.

Protocol 2 requests have `protocol`, `operation` (`status`, `complete`, `stream`,
or `schema`), and a `prompt` for generation; a `schema` request also carries
`schema`. Each event is stamped with the request's protocol, `version: "0.5.0"`,
and `model: "local"`. A protocol-1 request is still accepted for its original
three operations and answered with protocol-1 events (no `context_size`, no
`structured`, no `schema` operation); a managed install caught between the two
public symlinks fails closed on the version-and-protocol identity check, never on
a misinterpreted request.

- `status`: `available` Boolean, `reason` string, and, on protocol 2,
  `context_size` (the measured token budget of the local model). Status
  operations stop here without creating a session or generating a response.
- `snapshot`: cumulative `text` from streaming. The Go runner suppresses duplicate
  snapshots and emits append-only differences; revisions fail the stream.
- `complete`: final `text`, plus measured `usage` for complete requests. Usage
  includes integer `input_tokens`, `output_tokens`, and `reasoning_tokens`.
  Streaming completions omit usage because it has not been qualified. A `schema`
  completion also carries `structured`, the generated object.
- `schema` (protocol 2 only): complete-only guided generation. The helper parses
  and enforces the supported JSON Schema subset before any model access, builds a
  dynamic `GenerationSchema`, generates the object, re-validates the returned
  object against the same subset, and only then emits `complete` with `structured`
  and measured usage. Unsupported schemas (references, unions, patterns, formats,
  integer enums, non-object roots, missing `additionalProperties: false`, objects
  nested deeper than 3, more than 16 schema nodes on a path including the root,
  property/required names or enum choices outside printable ASCII, more than 32
  properties, more than 64 KiB, or contradictory bounds) and schema-plus-`stream`
  fail closed with an `error` event and no partial object. The caller re-validates
  the object independently; structural validity never claims semantic truth.
  See the [full subset rules](../docs/native-local.md#guided-output).
- `error`: a stable `kind` without input or framework diagnostic text.

A generation starts with status, then either complete or snapshots followed by
complete. A schema request follows the same status-then-complete shape. Clean EOF and successful process exit are required; a complete event
alone is insufficient. A failed stream may have displayed partial text but is
never a successful completion. The runner owns deadlines and process-group
cancellation; the terminal presentation layer owns safe display escaping.

Each generation uses one fresh SDK session and the entire rendered prompt.
Neither helper nor runner removes conversation entries, summarizes history,
retries, or changes model. The helper checks Apple's token count against the
reported context size before generation and separately maps the SDK's typed
context-size error. Hollis also enforces its 128 KiB rendered-input limit. These
are explicit failures; a caller must deliberately change its input or route.

The helper uses the default local system model, with no tools or custom persona.
It does not provide native PCC access. Availability checks describe configuration,
not successful inference. Model identity is not inferred from a successful call.
