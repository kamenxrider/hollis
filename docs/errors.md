# Understanding failures

Hollis distinguishes an Apple request for a different description from an
unrecognized Shortcut execution failure, and reports a missing native helper
separately from a mismatched one. CLI numeric exits and HTTP statuses follow
the table; machine-readable codes are more specific. The JSON envelope and
agent schema version remain unchanged.

| Machine code | Meaning | CLI exit | HTTP status | Next action |
| --- | --- | --- | --- | --- |
| `request_declined` | The failed process returned Apple's recorded “Try describing something different to create an image” diagnostic | 5 | 502 | Review the description; a different request is a new experiment, not a silent retry |
| `shortcut_failed` | The Shortcut executed unsuccessfully without a recognized diagnostic | 5 | 502 | Inspect the failure context before making another call |
| `transport` | Launch, staging or other transport failure, including Shortcut stdout that exceeded the 1 MiB capture cap | 5 | 502 for typed launch/transport errors | Check host access and process setup; for an oversized response, narrow the request instead of expecting a partial answer |
| `native_protocol` | The paired native helper and this CLI disagree about their contract: an older or newer helper, an event that breaks the sequence, or a guided object that does not match the requested schema | 5 | 502 | Install the matching bundle from the same release; do not retry against the same helper |
| `unsupported_capability` | The paired helper speaks this protocol era but did not advertise, or refused, a capability the request needs (for example native tool calling) | 5 | 502 | Install the matching bundle from the same release; the helper is newer than the request needs or was built without that capability |
| `tool_call_invalid` | A recognized tool call remained malformed or failed validation after its one repair round, or one round carried more than 16 parsed calls. The message names the round and a fixed reason, never an argument value | 5 | 502 | Simplify the tools or the request; do not retry unchanged |
| `tool_round_cap` | The model asked for another tool call in the third model round of one user turn | 5 | 502 | Answer from the results you have, or start a new turn; do not retry unchanged |
| `local_unavailable` | The native local model or its paired helper is unavailable: no helper is installed or executable, or it cannot answer | 5 | 400 `model_unavailable` at admission; 503 `model_unavailable` mid-run | Install the matching bundle from the same release; do not retry the same invocation |
| `model_busy` | The native model returned transient `ModelManagerError` code 1012 | 5 | 503 | A tool decision retries once after 500 ms only before any callback is captured or executed in that decision; no tool is re-executed |
| `mcp_config` | `mcp.json` is invalid, or a `hollis mcp` edit would make it invalid; the file is left unchanged | 10 | n/a | Fix the field the message names, or run `hollis mcp list` to see the current file |
| `mcp_unavailable` | A configured MCP server could not start, or its endpoint could not be reached or failed TLS verification | 5 | n/a | Check the command or URL with `hollis mcp tools <server>`; nothing was retried |
| `mcp_protocol` | An MCP server broke the protocol, published a malformed tool list, redirected, or exceeded a size cap | 5 | n/a | Treat the server as broken for this run; Hollis never retries or uses a partial listing |
| `mcp_auth` | An HTTP MCP server rejected the credential read from the configured environment variable | 5 | n/a | Set a valid value in the variable `hollis mcp list` names; the credential is never printed |
| `mcp_schema_unvalidated` | An `auto` server would allow a tool whose arguments only the server can validate | 5 | n/a | Keep the server in `ask` mode, or remove the tool with `hollis mcp deny` |
| `approval_required` | An MCP call needs approval but this invocation cannot ask | 5 | n/a | Use an interactive invocation or a reviewed configured auto allow list; nothing was run |
| `approval_args_too_large` | The complete arguments exceed the approval display cap | 5 | n/a | Narrow the call; Hollis never asks approval for a truncated preview |
| `tool_denied` | The MCP call was not explicitly approved | 5 | n/a | The run stops; later calls do not execute |
| `tool_not_allowlisted` | An automatic MCP call has no current reviewed allow-list entry | 5 | n/a | Review its definition with `hollis mcp tools` and pin it with `hollis mcp allow` |
| `tool_definition_changed` | An automatic MCP tool no longer matches its reviewed definition pin | 5 | n/a | Review the changed definition before pinning again |
| `tool_result_too_large` | An MCP result exceeds its cap or cannot fit the next bounded transcript | 5 | n/a | Narrow the request; nothing was truncated |
| `tool_name_conflict` | Caller and MCP tools share a rendered name | 2 | n/a | Rename the caller tool or select noncolliding servers |
| `tool_count_limit` | The combined offer exceeds 128, or caller tools exceed 32 | 2 | n/a | Select fewer servers or caller tools; no automatic pruning occurs |

`native_protocol` is a component mismatch, not a bad invocation: the command
was understood and the installed helper cannot serve it. It deliberately shares
exit 5 with the transport family so a script can treat a stale pairing like any
other failed execution, while the machine code names the cause. `hollis serve`
keeps the classification from its startup status probe, so a paired helper that
answers with another protocol or version is 502 `native_protocol` for every
`local` request, guided or plain, with a message that the helper does not match
this Hollis version and a pointer at the matching bundle. A missing or
unavailable helper stays 400 `model_unavailable`; a malformed payload from a
matched helper keeps the shared `native_protocol` sentence.

`unsupported_capability` is a component mismatch of the same family but a narrower cause: the helper answered validly, in the
right protocol, and simply does not offer what the request needs. It is never used for a caller mistake such as too many
images or an unsupported parameter; those stay usage errors, because telling a caller to reinstall for a bad request would be
wrong. The remedy is the same matching bundle.

The `mcp_*` codes come from `hollis mcp` commands and explicitly attached
`respond`/`chat` tools. `hollis serve` never reads
`mcp.json` or starts an MCP server, so none of them has an HTTP status. See
[MCP servers](mcp.md) for the configuration rules behind each refusal.

`tool_call_invalid` and `tool_round_cap` are neither component faults nor caller mistakes:
the helper and the request were both fine, but the tool loop could not converge. They share exit 5 and HTTP 502 because the
same request should not be retried unchanged.

`local_unavailable` is the companion code with a different cause: no paired
helper is installed or executable, or the helper cannot answer at all. It shares
exit 5 with `native_protocol` and calls for the same remedy, the matching bundle
from the same release, but it is not a protocol disagreement. Do not retry
either one against the same installation.

PDF extraction is CLI-only through `respond --file`; HTTP endpoints do not
accept PDFs or produce these extraction errors. Their messages carry the
document's position and basename and integers only, never page text, paths or
framework diagnostics.

| Machine code | Meaning | CLI exit |
| --- | --- | --- |
| `page_out_of_range` | `--pages` asks for a page past the end; the message names the page and the document's real `page_count` | 2 |
| `unsupported_media` | The `--file` content is an image (use `--image`) or neither UTF-8 text nor a PDF | 2 |
| `extract_media_mismatch` | Hollis and its paired helper disagree about the detected media type; this is not a user-selected hint | 2 |
| `invalid_document` | The PDF is damaged, unreadable, a page cannot be rendered or recognized, or it exceeds 2000 pages (with `limit` and `actual`) | 5 |
| `extract_no_text` | The selected pages hold no text, even after OCR | 5 |
| `context_capacity` | Extracted text exceeds 128 KiB; the message gives the limit and the measured size, and nothing is truncated | 5 |

Shortcut output is bounded at 1 MiB per run and for `shortcuts list`. A run
whose stdout exceeds that cap is refused with `transport` and exit 5 rather than
delivered partially; an oversized listing is refused the same way and does not
prove a bridge missing. Narrow the request instead of expecting a truncated
answer, and do not treat the refusal as a missing bridge.

`request_declined` does **not** establish why Apple declined. It is not proof of
a safety-filter rejection, a rate limit, or a broken bridge. Hollis recognizes
only the complete recorded failed-process diagnostic. It never classifies a
successful answer by refusal-like wording. Public messages for declined and
unknown execution errors omit raw stderr, private paths and credentials. The
blocked ChatGPT image style reports this code when the Shortcut declines the
request; observed signatures have changed between builds, and the code names
the refusal, not its cause.

Except for the CLI-only PDF extraction codes above, the mapping covers direct text/image commands, stored conversations, and the
chat, Responses and image HTTP endpoints. Existing timeout, missing-resource,
rate-limit and validation codes retain their existing numeric mappings.
`hollis agent-context` lists semantic codes independently of numeric exits.

Too many images for the selected route is a parameter error, not a size error:
HTTP 400 `unsupported_parameter` naming the route's limit (three for Cloud and
Cloud Pro, one for ChatGPT and `local`). On the CLI, a one-image route given
several `--image` paths is a usage error, exit 2. Byte and pixel limits on
image input stay HTTP 413 `context_too_large`.

Pressing Ctrl-C during a Shortcuts request stops it with exit 5 and the
message "request canceled (Ctrl-C); no response was returned".

HTTP `server_busy` means Hollis's local capacity is occupied and carries
`Retry-After: 1`. Provider `rate_limited` remains distinct and has no retry
timing hint when the reset is unknown. Stop rather than tight-looping on a
provider limit; neither a one-second recovery nor another model is implied.

A timeout reports that the model request exceeded its deadline. It does not
locate the cause among approval, execution context, transport and provider,
or prove whether the provider completed the request. Keep any known execution
stage from the error, but do not diagnose the timeout as “Apple-side” from the
deadline alone. Preserve the uncertain result and do not automatically retry.
For a future request, `--timeout` can be raised up to the 120-second ceiling.
CLI timeout remains exit 7 with machine code `timeout`.

`respond --code` uses the existing missing-resource exit 3 for an empty response:
"no code to print: the response contained no code". Empty or whitespace-only
complete code blocks are skipped. If all complete blocks are empty, exit 3 says
"no code to print: the code blocks were empty", even when the response includes
prose. Without a complete fence, the whole answer is printed. Exit 7 remains
reserved for timeouts; `--code` rejects JSON and agent output.

Runtime `auto` tries Cloud once and can try On-Device once **only** after a
confirmed missing bridge or recognized rate limit. Declines, unknown failures,
empty output, timeouts, cancellations and crashes stop without a second model
call. Explicit models never fall back. The plugin chooses a concrete model
(Cloud by default), so it does not invoke runtime `auto` implicitly.

Generation failures, photographic appearance, reference similarity and
instruction-following are separate observations. A valid PNG proves image
generation completed; it does not prove photography or an exact edit.
