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
- `--header NAME:VALUE` adds a non-secret HTTP provider setting, repeatable
  for different names. It is stored literally in the server's `headers` map.
  Names are case-insensitive; duplicates, credential-like names (including
  cookies and session headers), and HTTP/MCP transport headers are refused.
  At most 32 headers, each value at most 8 KiB with no control characters.
  Use `--auth` for credentials, never `--header`. Header values are not shown
  by `mcp list`; do not embed secrets inside configuration JSON.
- `mcp.json` lives in the Hollis state directory
  (`~/Library/Application Support/hollis`, or `$HOLLIS_STATE_DIR`). It is
  written atomically under a lock, kept at mode 0600, and refused if it is a
  symlink, has unknown or duplicate keys, or is not valid UTF-8.

### Offer only selected tools

`--tool NAME` on `mcp add` restricts the model's offer to exact server tool
names, not the rendered `server__tool` names. Repeat it for multiple tools:

```bash
hollis mcp add wiki --url https://mcp.deepwiki.com/mcp --tool ask_wiki_question
hollis mcp set wiki --tool ask_wiki_question
hollis mcp tools wiki
hollis respond --model local --mcp wiki --timeout 120s --stream=false \
  'In the modelcontextprotocol/go-sdk repository, how do I create a minimal MCP server with one tool? Look it up and give a brief answer with a source link.'
```

Use `add` for a new server or `set` for an existing one. `set` replaces the
whole filter without contacting the server. It preserves approval mode,
automatic-execution allow list and pins. Calls still need approval by default.

This DeepWiki recipe offers only `ask_wiki_question`, not `read_wiki_contents`,
which can return an entire repository exceeding Local's 128 KiB input limit.
A focused answer can still exceed byte or context limits. The filter is not
a result-size cap. The partial-result policy below applies independently.

The filter is saved as `"tools": ["ask_wiki_question"]` on the server in
`mcp.json`. Missing or empty `tools` means all supported tools, preserving
existing configurations. `hollis mcp set wiki --all-tools` removes the filter.
Unknown selected names fail clearly at connection time. Filtered-out tools
are never offered, and call-time checks reject them even if a model invents
their names. Attachment counts report how many tools were filtered out.
`mcp tools` lists them with `OFFER` set to `filtered`.

This filter is separate from `allow`/`deny`: those control automatic approval,
not which tools are offered. Selecting a tool never grants permission to run it.

### Opt in to partial Local results

By default, a result that cannot fit Local stops with exit 5 and code
`tool_result_too_large_for_local`. Nothing is truncated. The message names the
server, tool, and full byte count, and suggests `--partial-results`.
The same rule applies without a TTY, with `--no-input`, in JSON or agent mode,
and in scripts. JSON errors include a machine-readable `error.suggestion`.
Later calls stop, and helper and MCP sessions close before the command exits.

`--partial-results` on `respond` or `chat` allows a disclosed prefix instead.
`hollis config set partial-results on` saves this opt-in for attached Local
MCP requests. `off` or `config unset partial-results` restores the hard stop.
An explicit `--partial-results=false` overrides a saved `on` for one invocation.
Opt-in works in interactive and non-interactive calls. It does not grant tool
approval or bypass an auto allow list. HTTP and batch execute no MCP tools
and do not accept this flag.

SDK token counting admits the framed prefix
only when it leaves at least 512 answer tokens plus 128 safety tokens.
An explicit larger answer limit is reserved instead of 512.
Text blocks take priority over structured content when both are present.
Cuts prefer complete lines or JSON elements, with word boundaries as a fallback.

The original untrusted-content delimiters wrap the excerpt and its partial-result
marker. Stderr reports the full and shown byte counts and the private full-result
file. JSON adds `tool_results_bounded` entries with `server`, `tool`, `full_bytes`,
`shown_bytes`, and `path`. Full normalized results, including structured content,
are saved in the state directory's `tool-results` folder, mode 0700, with file
mode 0600. Saving beneath `/tmp`, including symlink aliases, is refused.
Files remain until you remove them. They can contain sensitive tool data.
If saving or disclosure fails, the request stops before synthesis.

The marker reads `[partial result: showing the first N of M bytes; the rest
was not shown to the model]`. Other routes, approval, validation, and the hard 1 MiB MCP result ceiling
are unchanged. Results that fit remain complete and are not saved separately.
A partial result may omit the answer or an important qualification. Check the
full result before relying on an answer that needs complete evidence.

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
| `OFFER` | `yes`, `filtered` (excluded by `tools`), or `unsupported` |

## Limits

- At most 32 attached/configured servers, 64 tools per server and 128 combined offers.
- Server-validated schema text is capped at 2 KiB per tool and 16 KiB total.
  Excluded/unsupported tools remain visible in inspection and attachment counts.
- Native definitions, server-validated metadata, conversation and framed results
  share the 128 KiB input bound and native token preflight. The encoded helper
  request stays within 1 MiB. Disclosed excerpts can reduce MCP result data,
  not definitions or user messages. Above 1 MiB, an MCP result always stops.
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
With `--partial-results` or the `partial-results` config set to `on`, result
admission reserves at least 512 answer
tokens plus 128 safety tokens. A larger requested maximum replaces 512.
Generation keeps the requested options. By default, admission
reserves the requested maximum or 512 when omitted.
MCP diagnostics separately report text/structured-result bytes, byte-identical
JSON duplication, and result versus framed bytes. No tool names, schemas,
arguments, result content or credentials enter these diagnostic records.

A small user prompt can still exceed Local capacity after a large search
result. For example, anonymous Parallel search can return more than Local can
admit; approval and a valid search do not imply a completed answer. By default,
an oversized Local result stops with `tool_result_too_large_for_local`; no excerpt
is admitted or saved. Opt in with `--partial-results` or set the `partial-results`
config to `on` to supply a disclosed excerpt and save the full result privately.
Other post-call capacity errors use `tool_result_too_large`; the diagnostic
distinguishes them from the MCP byte cap.
Neither policy automatically summarizes content.
Where the provider supports them, explicitly request smaller results or select
fewer tools at the server.

### Provider-side result caps

Provider-side result limits can reduce data before transport and avoid partial
results. They remain useful alongside the experimental client-side policy.
For a server that accepts a non-secret configuration header, for example:

```bash
hollis mcp add search --url https://api.example.dev/mcp \
  --auth bearer:EXAMPLE_KEY \
  --header 'X-Search-Config: {"max_results":3,"max_chars_total":6000}'
```

Header names and JSON formats are provider-specific. Parallel's
[MCP configuration](https://docs.parallel.ai/integrations/mcp/quickstart#override-settings)
uses `x-parallel-mcp-config` and requires authentication for overrides:

```bash
hollis mcp add parallel --url https://mcp.parallel.ai/mcp \
  --auth bearer:EXAMPLE_KEY \
  --header 'x-parallel-mcp-config: {"web_search":{"override":{"max_chars_total":6000,"advanced_settings":{"max_results":3,"excerpt_settings":{"max_chars_per_result":2000}}}}}'
hollis respond --model local --mcp parallel \
  'Find the official Go documentation page about slices. Report its title and URL.'
```

Set `EXAMPLE_KEY` in the launching environment; only its name is stored.
Parallel also accepts `--auth header:x-api-key:EXAMPLE_KEY`. The current MCP
endpoint does not accept the legacy `x-parallel-search-config` header or
configuration query parameters; use the documented configuration header.
Overrides apply after model arguments, so the model cannot raise these caps.
The example caps excerpts, not metadata, framing or total native tokens.
Use `HOLLIS_DEBUG_BUDGET=1` to measure the complete request and leave room
for tool definitions, conversation and the answer. Adjust caps for each task;
these values are not a guarantee for every result or conversation length.

## Failures

Configuration problems are `mcp_config` (exit 10). A server that cannot start
or be reached is `mcp_unavailable`; one that breaks the protocol or a cap is
`mcp_protocol`; a rejected credential is `mcp_auth`; an `auto` allow list that
names a server-validated tool is `mcp_schema_unvalidated`. Each of those exits
5. See [errors](errors.md).
