# AI review helper

This directory contains the trusted, standard-library-only client used by the
AI pull-request review workflow. The workflow runs only for non-draft,
same-repository pull requests targeting `main`.

The helper first validates the open pull request against the repository, pull
request number, full base SHA, and full head SHA from the GitHub event. It then
requests the matching compare endpoint's unified-diff media type. The response
is read as UTF-8 text with a 120,000-byte hard limit. An HTTP error, redirect,
invalid text response, or oversized response fails before any partial diff is
sent to the model provider.

The provider call is a direct `POST` to
`https://api.openai.com/v1/chat/completions`, and every provider value lives in
one marked configuration block at the top of `ai_review.py`: the URL, the
expected host, the model, the reasoning effort, the completion budget, the
socket timeout, and the secret's environment variable name. The model is
`gpt-6-luna`, from the
[model catalog](https://developers.openai.com/api/docs/models/gpt-6-luna). The
effort is `xhigh`: the model page lists `xhigh` among the efforts this model
accepts, and the
[reasoning guide](https://developers.openai.com/api/docs/guides/reasoning)
names "security and code review" as an `xhigh` use case. The guide also notes
that `xhigh` costs more latency and tokens, so the budget is sized for it:
`max_completion_tokens` is 64000, half the model's 128000-token output cap and
well above the 25000 tokens OpenAI recommends reserving for reasoning, because
that limit also covers hidden reasoning tokens. The request uses the documented
[Chat Completions](https://developers.openai.com/api/reference/resources/chat/subresources/completions/methods/create)
shape: a `developer` message carrying the reviewer instructions (the documented
replacement for `system`), a `user` message carrying the diff, and
`max_completion_tokens` rather than the deprecated `max_tokens`. It sets
`reasoning_effort` explicitly and sends no `temperature`, because reasoning
models reject a non-default value. The key is sent as a bearer token. The
request contains text messages only and no tools.

A non-streamed `xhigh` completion sends nothing back until the model finishes,
so the provider call gets its own 900-second socket timeout while the GitHub
calls keep 30. Both the review job and the smoke job allow 20 minutes, which
outlasts the socket timeout plus checkout, the GitHub calls, validation, and the
artifact upload, so a slow call ends with the helper's own diagnostic rather
than a cancelled job. The policy tests read `PROVIDER_TIMEOUT_SECONDS` from the
helper and fail if either job timeout drops below it plus a margin.

Responses are rejected if they request a tool or function call, do not finish
with `stop`, do not carry an `assistant` message, refuse without content, or
exceed the 60,000-byte review limit. `finish_reason` `length` has its own
message saying the completion budget was exhausted, so a budget problem is not
mistaken for a provider fault. Hidden reasoning output is never read. Model
output is stored and posted only as Markdown text. The reviewer analyzes the
diff only and does not run tests.

A refused request fails with the HTTP status followed by a bounded diagnostic.
At most the first 16 KiB of the error body is kept. When that body is JSON, the
message is built from its decoded form, never from the escaped bytes: the
provider's `error.type`, `error.code`, `error.param`, and `error.message`, plus
top-level `message`, `detail`, or `code`, or otherwise one excerpt of the
decoded document. When the body is not JSON, one excerpt of the text is used.
On `401` and `403` only the status and the `type`/`code` identifiers are
reported; free-text messages and excerpts are dropped. Redirects are refused
without reading their body. Every diagnostic is limited to 500 bytes. Before
pattern matching, every whitespace character is replaced with a space and every
other non-printable character, including format characters, is removed; runs of
spaces then collapse to one. What remains has any `Bearer` token, `sk-`,
`sk-proj-`, `sk-svcacct-`, `sk-admin-`, `gh[pousr]_`, or `github_pat_` value
replaced with `[redacted]`, and any run of 8 or more consecutive characters of
the configured credential replaced the same way. Because removing format
characters can close the gap a `Bearer` token was hidden behind, the pattern
accepts a token directly after `Bearer` with no separator, so an ordinary word
such as `Bearerless` is redacted too. Request bodies and response headers are
never printed.

Known limits: run redaction matches contiguous characters of the credential, so
a key echoed with a separator inserted every 7 characters or fewer never forms a
long enough run and is not redacted; the pattern-based rules still apply. A
`Bearer`-prefixed word that is not a token is over-redacted rather than kept.

The review and comment jobs use clean runners. Only the fetch step receives a
read-only GitHub token, only the model step receives `OPENAI_API_KEY`, and only
the final posting step receives a GitHub token with `issues: write`. The posting
job downloads one bounded JSON file, checks its exact schema and pull-request
binding, and passes the Markdown through a JSON request body.

Run the provider-free checks from the repository root:

```sh
python3 -m unittest discover -s scripts/ai-review -p 'test_*.py'
```

These tests use fake HTTP transports. They do not call GitHub, OpenAI, Apple
Shortcuts, or any other paid or live service.
