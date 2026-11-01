# Poolside review helper

This directory contains the trusted, standard-library-only client used by the
Poolside pull-request workflow. The workflow runs only for non-draft,
same-repository pull requests targeting `main`.

The helper first validates the open pull request against the repository, pull
request number, full base SHA, and full head SHA from the GitHub event. It then
requests the matching compare endpoint's unified-diff media type. The response
is read as UTF-8 text with a 120,000-byte hard limit. An HTTP error, redirect,
invalid text response, or oversized response fails before any partial diff is
sent to Poolside.

The Poolside call is a direct `POST` to
`https://inference.poolside.ai/v1/chat/completions` using model
`poolside/laguna-s-2.1`. The request contains text messages and `max_tokens`;
it contains no tools. Redirects are refused. Responses are rejected if they
request a tool or function call, do not finish with `stop`, or exceed the
60,000-byte review limit. Model output is stored and posted only as Markdown
text. The reviewer analyzes the diff only and does not run tests.

The review and comment jobs use clean runners. Only the fetch step receives a
read-only GitHub token, only the inference step receives `POOLSIDE_API_KEY`, and
only the final posting step receives a GitHub token with `issues: write`.
The posting job downloads one bounded JSON file, checks its exact schema and
pull-request binding, and passes the Markdown through a JSON request body.

Run the provider-free checks from the repository root:

```sh
python3 -m unittest discover -s scripts/poolside-review -p 'test_*.py'
```

These tests use fake HTTP transports. They do not call GitHub, Poolside, Apple
Shortcuts, or any other paid or live service.

Protocol references: [Poolside API overview](https://docs.poolside.ai/api/overview)
and [OpenAI API examples](https://docs.poolside.ai/api/openai-api-examples).
