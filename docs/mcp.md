# MCP servers

Current source can attach MCP (Model Context Protocol) tools to an explicit
`local` model request. This is not a claim of live qualification or availability
in an older installed runtime. Servers in `mcp.json` remain inert until selected.
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
MCP definitions use native metadata. Server-validated definitions use an
explicit, capability-gated, bounded raw-schema instruction representation,
including when every tool is server-validated. An old helper refuses this
capability rather than falling back silently.

Each user turn admits at most three model generations, including argument
corrections and final synthesis. Models see each MCP tool as
`<server>__<tool>`. A call that names only `<tool>`, when exactly one attached
server offers it, is corrected once with the declared name; it never runs as
written and is never renamed. Any reply that contains `"tool_calls"` and quotes
a declared or uniquely matching tool name is treated as a call attempt and
corrected once, however broken it is; nothing in it runs. If the corrected
reply is still wrong, the run fails with `tool_call_invalid`. Arguments for
locally validated tools are checked before approval or execution. For server-validated tools, Hollis checks
only a bounded JSON object, without coercion or defaults, then asks with the
full arguments and a notice that the server validates them.

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
- Native definitions, raw-schema instructions, conversation and framed results
  share the 128 KiB input bound and native token preflight. The encoded helper
  request stays within 1 MiB. Oversized input/results fail without truncation.
- Connect deadline 10 s by default (`--connect-timeout`, up to 60 s); call
  deadline 20 s (`--call-timeout`, up to 120 s).
- HTTP: TLS 1.2 or newer, no redirects, no retries, no background event
  stream, and response bodies are size-capped. A server's stderr is counted
  but never shown.
- Tool results are treated as untrusted data: a single text block, capped in
  size, and framed so instructions inside it cannot pass as Hollis's own.

## Failures

Configuration problems are `mcp_config` (exit 10). A server that cannot start
or be reached is `mcp_unavailable`; one that breaks the protocol or a cap is
`mcp_protocol`; a rejected credential is `mcp_auth`; an `auto` allow list that
names a server-validated tool is `mcp_schema_unvalidated`. Each of those exits
5. See [errors](errors.md).
