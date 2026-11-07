# Shortcuts transport and model notes

These observations describe the tested macOS builds. For installation and supported systems, see [compatibility and troubleshooting](compatibility.md).

## Cloud and refusal errors

Shortcuts code 1508 is `rate_limited` (CLI exit 5 / HTTP 429), code 900 is
`content_refused` (exit 5 / HTTP 422), and code 1511 is `access_required`
(exit 5 / HTTP 403). A refusal on On-Device is not a cloud quota event.
Usage-limit text on stdout with exit 0 is never delivered as an answer.
Apple's sanitized error text is retained; unknown errors remain
`shortcut_failed` (exit 5 / HTTP 502).

Only after failure, Hollis can inspect the run's bounded unified-log window;
it keeps domain/code/known keys, never log messages. Ambiguous overlapping runs
remain unknown. Slow or unavailable logs fall back to text classification.
Provider limits never trigger automatic retries, batch continuation or a
model swap, including with `auto`. A limit may last hours; wait rather than
probing repeatedly. Doctor checks availability and advisory PCC quota status,
not remaining request counts or whether the Shortcuts cloud path can generate. See [failures](errors.md).

## How it works

```text
hollis → /usr/bin/shortcuts → Use Model → Cloud / Cloud Pro / On-Device / ChatGPT
```

Each model bridge configures incoming text, runs **Use Model**, then **Stop and Output**. Receive is input configuration, not a separate action. Hollis stages the exact rendered prompt in a private temporary `.txt` file and passes it with `--input-path`, for both text-only and image requests. Image paths follow the prompt as repeated inputs. The Shortcuts child receives no prompt on stdin. Hollis captures plain text back. It does not patch or modify `fm`, and needs no Apple model API key: the transport is the local Shortcuts app, running as you. The optional HTTP server has its own local bearer token.

Positional prompts, piped stdin, `--prompt-file` and documents keep their existing CLI behavior; staging happens after the final prompt is rendered. The staging file has a unique name and mode `0600`, and receives the bytes without trimming, reflowing or adding a newline. It is removed when the request finishes, including ordinary errors, handled cancellation and timeouts. A forced process kill, host crash or removal failure can leave the private file in the system temporary directory; removal is not secure erasure. Staging failure prevents dispatch, with no retry through stdin. The 30-second default and 120-second maximum request timeout are unchanged.

Each run's stdout is captured up to 1 MiB. A run that produces more is refused as a transport failure with exit 5: the response cannot be delivered faithfully, so Hollis never returns a truncated one. Stderr keeps a bounded head and tail so a late diagnostic can still classify the failure. The same 1 MiB bound applies to `shortcuts list`; an oversized listing is refused rather than turned into invented bridge names.

### Bridge discovery

Shortcuts UUIDs differ on every Mac, so hollis resolves each tier at runtime, in order:

1. An explicit configured bridge
2. A stable bridge name found via `shortcuts list`
3. A compiled placeholder UUID — a synthetic, well-formed identifier that names no shortcut on any Mac, kept only as an unverified candidate and never treated as proof the bridge exists

```bash
hollis config set bridge cloud "AFM Bridge - Cloud.signed"
hollis config set bridge cloud ""      # clear the override
```

If you rename a bridge in Shortcuts.app, hollis can no longer match it by name and reports it missing; point config at the new name or its UUID to restore it.

### Shortcuts quirks hollis handles

The transport rules below are not stylistic. Each one is a behaviour that was measured and had to be worked around:

| Behaviour | What hollis does |
| --- | --- |
| `shortcuts run` attached to a terminal can silently print nothing | Always captures through a pipe, never a TTY |
| Output arrives as RTF by default | Always requests `public.plain-text` |
| Empty input makes `shortcuts run` wait forever, and macOS has no `timeout(1)` | Refuses empty prompts before spawning; every run has a deadline |
| A killed run can orphan the child | Puts the child in its own process group and kills the group |
| Exit 0 with empty stdout is indistinguishable from success | Treats it as failure, never as an empty response |
| Native stdin can interpret text as another input type | Consistently stages the complete text prompt as a `.txt` input file |
| With file input, piped stdin does not reach the model | Passes the prompt text file first, followed by any image files through repeated `--input-path` arguments |
| Responses are complete, not streamed | Does not fake streaming |

The measured evidence behind these rules is summarized in [EVIDENCE.md](../EVIDENCE.md).

### The ChatGPT quirk

ChatGPT requests may wait silently for approval, including on the first run of
a newly imported shortcut. Check for a macOS prompt or notification asking to
allow the request or the shortcut, and choose **Always Allow**. If **Confirm
ChatGPT requests** is on in **System Settings > Apple Intelligence & Siri**,
each request may need approval; unattended calls can time out. A timeout alone
does not prove that approval was the cause. Do not automatically retry.

Measured on macOS 27 during development: a **signed-in** ChatGPT account made the Shortcuts extension fail with `login could not be verified`, and logging out let the bridge work. The macOS ChatGPT extension does not require an account for basic use.

Image Playground's separate **ChatGPT** style has a different scoped result:
on macOS 27.0 build `26A5425a`, the fixed, parameterized, native-Shortcuts,
and fresh-extension routes failed before inference with Apple's Shortcuts
ToolKit database sandbox denial, while the native Image Playground app worked.
A later check on 2026-11-02 saw the same style refused in under a second as
exit 5 `request_declined`, so the signature changed while the block did not.
Neither observation proves universal impossibility; Hollis does not silently
retry or substitute that route. See the
[image-generation qualification note](image-generation.md#setup).

## Model notes

The Shortcuts choices correspond, at family level, to Apple's [third-generation Foundation Models](https://machinelearning.apple.com/research/introducing-third-generation-of-apple-foundation-models):

| Hollis | Shortcuts | Model family |
| --- | --- | --- |
| `cloud` | Cloud | AFM 3 Cloud via Private Cloud Compute |
| `cloud-pro` | Cloud Pro | AFM 3 Cloud Pro |
| `on-device` | On-Device | AFM 3 Core family |
| `chatgpt` | ChatGPT | OpenAI through Apple's extension |

Apple exposes no stable backend IDs through this interface, so these mappings stay at the family level deliberately. A separate ADM 3 Cloud family powers image features such as Genmoji and is not reachable through the **Use Model** text action. [Private Cloud Compute](https://developer.apple.com/private-cloud-compute/) is documented with a 32K context window; on-device models have a smaller working context and are more sensitive to prompt length.

[Back to Hollis](../README.md)


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
