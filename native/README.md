# Native local helper

`hollis-native` is the precompiled Apple Silicon/macOS 27 companion to Hollis
0.4.0. Build it with `scripts/build-native.sh OUTPUT`; install it beside the
matching Hollis executable. Hollis resolves its own executable through symlinks,
then requires a regular executable sibling helper. It does not search `PATH` or
compile code during a request.

`--version` and `--protocol-version` print metadata without initializing a model.
Normal operation reads exactly one JSON request from stdin and writes NDJSON to
stdout. Protocol version 1 requests have `protocol`, `operation` (`status`,
`complete`, or `stream`), and a `prompt` for generation. Prompt text never appears
in process arguments. Each event carries `protocol: 1`, `version: "0.4.0"`, and
`model: "local"`.

- `status`: `available` Boolean and `reason` string. Status operations stop here
  without creating a session or generating a response.
- `snapshot`: cumulative `text` from streaming. The Go runner suppresses duplicate
  snapshots and emits append-only differences; revisions fail the stream.
- `complete`: final `text`, plus measured `usage` for complete requests. Usage
  includes integer `input_tokens`, `output_tokens`, and `reasoning_tokens`.
  Streaming completions omit usage because it has not been qualified.
- `error`: a stable `kind` without input or framework diagnostic text.

A generation starts with status, then either complete or snapshots followed by
complete. Clean EOF and successful process exit are required; a complete event
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
