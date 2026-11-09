# Understanding failures

Hollis distinguishes an Apple request for a different description from an
unrecognized Shortcut execution failure, and reports a missing native helper
separately from a mismatched one. CLI numeric exits and HTTP statuses follow
the table; machine-readable codes are more specific. The JSON envelope and
agent schema version remain unchanged.

| Machine code | Meaning | CLI exit | HTTP status | Next action |
| --- | --- | --- | --- | --- |
| `invalid_json_output` | JSON-object mode received something other than one valid object | 5 | 502 | Clarify with `-s`, or use Local `--schema`; no retry or repair. Shortcut JSON errors include the raw answer in `error.details.raw_response` |
| `rate_limited` | An advisory PCC reading of limit reached, Shortcuts code 1508, explicit usage-limit text, or likely-limit “can't be completed right now” diagnostic | 5 | 429, no Retry-After | Apple is rate-limiting cloud requests from this Mac. Wait before trying again (this can last hours). No automatic retry or model fallback |
| `content_refused` | Shortcuts model rejection, code 900 | 5 | 422 | Apple declined this request. Rephrasing may help; retrying will not. |
| `access_required` | Shortcuts account access failure, code 1511 | 5 | 403 | Cloud Pro is not available for this account. |
| `request_declined` | The failed process returned Apple's recorded “Try describing something different to create an image” diagnostic | 5 | 502 | Review the description; a different request is a new experiment, not a silent retry |
| `shortcut_failed` | The Shortcut executed unsuccessfully without a recognized diagnostic | 5 | 502 | Inspect the failure context before making another call |
| `transport` | Launch, staging or other transport failure, including Shortcut stdout that exceeded the 1 MiB capture cap | 5 | 502 for typed launch/transport errors | Check host access and process setup; for an oversized response, narrow the request instead of expecting a partial answer |
| `invalid_image` | The native helper could not decode an image | 5 | 400 at admission; 502 from the helper | Supply a valid PNG/JPEG |
| `unsupported_os` | Native image input requires macOS 27 or later | 5 | 502 | Use a supported system |
| `native_failed` | The native Local request failed | 5 | 502 | Inspect the fixed diagnostic; do not retry blindly |
| `unsupported_parameter` | A parameter or combination is unsupported | 2 | 400 at admission; 502 for an unexpected helper-side refusal | Use parameters supported by the selected route |
| `context_capacity` | Local context or extracted-text capacity exceeded; nothing is dropped | 5 | 413 `context_too_large` for Local inference | Agree a smaller scope |
| `unsupported_media` | Input is not a supported text/PDF document or PNG/JPEG image | 2 | 400 for image requests | Convert to a supported format |
| `native_protocol` | The paired native helper and this CLI disagree about their contract: an older or newer helper, an event that breaks the sequence, or a guided object that does not match the requested schema | 5 | 502 | Install the matching bundle from the same release; do not retry against the same helper |
| `unsupported_capability` | The paired helper speaks this protocol era but did not advertise, or refused, a capability the request needs (for example native tool calling) | 5 | 502 | Install the matching bundle from the same release; the helper is newer than the request needs or was built without that capability |
| `tool_call_invalid` | A native callback batch failed independent validation, exceeded 16 calls, or did not satisfy tool choice. No argument repair or text-call extraction occurs, and no invalid batch is dispatched | 5 | 502 | Simplify the tools or the request; do not retry unchanged |
| `tool_round_cap` | The default eight-decision budget was exhausted, or the model requested a batch on its last permitted decision; final synthesis also costs a decision | 5 | 502 | Answer from the results you have, or start a new turn; do not retry unchanged |
| `local_unavailable` | The native local model or its paired helper is unavailable: no helper is installed or executable, or it cannot answer | 5 | 400 `model_unavailable` at admission; 503 `model_unavailable` mid-run | Install the matching bundle from the same release; do not retry the same invocation |
| `model_busy` | The native model returned transient `ModelManagerError` code 1012 or an `NSError` with exact domain `FoundationModels.LanguageModelError` and code -1 | 5 | 503 | A tool decision retries once after 500 ms only before any callback is captured or executed in that decision; no tool is re-executed |
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
| `tool_result_too_large` | An MCP result exceeds the hard 1 MiB cap, or still cannot fit with partial results enabled | 5 | n/a | Narrow the request or start a new chat |
| `tool_result_too_large_for_local` | With partial results off, an MCP result cannot fit Local's next decision | 5 | n/a | Nothing was truncated. The message gives the server, tool, byte count, and `--partial-results` suggestion |
| `tool_name_conflict` | Caller and MCP tools share a rendered name | 2 | n/a | Rename the caller tool or select noncolliding servers |
| `tool_count_limit` | The combined offer exceeds 128, or caller tools exceed 32 | 2 | n/a | Select fewer servers or caller tools; no automatic pruning occurs |

JSON-object mode returns one validated JSON object on success; invalid output
fails explicitly with `invalid_json_output`. A request can still fail or return
wrong data. Shortcut routes do not enforce requested fields, types or values:
models may add keys, use `false` or `"null"` instead of null, nest answers or
change punctuation. Those differences do not cause `invalid_json_output` when
the answer is still a valid object. Local `--schema` / `json_schema` constrains
structure and types to the supported schema subset, not factual correctness.

`native_protocol` is a component mismatch, not a bad invocation: the command
was understood and the installed helper cannot serve it. It shares
exit 5 with the transport family so a script can treat a stale pairing like any
other failed execution, while the machine code names the cause. `hollis serve`
keeps the classification from its startup status probe, so a paired helper that
answers with another protocol or version is 502 `native_protocol` for every
`local` request, guided or plain, with a message that the helper does not match
this Hollis version and a pointer at the matching bundle. A missing or
unavailable helper stays 400 `model_unavailable`; a malformed payload from a
matched helper keeps the shared `native_protocol` sentence.

`unsupported_capability` is a component mismatch of the same family but a narrower cause: the helper answered validly, in the
right protocol, and simply does not offer what the request needs. It is never
used for a caller mistake such as too many images or an unsupported parameter.
Those remain usage errors; telling a caller to reinstall for a bad request
would be wrong. Install the matching bundle for a capability mismatch.

The `mcp_*` codes come from `hollis mcp` commands and explicitly attached
`respond`/`chat` tools. `hollis serve` never reads
`mcp.json` or starts an MCP server, so none of them has an HTTP status. See
[MCP servers](mcp.md) for the configuration rules behind each refusal.

The default Local MCP size failure has the same exit 5 and message in a
terminal, without a TTY, and with `--no-input`, `--json`, or `--agent`.
JSON adds `error.suggestion`, even when `--select` requests only answer fields.
For example:

```text
the result from wiki/read was 53472 bytes, too big for Local; nothing was truncated. Re-run with --partial-results to let Local answer from the first part of the result (you will be told how much it saw).
```

Opt-in through `--partial-results` or `config set partial-results on` applies
to attached Local MCP requests in interactive and automated invocations.
It does not bypass approval, validation, or the hard 1 MiB result cap.
There is no MCP execution through HTTP or batch.

`tool_call_invalid` and `tool_round_cap` are neither component faults nor caller mistakes:
the helper and the request were both fine, but the tool loop could not converge. They share exit 5 and HTTP 502 because the
same request should not be retried unchanged.

The helper's one permitted transient `model_busy` retry is separate from argument
repair, which Local does not perform, and from tool execution, which is never
retried. Both allowlisted errors share that one retry, identical input and the
original deadline. A later synthesis decision may retry after earlier tools
completed, but never after a callback in the failing decision. Other
`LanguageModelError` codes, guardrail, context and decoding failures do not
retry. A repeated allowlisted failure retains `model_busy`, exit 5 / HTTP 503.

CLI `respond` and `chat` also accept [opt-in Local `--retry`](cli.md#opt-in-local-retry)
for classified busy failures before output or tool activity, within the original
deadline. It never retries Cloud, Cloud Pro, On-Device, ChatGPT or provider limits.
HTTP and batch do not acquire this CLI retry policy.

`ModelManagerServices.ModelManagerError` code **1042** remains `native_failed`
(exit 5 / HTTP 502), never `model_busy`. On the inspected macOS framework its
error-code table maps `invalidClientIdentifier` to 1042, not a resource,
timeout or asset-loading failure. The fixed public message identifies the
rejected client identifier without exposing SDK descriptions. This private
enum is not a documented cross-version API. Six isolated Local replays of
the previously failing Chinese/Japanese cases succeeded without 1042; those
observations do not prove a Unicode cause or justify automatic retry.
Invalid or incomplete callbacks fail before approval or dispatch.
A missing named function is a usage error (CLI exit 2, HTTP 400
`unsupported_parameter`). HTTP uses the fixed message `tool_choice function
was not offered`, omitting names. Only the CLI names the missing function and
the offered names when the list is short. It costs no inference.

`local_unavailable` is the companion code with a different cause: no paired
helper is installed or executable, or the helper cannot answer at all. It shares
exit 5 with `native_protocol` and calls for the same remedy, the matching bundle
from the same release, but it is not a protocol disagreement. Do not retry
either one against the same installation.

PDF extraction is CLI-only through `respond --file`; HTTP endpoints do not
accept PDFs or produce the PDF-only errors below. `context_capacity` and
`unsupported_media` also apply to Local inference and images as listed above. Their messages carry the
document's position and basename and integers only, never page text, paths or
framework diagnostics.

| Machine code | Meaning | CLI exit |
| --- | --- | --- |
| `page_out_of_range` | `--pages` asks for a page past the end; the message names the page and the document's real `page_count` | 2 |
| `extract_media_mismatch` | Hollis and its paired helper disagree about the detected media type; this is not a user-selected hint | 2 |
| `invalid_document` | The PDF is locked, encrypted, damaged, unreadable, a page cannot be rendered or recognized, or it exceeds 2000 pages (with `limit` and `actual`) | 5 |
| `extract_no_text` | The selected pages hold no text, even after OCR | 5 |

Shortcut output is bounded at 1 MiB per run and for `shortcuts list`. A run
whose stdout exceeds that cap is refused with `transport` and exit 5 rather than
delivered partially; an oversized listing is refused the same way and does not
prove a bridge missing. Narrow the request instead of expecting a truncated
answer, and do not treat the refusal as a missing bridge.

`request_declined` does **not** establish why Apple declined. It is not proof of
a safety-filter rejection, a rate limit, or a broken bridge. Hollis recognizes
only the complete recorded failed-process diagnostic. It never classifies a
successful answer by refusal-like wording. Known Apple usage-limit diagnostics
on stdout with exit 0 are an exception: they are errors, never answers.
Shortcuts failures keep sanitized stdout/stderr excerpts (at most 2 KiB plus a
truncation marker each) in the message and JSON/HTTP `error.details` as
`apple_stdout`/`apple_stderr`. Prompt text, private paths, control characters
and credential-like values are removed. Arbitrary nested transport diagnostics
are not exposed as Apple excerpts. The
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
Below the maximum, a future request can raise `--timeout` up to 120 seconds.
At that ceiling, the hint does not suggest a larger timeout.
CLI timeout remains exit 7 with machine code `timeout`.

ChatGPT requests may wait silently for approval. Check for a macOS prompt or
notification asking to allow the request or the shortcut, and choose **Always
Allow**. If **Confirm ChatGPT requests** is on in **Apple Intelligence & Siri**
settings, each request may need approval. This is a possible cause, not a
diagnosis of a timeout. ChatGPT timeout JSON also includes this advice in
`error.suggestion`.

`respond --code` uses the existing missing-resource exit 3 for an empty response:
"no code to print: the response contained no code". Empty or whitespace-only
complete code blocks are skipped. If all complete blocks are empty, exit 3 says
"no code to print: the code blocks were empty", even when the response includes
prose. Without a complete fence, the whole answer is printed. Exit 7 remains
reserved for timeouts; `--code` rejects JSON and agent output.

Runtime `auto` tries Cloud once and can try On-Device once **only** after a
confirmed missing bridge. Rate limits, content refusals, access failures, declines, unknown failures,
empty output, timeouts, cancellations and crashes stop without a second model
call. Explicit models never fall back. The plugin chooses a concrete model
(Cloud by default), so it does not invoke runtime `auto` implicitly.

After a failed Shortcuts run, macOS unified logs may distinguish
`WFAskLLMActionErrorDomain` codes 1508, 900 and 1511. Hollis bounds the check to
five seconds, 1 MiB and the run's start minus one second through end plus two
seconds. Classification uses event timestamps inside the actual invocation so
serialized neighboring runs do not contaminate it. Only domain, code and known error keys survive parsing; no log message
text is stored. Multiple runner processes or conflicting codes are unknown,
never guessed. If the check fails or times out, text classification remains.
“Try again” alone does not mean a rate limit: on-device code 900 is a refusal.
Batch stops on the first provider failure; chat and server do not retry limits.

Generation failures, photographic appearance, reference similarity and
instruction-following are separate observations. A valid PNG proves image
generation completed; it does not prove photography or an exact edit.


## Advisory PCC quota preflight

Before each Cloud or Cloud Pro Shortcuts request, including CLI, HTTP and
batch, Hollis asks the native helper for Apple's PCC `quotaUsage`. This is a
non-generative status read: no prompt, model call, entitlement or Cloud request
is used. The check is bounded to about two seconds. Only an explicit
`limit_reached` reading stops the request (`rate_limited`, exit 5 / HTTP 429),
with the reset date if Apple supplies one. Available, approaching, unknown,
errors and timeouts all proceed normally.

`doctor --model cloud` and `doctor --model cloud-pro` report available /
approaching / limit reached / unknown and an optional reset date as information
only; they do not change readiness or test inference. The reading is advisory:
on the tested Mac it said “not near the limit” even while Shortcuts Cloud was
rate-limited with code 1508. It cannot promise a request will work. The existing
post-failure 1508 classification remains in place. Never automatically retry a
limit. Code 600 with `AppleIntelligenceRetryAfterDate` instead says Apple asked
to retry later, without asserting a quota limit.
