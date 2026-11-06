# MCP servers

Hollis 0.7.0 can attach MCP (Model Context Protocol) tools to an explicit
`local` model request, from a local program over stdio or a remote server over
HTTP. Calls need your approval (`ask`, the default) or an explicit allow list
(`auto`). Runtime 0.4.0 does not include MCP. Servers in `mcp.json` remain
inert until selected.

Local uses executable native tool callbacks, with independent Go validation
before approval or execution. Invalid arguments stop with `tool_call_invalid`;
an incomplete native batch stops with `native_protocol`. Nothing in that batch runs.
`hollis serve` and
`hollis batch` never read `mcp.json` and never start an MCP server.

## Use tools with the local model

```bash
hollis respond --model local --mcp files "Summarize notes.md"
hollis chat --model local --mcp files
hollis respond --model local --mcp files --mcp wiki "Cross-check this note"
hollis respond --model local --mcp-server 'program "argument with spaces"' "Read fixture data"
```

`--mcp <name>` selects a configured server, repeatable in request order.
`--mcp-server` accepts a command with quoted arguments or an HTTP URL. No
shell, variable expansion or command substitution is used. Ad-hoc servers
are named `adhoc-1`, `adhoc-2`, and are always ask-only. `--mcp-timeout`
sets a positive call deadline up to 120 seconds; the command/turn deadline
still wins. `--no-mcp` overrides attachment flags and guarantees no MCP
config read or server attachment. Nothing attaches by default.

MCP is local-only and complete-only: images, image generation, guided output,
code extraction and streaming cannot be combined with it. Requested servers
attach all-or-nothing and sessions close on exit. Chat retains attached sessions
for the invocation, but each turn has its own model/call deadline.

At most 128 combined tools are offered, including at most 32 caller-provided
`--tools`. Name collisions or excess counts fail before generation; no tool
is silently selected or dropped to fit the model. Caller and locally validated
MCP definitions become executable native tools. Ask-only server-validated
definitions use an explicit native `arguments_json` string envelope, with the
bounded original schema in escaped untrusted metadata. Go unwraps a strict
JSON object without coercion or defaults, then asks with the full original
arguments and a notice that the server validates them. An old helper refuses
this capability rather than falling back silently.

The default budget is eight model decisions per visible user transaction,
including prior structured call batches and final synthesis. A callback batch
on the eighth decision fails with `tool_round_cap` before dispatch.
Models must use the exact `<server>__<tool>` name. There is no Local text-call
extraction, name correction or argument repair. Quoted `CALL:` or JSON calls
in prose, documents and results stay content and never become executable.
Shortcuts and ChatGPT bridge/client text protocols are unchanged; they do not
enable MCP or the public tools API on those routes.

Only a transient native `model_busy` decision can retry once, after 500 ms,
with identical input and the original deadline, before a callback is captured
or executed in that decision. This is not argument repair. Guardrail, context
and decoding failures do not retry. Tool execution never retries.

### Mixed caller and MCP tools

`respond --tools tools.json --mcp files --model local` keeps the existing
caller-tool JSON document. Hollis executes only authorized MCP calls, serially
in model order. If any caller calls remain, top-level `tool_calls` contains only
those pending caller calls with their original IDs; no further model generation
occurs. The `mcp` member contains server counts, MCP audit records and ordered
executed/pending dispositions, never raw tool results. Caller-only output is
unchanged. `--tools` still cannot be combined with `--json` or `--agent`.

A partial failure stops later MCP calls and emits one error document with
accumulated audit records, not a successful pending-work document. A valid MCP
`isError` result is tool data, not a transport failure. `executed:true` means
Manager entered a dispatch attempt, not proof of transmission or completion.
A failed attempt can have an unknown remote outcome. Hollis never retries it.
There is no new caller-result input, durable receipt or CLI resume protocol.
Repeating a command is a new invocation and can repeat side effects.

MCP-only JSON/agent output adds `mcp_servers`, `tool_calls` and
`tool_dispositions`, also available through `--select`. Agent fields are under
`results`. Failures retain the collected audit fields alongside the error.
Arguments appear only in that caller-requested audit output, approval prompts
and chat transcripts. Raw results remain framed transcript data, not diagnostics.

## Configure a server

```bash
hollis mcp add files --command npx --arg -y --arg @modelcontextprotocol/server-filesystem --arg /Users/you/Documents
hollis mcp add wiki --url https://mcp.deepwiki.com/mcp
hollis mcp add search --url https://api.example.dev/mcp --auth header:X-API-Key:EXAMPLE_KEY
hollis mcp list
hollis mcp tools wiki
hollis mcp remove files
```

- A stdio server is a program and its arguments. It is started directly, with
  no shell: `--command "npx -y pkg"` is refused. Pass each argument with `--arg`.
- An HTTP server is an `https://` URL. Plain `http://` is accepted only for
  loopback addresses.
- `mcp.json` lives in the Hollis state directory
  (`~/Library/Application Support/hollis`, or `$HOLLIS_STATE_DIR`). It is
  written atomically under a lock, kept at mode 0600, and refused if it is a
  symlink, has unknown or duplicate keys, or is not valid UTF-8.

## Secrets never live in `mcp.json`

- `--env KEY=VALUE` is for non-secret settings. A name that looks like a
  credential (containing `TOKEN`, `KEY`, `SECRET`, `PASSWORD`, `CREDENTIAL`,
  `AUTH`, or starting with `HOLLIS`) is refused.
- `--env-from CHILD=PARENT` passes the parent variable `PARENT` to the server
  as `CHILD` when it starts. The value is never stored.
- `--auth bearer:VAR` or `--auth header:NAME:VAR` sends one header whose value
  is read from `VAR` when the server is contacted.
- `hollis mcp list` shows which referenced variables are set. It never prints
  their values or the command's arguments.

A stdio server's environment is built from scratch: `PATH`, `HOME`, `TMPDIR`,
`LANG`, `LC_*`, `PYTHON*`, `VIRTUAL_ENV`, the `--env` settings and the
`--env-from` variables. Credential-like names are dropped whatever their
source. Nothing else from your shell reaches it.

## Approval

Each server has an approval mode:

- `ask` (the default): every call needs a yes on the terminal. The answer is
  read only from the terminal device, never from piped input. A run that
  cannot ask fails closed with `approval_required`. `--json`, `--agent`,
  `--no-input` and a missing controlling terminal cannot approve calls.
- `auto`: only exact tool names in the allow list run without asking.

```bash
hollis mcp allow wiki read_wiki_structure
hollis mcp mode wiki auto
hollis mcp deny wiki read_wiki_structure
```

`hollis mcp allow` starts the server once, reads the tool's current name,
description and input schema, and records a pin of that definition. An `auto`
tool runs only while its live definition still matches the pin. If the server
changes the tool, `hollis mcp tools` shows it as `changed` and the tool stops
running automatically until you review it and run `hollis mcp allow` again.
A tool that appears after a session started is never run automatically.

## What `hollis mcp tools` shows

`hollis mcp tools <server>` starts the server once, lists its tools and stops
it. It never runs a tool and never calls a model. For each tool:

| Column | Meaning |
| --- | --- |
| `MODEL NAME` | The name a model would see: `<server>__<tool>` |
| `VALIDATION` | `local`: Hollis checks the arguments against the schema. `server`: only the server validates them, so the tool is ask-only. `unsupported`: the tool is never offered, with the reason printed below the table |
| `ALLOW` | `-` (not allow-listed), `unpinned`, `current`, or `changed` |

## Limits

- At most 32 attached/configured servers, 64 tools per server and 128 combined offers.
- Server-validated schema text is capped at 2 KiB per tool and 16 KiB total.
  Excluded/unsupported tools remain visible in inspection and attachment counts.
- Native definitions, server-validated metadata, conversation and framed results
  share the 128 KiB input bound and native token preflight. The encoded helper
  request stays within 1 MiB. Oversized input/results fail without truncation.
- Connect deadline 10 s by default (`--connect-timeout`, up to 60 s); call
  deadline 20 s (`--call-timeout`, up to 120 s).
- HTTP: TLS 1.2 or newer, no redirects, no retries, no background event
  stream, and response bodies are size-capped. A server's stderr is counted
  but never shown.
- Tool results are treated as untrusted data: a single text block, capped in
  size, and framed so instructions inside it cannot pass as Hollis's own.

### Context diagnostics

Set `HOLLIS_DEBUG_BUDGET=1` for opt-in numeric diagnostics on stderr. Native
preflight reports token costs for base/profile instructions, tool definitions
(including MCP schemas), existing conversation, returned results with framing,
the answer reservation and measured context capacity, plus component byte
counts. Token costs are incremental in that order and sum to `input_tokens`.
The answer reservation is the requested per-decision maximum, or 512 when
omitted; it does not change the SDK's default generation options.
MCP diagnostics separately report text/structured-result bytes, byte-identical
JSON duplication, and result versus framed bytes. No tool names, schemas,
arguments, result content or credentials enter these diagnostic records.

A small user prompt can still exceed Local capacity after a large search
result. For example, anonymous Parallel search can return more than Local can
admit; approval and a valid search do not imply a completed answer. Hollis
preserves the full result and refuses with `context_capacity` when the native
token preflight fails. `tool_result_too_large` instead identifies an MCP
byte-limit failure. Neither path truncates or automatically summarizes content.
Where the provider supports them, explicitly request smaller results or select
fewer tools at the server.

## Failures

Configuration problems are `mcp_config` (exit 10). A server that cannot start
or be reached is `mcp_unavailable`; one that breaks the protocol or a cap is
`mcp_protocol`; a rejected credential is `mcp_auth`; an `auto` allow list that
names a server-validated tool is `mcp_schema_unvalidated`. Each of those exits
5. See [errors](errors.md).
