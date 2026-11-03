# MCP servers

Hollis can keep a list of MCP (Model Context Protocol) servers in `mcp.json`
and inspect them. This release adds configuration and inspection only.
**No Hollis command runs an MCP tool yet.** Attaching servers to a model
request will arrive with the local tool loop. `hollis serve` and
`hollis batch` never read `mcp.json` and never start an MCP server.

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
  cannot ask fails closed with `approval_required`.
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

- At most 32 servers in `mcp.json` and 64 tools per server.
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
