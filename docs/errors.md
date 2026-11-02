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
| `local_unavailable` | The native local model or its paired helper is unavailable: no helper is installed or executable, or it cannot answer | 5 | 400 `model_unavailable` at admission; 503 `model_unavailable` mid-run | Install the matching bundle from the same release; do not retry the same invocation |

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

`local_unavailable` is the companion code with a different cause: no paired
helper is installed or executable, or the helper cannot answer at all. It shares
exit 5 with `native_protocol` and calls for the same remedy, the matching bundle
from the same release, but it is not a protocol disagreement. Do not retry
either one against the same installation.

Shortcut output is bounded at 1 MiB per run and for `shortcuts list`. A run
whose stdout exceeds that cap is refused with `transport` and exit 5 rather than
delivered partially; an oversized listing is refused the same way and does not
prove a bridge missing. Narrow the request instead of expecting a truncated
answer, and do not treat the refusal as a missing bridge.

`request_declined` does **not** establish why Apple declined. It is not proof of
a safety-filter rejection, a rate limit, or a broken bridge. Hollis recognizes
only the complete recorded failed-process diagnostic. It never classifies a
successful answer by refusal-like wording. Public messages for declined and
unknown execution errors omit raw stderr, private paths and credentials.

The mapping covers direct text/image commands, stored conversations, and the
chat, Responses and image HTTP endpoints. Existing timeout, missing-resource,
rate-limit and validation codes retain their existing numeric mappings.
`hollis agent-context` lists semantic codes independently of numeric exits.

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

Runtime `auto` tries Cloud once and can try On-Device once **only** after a
confirmed missing bridge or recognized rate limit. Declines, unknown failures,
empty output, timeouts, cancellations and crashes stop without a second model
call. Explicit models never fall back. The plugin chooses a concrete model
(Cloud by default), so it does not invoke runtime `auto` implicitly.

Generation failures, photographic appearance, reference similarity and
instruction-following are separate observations. A valid PNG proves image
generation completed; it does not prove photography or an exact edit.
