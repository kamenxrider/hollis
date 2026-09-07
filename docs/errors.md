# Understanding failures

Runtime 0.3.2 distinguishes an Apple request for a different description from an
unrecognized Shortcut execution failure. CLI numeric exits and HTTP statuses
are unchanged; machine-readable codes are more specific. The JSON envelope
and agent schema version remain unchanged.

| Machine code | Meaning | CLI exit | HTTP status | Next action |
| --- | --- | --- | --- | --- |
| `request_declined` | The failed process returned Apple's recorded “Try describing something different to create an image” diagnostic | 5 | 502 | Review the description; a different request is a new experiment, not a silent retry |
| `shortcut_failed` | The Shortcut executed unsuccessfully without a recognized diagnostic | 5 | 502 | Inspect the failure context before making another call |
| `transport` | Launch, staging or other transport failure | 5 | 502 for typed launch/transport errors | Check host access and process setup |

`request_declined` does **not** establish why Apple declined. It is not proof of
a safety-filter rejection, a rate limit, or a broken bridge. Hollis recognizes
only the complete recorded failed-process diagnostic. It never classifies a
successful answer by refusal-like wording. Public messages for declined and
unknown execution errors omit raw stderr, private paths and credentials.

The mapping covers direct text/image commands, stored conversations, and the
chat, Responses and image HTTP endpoints. Existing timeout, missing-resource,
rate-limit and validation codes retain their existing numeric mappings.
`hollis agent-context` lists semantic codes independently of numeric exits.

Runtime `auto` tries Cloud once and can try On-Device once **only** after a
confirmed missing bridge or recognized rate limit. Declines, unknown failures,
empty output, timeouts, cancellations and crashes stop without a second model
call. Explicit models never fall back. The plugin chooses a concrete model
(Cloud by default), so it does not invoke runtime `auto` implicitly.

Generation failures, photographic appearance, reference similarity and
instruction-following are separate observations. A valid PNG proves image
generation completed; it does not prove photography or an exact edit.
