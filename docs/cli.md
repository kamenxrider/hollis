# CLI usage and local data

On Shortcut text routes, explicit `--system`/`--system-file` instructions come
first within a final `Instructions:` block, followed by conversation system and
developer messages in their original order. Conversation text and its replay
closing directive come before that block, including shortened and continued
chats. These labels are not an isolation boundary. Local's native instructions
channel is unchanged. Tool protocol scaffolding and context summarization are
Local-only and keep their existing native framing. Image conversation rendering
also puts system/developer instructions after image context and revisions.

Start with the [verified install and first request](../README.md#install-verified).

## JSON object responses

`hollis respond --model cloud --format json 'Return an object with key ready.'`
requests exactly one JSON object on Cloud, Cloud Pro, On-Device or ChatGPT.
It adds a fixed JSON-only instruction and validates the complete answer.
`--format json` controls model output; `--json` and `--agent` independently
select Hollis's envelope, whose `response` remains the validated JSON text.
Without an envelope, stdout is the object itself.

The four `AFM Bridge JSON - <Route>` Dictionary bridges use `public.json`;
text bridges keep `public.plain-text`. On success Hollis returns one validated
JSON object; invalid output fails explicitly with `invalid_json_output`.
A request can still fail or return wrong data. Shortcut routes do not enforce
requested fields, types or values: models may add keys, use `false` or `"null"`
instead of null, nest answers or change punctuation. Local `--schema` constrains
structure and types to the supported schema subset, not factual correctness.
If the JSON bridge
is missing, Hollis keeps the text-bridge behavior and suggests
`hollis setup --import <route>`; envelopes include `json_bridge_hint` (under
`error.details` on invalid output). Doctor reports missing JSON bridges as
warnings, not readiness errors. Numeric spelling is preserved: Shortcuts may
serialize 19.95 as `19.949999999999999`; Hollis does not round or re-encode it.
Financial or exact-decimal callers should use decimal strings or round explicitly.

Hollis trims outer whitespace. One whole fenced block, opened with exactly
` ```json ` or ` ``` ` (without the spaces shown here), is unwrapped only if
its entire content is an object. Surrounding prose, arrays, null, malformed
JSON and empty output fail with exit 5, `invalid_json_output`. No fields are
repaired and no retry is made. Machine errors preserve the original text at
`error.details.raw_response`; handle it as untrusted, potentially private data.
The exact error is: “The model did not return a valid JSON object. Use -s to
clarify the required object, or use --model local --schema for guided output.
No retry was attempted.” Human errors also include a single-line raw-answer
excerpt of at most 120 characters; machine errors retain the complete raw text.

This is complete-only and cannot accompany `--stream`, `--code`, `--schema`,
tools, `--count-tokens` or images. Local refuses `--format json` with exit 2:
use `--model local --schema <file>` for native guided output.

## Import and bridge warnings

`setup --import` prints “Choose Add Shortcut in each window, then run hollis
doctor” only after opening at least one import window. When every selected
bridge is installed, it instead reports: “All selected bridges are already
installed; nothing to import. Run hollis doctor to check them.”

`doctor` and `doctor --json`, including `--model`, warn about exact duplicate
bridge names with reason code `duplicate_bridge_name`. JSON `warnings` entries
contain `reason_code`, `name`, `count` and `message`. This is a warning only:
routing and exit status do not change, and Hollis never deletes a Shortcut.
Keep one copy of each bridge in the Shortcuts app. `setup --import --force`
warns before opening that replacing a Shortcut can leave duplicate names.

## Checking for updates

```sh
hollis update
hollis update --json
hollis update --apply          # Homebrew only; asks first on a terminal
hollis update --apply --yes --no-input
```

Only `hollis update` checks for updates. Hollis does not check at startup, in the
background, on a schedule or automatically. The command makes one explicit request to the public
GitHub latest-release API for `kamenxrider/hollis`, with a `hollis/<version>`
User-Agent, a 10-second timeout, no redirects and no retry. This is the same
repository and `v<version>` tag convention used by the verified installer.
Drafts and prereleases are ignored, including semver prerelease tags.
Versions use SemVer 2.0.0 precedence, not lexical ordering; build metadata
does not affect ordering. A development build newer than the published stable
release is reported as newer, never offered a downgrade.

Human output includes current/latest versions, availability, release URL,
installation method and the exact upgrade command or manual download link.
JSON fields are `current_version`, `latest_version`, `update_available`,
`version_comparable`, `status`, `release_url`, `install_method`,
`upgrade_command` and `applied`. `--agent` uses the standard `meta`/`results`
envelope; `--select` selects these fields. `status` is `update_available`,
`up_to_date`, `newer_than_published` or `unknown`. An unversioned source build
(such as `dev`) reports `unknown`, `version_comparable: false` and
`update_available: false`.

Installation detection resolves executable symlinks: a `Cellar/hollis/<version>`
layout is Homebrew, including nonstandard prefixes. No `brew` process is
started for detection. The installer leaves no receipt: `~/.local/bin/hollis`
with an executable sibling `hollis-native` is treated as the default installer
layout, not proof of provenance. A standalone binary, custom installer
`--bin-dir`, missing helper or unrecognized layout is `manual`.

For Homebrew the command is `brew upgrade kamenxrider/tap/hollis`.
`--apply` runs it without a shell only when a newer stable release is available.
It asks first on a terminal; non-terminal, `--json`, `--no-input` and `--agent`
invocations require explicit `--yes`. Brew output goes to stderr so JSON stdout
stays valid. `applied: true` means brew exited successfully; the version fields
describe the check before upgrading, not a post-upgrade verification.
Installer and manual installs refuse `--apply` with exit 2 and print the
documented installer command or release link instead. Hollis never runs that
installer command or pipes a download into a shell.

An available update is **exit 0**, as are same/newer/unknown current versions.
No stable release (HTTP 404, or a draft/prerelease returned as latest) is exit
3, code `missing`; a 404 can also mean the repository is unavailable. GitHub
403/429 is exit 5, code `rate_limited`; offline/HTTP failures are exit 5,
code `transport`; invalid JSON, version or release URL is exit 5,
code `update_response`; timeout is exit 7, code `timeout`.
Failed brew execution is exit 5, code `update_apply_failed`. These reuse the
existing numeric exit table; machine codes distinguish failures sharing exit 5.
Before applying an upgrade, close running Hollis chat and serve processes
and follow [database upgrade and recovery guidance](#database-upgrades-and-recovery).

## Everyday use

Run `hollis examples` for [job recipes](../examples/recipes.md), including
summarizing notes, drafting replies, reviewing code and saving chats. It prints
embedded text only. `--json` or `--agent` returns `format` and `content`;
`--select` can select those fields. Copying a recipe is a separate action:
the guide names its model requests and file writes.

```bash
hollis respond "Summarize this repo in one sentence"
hollis "Explain closures in Go"          # same as hollis respond "..."
printf 'Explain closures in Go' | hollis respond
hollis respond --model cloud-pro "Analyze this bug"
hollis respond model cloud-pro "same thing, model before the prompt"
hollis respond --timeout 90s "A question worth waiting for"
```

The prompt comes from the argument, `--prompt-file`, or stdin, so pipelines work. Each `respond` call is stateless. Default timeout is 30 seconds, ceiling 120. Hollis rejects a rendered prompt over 128 KiB before invoking Apple. For Shortcuts routes, all rendered text reaches Shortcuts through a private temporary `.txt` file; see [transport and cleanup details](shortcuts.md#how-it-works).

### Opt-in Local retry

`respond` and `chat` accept `--retry` for three extra attempts after the first,
or `--retry=N` for 0–5 extra attempts. Use `=` for the count: `--retry 7`
keeps `7` as the prompt, not a retry count. Retry is off unless this flag is supplied.
The effective model must be `local`, selected explicitly, through `HOLLIS_MODEL`
or a saved default, or retained by a continued Local chat. Other routes,
including `auto`, fail with exit 2 before discovery or inference. Image
generation and token counting refuse this flag; batch and HTTP are unchanged.

The refusal names every concrete non-Local text route:

```text
--retry works only with --model local; Cloud, Cloud Pro, on-device and ChatGPT requests are never retried
```

Only classified transient `model_busy` failures retry. Rate limits, refusals,
guardrails, context capacity, invalid schema/output, usage errors and timeouts
never retry. Any streamed text, partial structured answer or captured tool
callback closes the retry window. Tool/MCP turns may retry only their first
decision, with affirmative evidence that no callback was captured; completed
decisions, dispatched calls and subsequent synthesis are never replayed.
The native helper's existing single pre-callback busy retry is unchanged.

Backoff is 1, 2, 4, 8, 8 seconds, interruptible immediately with Ctrl-C.
All extra attempts and waits share the original request/turn `--timeout`
budget; retries do not restart the timeout. Respond's discovery and PDF
extraction retain their existing separate budgets. Chat continuation locking
uses the same turn budget. Stderr notes name the upcoming attempt:
`hollis: Local model busy; retrying in 1 s (attempt 2 of 4)`.
`-q` hides these notes. Successful JSON/agent output adds numeric `attempts`
only when `--retry` is supplied (1 on first-attempt success); this counts
CLI attempts, not the helper's internal retry or ordinary tool-loop decisions.

```sh
hollis respond -m local --retry "Reply OK"
hollis respond -m local --retry=2 --json "Reply OK"
hollis chat -m local --retry "Give me two ideas"
```

### Instructions, one-shot conversations and Local checks

`respond --system 'Be concise'` (`-s`) or `--system-file instructions.txt`
sets instructions; the two options are mutually exclusive. Local uses real
native session instructions. Cloud, Cloud Pro, on-device and ChatGPT Shortcuts
accept only one text, so Hollis puts the `Prompt:` block first and the
`Instructions:` block after the prompt (or the whole replayed conversation).
These models follow the later text, so the instruction comes last. Those
routes do not have a separate instruction channel.

`respond --messages conversation.json` or `--messages -` reads a one-shot
OpenAI messages array. Roles are system, developer, user and assistant; developer
becomes system, content follows the HTTP text rules, and the last message must
be user. Local uses real transcript entries and native instructions. Other
routes replay the labelled conversation as persistent chat does. This option
cannot accompany positional text, `--prompt-file`, a stdin prompt, or attachments.
Instruction/conversation options currently cannot accompany caller tools or MCP.

`respond --count-tokens 'Hello' --system 'Be brief' --json` counts with Apple's
Local tokenizer without generating an answer. Text/PDF `--file` input is
supported. Output fields are `tokens`, `context_size`, `answer_reserve` (default
1024; override with `--answer-reserve`, range 0–1048576) and `fits`. The count includes the native
transcript framing and instructions. Fit means input plus reserve is at most
the Local context size; it does not guarantee the model will use that many
answer tokens. An explicit non-local model is a usage error (exit 2), even if a
non-local model is the saved default. Counting cannot accompany images,
sampling, tools, schema, streaming, code extraction or permissive mode.

`respond --model local --permissive` uses
`SystemLanguageModel(guardrails: .permissiveContentTransformations)` for
transforming supplied text, such as summarizing, translating or rewriting.
For String output, Apple's guardrail-violation error is suppressed, but the model
can still return a refusal. This is not unrestricted generation or a guarantee
of compliance; structured output keeps normal guardrails. Other routes refuse
this flag with exit 2. A short stderr note appears once per run unless quiet.
See [Apple's API documentation](https://developer.apple.com/documentation/foundationmodels/systemlanguagemodel/guardrails/permissivecontenttransformations).

`--quiet` (`-q`) hides notes, hints and progress on stderr, not errors or the
answer. It also works with saved chat; interactive approval is never bypassed.

### Local speed test

```sh
hollis benchmark --runs 3
hollis benchmark --runs 3 --json
```

`benchmark` always uses native Local. `--model`/`-m` accepts only `local`; any other
value exits 2 before a helper starts. The command ignores saved model/timeout
defaults and environment overrides. No user input, chat history, tools, Shortcuts, Cloud,
Cloud Pro, ChatGPT or network requests are used, and no answer text is stored.
Unavailable Local reports the existing `local_unavailable` error (exit 5).

`--runs N` selects warm attempts per case (default 5, range 1-20). The up-front
stderr notice gives the bounded request count: `1 + 4*N` (13 for `--runs 3`,
at most 81), plus a non-generative availability call and protocol probe.
One short first call is separate from warm statistics. The four warm cases
are an exact `OK` answer, a streamed ~100-word explanation, guided JSON with
`status=OK` and `count=3`, and token counting without generation. All calls
are serial, with 30 seconds per request and no retry or fallback. Each request
starts a helper: warm means warmed model/OS caches, not a persistent helper.
Stream success checks only a 70-140-word length, not factual quality.

The header includes Hollis/macOS versions and build, hardware model and chip,
measured Local context size, and 1-minute load average when available. Results
vary with load. Helper start/status (including platform/protocol checks) and
the first generation are reported separately; these are cold-ish timings,
not a promise that the system model was unloaded before the run.

Each case reports success/attempt counts and median, nearest-rank p95
(`sorted[ceil(0.95*N)-1]`), minimum and maximum wall time, including failed
attempts. The streamed case also records time-to-first-nonwhitespace-text.
The table shows median first-text time and output tokens/second; JSON includes
all samples and all four statistics for these measurements. Throughput uses
only real SDK output token counts divided by total request wall time, including
helper overhead; absent counts stay `null`/`-`, never character estimates.
Failed/partial outputs do not contribute usage or throughput.

`--json` emits stable report schema version `"1"` with `model`, `header`,
`runs`, `planned_requests`, `attempted_requests`, `interrupted`,
`percentile_method`, `helper_start_ms`, `first_call` and `cases`. Case rows
contain `name`, `check`, `attempted`, `succeeded`, `wall_ms`, `first_text_ms`,
`output_tokens_per_second` and `samples`; statistics contain `median`, `p95`,
`min` and `max`. Samples contain `run`, `success`, `wall_ms`, `first_text_ms`,
`usage`, `output_tokens_per_second`, `token_count`, `error_code` and `error`.
The report contains no generated answer text. `--agent` wraps the same report under
`results` with the usual `meta`; `--select` works for declared report fields.
`--quiet` hides the up-front stderr notice, not the results or failed checks.

Ctrl-C cancels the active helper, immediately prints partial results, and exits
130. Wrong or failed answers remain visible and cause exit 5 after the report;
they are not retried, hidden or reported as successes.

### Opt-in conversation shortening

`chat` (including `--continue` and interactive sessions) and `respond --messages`
accept `--context-strategy strict|drop-oldest|sliding-window|summarize`.
The default is `strict`: an oversized conversation fails without dropping
anything. Shortening changes only the request for the current turn. Existing
saved messages are never edited or deleted, and generated summaries are not saved.

- `drop-oldest` preserves system/developer instructions and keeps the newest
  complete user turns that fit. A turn begins with a user message and includes
  its following assistant replies; the current user message counts as a turn.
- `sliding-window` keeps at most `--context-max-turns N` recent user turns
  (default 6, range 1–256), plus instructions. If that window is still too
  large, it errors rather than dropping more turns.
- `summarize` is Local-only; other routes refuse it with exit 2. When shortening
  is needed, one extra Local call summarizes the dropped older turns. A labelled
  `Context summary (costs one extra Local model call):` note precedes the retained
  conversation. The summary is limited to 256 output tokens and its source must
  fit in one Local call; oversized sources fail, never recursively summarize or
  silently lose part of the source.

With `--retry`, the extra summary call is not retried: a summary failure stops
the request. Only the final answer gets the selected extra-attempt budget,
under the same original timeout and output/tool safety rule. A successful
summary is reused rather than generated again when the final answer retries.
JSON `attempts` counts final-answer attempts, not the separate summary call.
`-q` hides both context and retry notes; omitted-message JSON disclosure remains.

Local fitting uses Apple's native tokenizer and the reported context window
(8,192 tokens on the supported system model). `--answer-reserve` reuses the
token-counting reserve (default 1024); it is available on both commands and
is not an output-token limit. Summarization also leaves 512 tokens of headroom
for its note and rechecks the final request. Other routes use Hollis's existing
128 KiB rendered-prompt limit, not an estimated provider token window.
An oversized newest user message or instructions still returns `context_capacity`.
The saved chat's 256-message storage limit and `--messages` 128 KiB input-file
limit remain unchanged. Shortening cannot accompany token-count-only requests,
tools/MCP, image input or image generation.

Every shortened human request prints a stderr note unless `-q` is set:

```text
hollis: context: sent the newest 6 of 14 messages (8 older dropped) with drop-oldest to fit Local's 8,192-token window
```

Summary notes add `; summary costs one extra Local model call`. JSON and agent
results include `context: {strategy, sent_messages, dropped_messages, summarized}`.
Counts refer to original messages, including preserved instructions, and exclude
the generated summary note. When messages were dropped, `-q` and `--select`
cannot hide this JSON disclosure. No-drop opt-in results still include `context`
with zero drops. The default strict result shape is unchanged.

```sh
hollis chat --continue <id> --context-strategy drop-oldest "What next?"
hollis respond --model local --messages conversation.json \
  --context-strategy sliding-window --context-max-turns 3 --answer-reserve 1024
hollis config set context-strategy drop-oldest
hollis config get context-strategy
hollis config unset context-strategy
```

An explicit strategy overrides the saved setting. The saved policy applies only
to text chat and `respond --messages`, never ordinary one-shot prompts,
`--count-tokens`, batch or HTTP. Pass `--context-strategy strict` to restore the
hard stop for one invocation. The HTTP API does not accept a context-strategy
extension.

### One attached image

With exactly one input `--image`, `respond` and one-shot saved `chat` detect
the explicit English words `images`, `pictures` and `photos`. They drop the
plural `s`; directly preceding `these`/`those` become `this`/`that`, and
`both`/`two`/`all` become `the`. Hollis preserves capitalization and intervening
spaces. This is a conservative word table, not a grammar or pronoun
rewrite: surrounding verbs and independent pronouns are left alone.

In a human Terminal, Hollis warns, shows the suggested prompt and asks
`Use this wording? [Y/n]`. Enter, Y or yes accepts; no, EOF or an invalid
answer keeps the original. Scripts, redirected output, JSON, agent and
`--no-input` calls never wait: they apply the change, disclose it on stderr,
and include `prompt_adjusted: true` in successful JSON output. Pass
`--keep-prompt` to disable detection and send the original wording.
Singular prompts and zero/multiple images are unchanged. No environment
variable or saved default controls this feature.

For a one-shot saved chat, attach `--image` with an explicit image-capable
model (for example `--model local`). Hollis adjusts only the new user's text
before normal history framing. It saves the sent text and an adjustment flag
atomically. The attachment is not replayed on later turns.
Input images are unavailable in the interactive chat loop or together with
MCP/image generation. Batch never rewrites its manifest-bound instruction;
planning records a warning instead.

### Saved defaults and environment

```sh
hollis config set model local
hollis config get model
hollis config set timeout 45s
hollis config get timeout --json
hollis config unset timeout
hollis config unset model
hollis config unset bridge cloud
hollis config unset image-bridge sketch
hollis config set partial-results on
hollis config get partial-results
hollis config unset partial-results
```

`config get` and `config unset` accept `model`, `timeout`, `partial-results`, `context-strategy`, `bridge <tier>` and
`image-bridge [style]`. With no style, the last key names the general image
bridge; a style names only its fixed-style override. Unset is idempotent and
preserves unrelated settings under the config lock. Get reads saved values,
not the effective environment or built-in default, and does not create config
or change permissions. An unset value prints a blank line (exit 0); JSON
reports `value: ""` and `configured: false`. Both support JSON, agent output
and field selection.

For `respond` and new text chats, model priority is positional
`model <tier>` > `--model` > `HOLLIS_MODEL` > saved model > built-in default.
A continued chat always uses its stored model, ignoring `HOLLIS_MODEL`.
For `respond`, `chat` and `image generate`, timeout priority is
`--timeout` > `HOLLIS_TIMEOUT` > saved timeout > command default (30 seconds
for text, 120 seconds for image generation). All durations must be greater
than zero and at most 120 seconds. Empty or invalid environment values are
usage errors unless a higher-priority explicit option overrides them.
Environment defaults are not saved. Batch manifests and the HTTP API retain
their own model and timeout contracts; these defaults do not alter them.

Local MCP results that cannot fit stop by default with exit 5 and code
`tool_result_too_large_for_local`, the full byte count, and a suggestion.
No result is truncated. `respond --partial-results` and `chat --partial-results`
explicitly permit disclosed prefixes. `config set partial-results on` saves
this opt-in. `off` or `unset` restores the hard stop. Explicit
`--partial-results=false` overrides a saved `on`. No environment variable
controls this setting.

Scripts, no-TTY, `--no-input`, JSON, and agent calls use the same default.
JSON failure output includes `error.suggestion`. Opt-in also works in those
modes, subject to normal approval. It adds a stderr notice and JSON
`tool_results_bounded` metadata, and saves each full result privately at mode
0600. HTTP and batch never execute MCP tools. See
[MCP servers](mcp.md#opt-in-to-partial-local-results) for the disclosure and storage rules.

### Prompt or command

Bare `hollis` on a terminal prints a concise description and examples
without reading stdin. `hollis --help` keeps the complete command list, flags,
environment and exit codes; its parsing pointer leads here. Non-terminal
bare-command and piped `respond` behavior is unchanged.

`-m` means `--model` on respond, chat and batch plan. `-f` means repeatable
`--file` on respond (chat and batch have no `--file` flag).
`-s` means `--system`, and `-q` means `--quiet`.

A non-streaming respond/chat run shows one `Waiting for Cloud…`-style line
after about one second, on stderr only when it is a terminal. It clears before
the answer or an error. JSON, agent, quiet and no-input modes suppress it;
pipes never receive terminal escapes. There is no animation, and Ctrl-C
cancels normally. Help links are web URLs; report issues at
https://github.com/kamenxrider/hollis/issues.

The first positional argument decides what runs. Flag values are not positional arguments. Hollis uses the root persistent and `respond` flag definitions: a value-taking flag consumes the next argument unless written `--flag=value`; boolean flags do not consume the next argument.

| Input | What runs | Example |
| --- | --- | --- |
| Names a command | That command | `hollis models`, `hollis chat "hello"` |
| Sole positional argument within edit distance 1 of a command name, or distance 2 for command names of 7 or more characters (case-insensitive) | Nothing: usage error, exit 2, with the closest command named | `hollis chta`, `hollis respnd --json`, `hollis modles`, `hollis doctr` |
| Anything else | `respond`, with the whole command line as its arguments | `hollis "Explain closures"` behaves like `hollis respond "Explain closures"` |
| Follows `--` | `respond`, with the text after `--` taken literally | `hollis -- "--chat"` sends the prompt `--chat` |
| Root `--help` or `--version` with extra positional arguments | Existing root argument validation, exit 2 for an unknown command | `hollis --help explain`, `hollis --version explain` |
| Lone positional `-` | `respond`, with the literal prompt `-` | `hollis -` does not read stdin |

Respond's own flags may come before the prompt, so `hollis --model local "hello"` and `hollis --model=local "hello"` both work. Values such as `--prompt-file chat` and `--image setup` are paths, not commands. With an exact command, leading flags belong to that command: `hollis --model cloud chat --help` shows chat help. A prompt whose first positional argument is exactly a command name must use `respond` or `--` explicitly. Cobra's hidden `__` completion commands are never reinterpreted.

An edit is an insertion, deletion, substitution, or adjacent letter swap. Only a sole positional argument can be refused as a near-miss. `hollis what is a closure`, `hollis hello`, `hollis make a plan` and `hollis tell me a joke` are prompts. So is `hollis respod hello`: multiple positional arguments are not typo-checked. To send a refused word literally, use `respond` or `--`. The error preserves leading flags in the prompt form:

```
$ hollis --json respnd
{"error":{"code":"usage","exit_code":2,"message":"unknown command \"respnd\": did you mean \"respond\"? To send it as a prompt instead, run: hollis --json respond \"respnd\""}}
```

The `config` and `chats` groups also suggest close subcommand names, such as `hollis config shwo` and `hollis chats lsit`. The `image` and `batch` groups reject unknown subcommands with exit 2 but do not provide these suggestions. JSON and agent output carry the usual `error` object with `"exit_code": 2`.

After `--`, even `model <tier>` is literal: `hollis -- model cloud hello` sends `model cloud hello` without selecting Cloud. Root help and version flags retain their existing strict argument checks; ask for prompt help with `hollis respond --help`.

A lone positional `-` is literal prompt text. `--prompt-file -` reads the instruction from stdin; it is refused on a terminal under `--no-input` or `--agent`. To read piped stdin, use `--prompt-file -` or omit the prompt and pipe to `hollis respond`.

## Native local streaming

`hollis respond --model local` and `hollis chat --model local` use Apple’s native
on-device SDK and stream human output when stdout is a terminal. Use `--stream`
to stream explicitly to a pipe, or `--stream=false` for a complete answer. JSON
and agent output never stream, even in a terminal; `--stream=true` with either
is rejected. Terminal control characters are rendered visibly. No conversation
is silently shortened to fit native context capacity. `respond --schema` asks
`local` for a guided object; the supported subset, the pre-call rejections and
the complete-only rule are in [native local](native-local.md#guided-output).

## Text documents

```bash
hollis respond --prompt-file instructions.txt
printf 'Summarize the notes' | hollis respond --prompt-file -
printf 'Summarize the notes' | hollis respond --file notes.md
hollis respond "Summarize the differences" --file first.md --file second.txt
hollis respond --file report.pdf "Summarize the report"
hollis respond --file scan.pdf --pages 2 --pages 5-7 "List the dates"
```

`--prompt-file` reads the instruction exactly as UTF-8 text; `--prompt-file -` reads it from stdin. Repeat `--file` to append local UTF-8 text documents of any extension in order, with their basenames and explicit boundaries. The instruction, boundaries and document contents together must fit the 128 KiB prompt limit; Hollis rejects oversized input without truncation. Empty, invalid UTF-8 and nonregular files are rejected.

Content, not the extension, decides how a `--file` is read. A PDF is extracted on this Mac by the paired native helper (`hollis-native`, macOS 27): each page's text layer is used, and a page without one is recognized with on-device OCR. Extraction calls no model, and the extracted text then goes to the selected route like any other document, framed with one `--- Page N ---` marker per page. A PNG or JPEG passed to `--file` is refused with a pointer at `--image`; other binary content is refused as `unsupported_media`. A valid UTF-8 text file named `.pdf`, or without an extension, is still accepted as text. PDF extraction is CLI-only through `respond --file`; the HTTP endpoints do not accept PDFs or emit extraction errors.

`--pages` selects pages of exactly one PDF `--file`: a page (`3`), a range (`3-7`), or a comma list (`1,4-6`). It is repeatable, 1-based, and the selection is read in ascending order with repeats removed. A malformed token, page 0 or a reversed range is refused by name (exit 2, `unsupported_parameter`) before anything is read, and so is a selection over 2000 pages. A page past the end of the document is `page_out_of_range` (exit 2) with the document's real page count. A document over 2000 pages or a damaged one is `invalid_document`, a document with no text in the selected pages is `extract_no_text`, and extracted text over 128 KiB is `context_capacity` with the limit and the measured size (all exit 5). These messages carry numbers, never page text or paths. The framed prompt must still fit the 128 KiB prompt limit, without truncation. If the model declines a request that carried extracted text, the error says so: Apple's guardrails can refuse such text, and the refusal does not mean the file was unreadable.

Choose exactly one instruction source: positional text, `--prompt-file`, or piped stdin. With `--file` or `--image` and no positional text or `--prompt-file`, piped stdin supplies the instruction, so `cat task.txt | hollis respond --file notes.md` works without `/dev/null`. Passing positional text and piped stdin together with file input, or a regular `--prompt-file` path and piped stdin, is refused instead of silently dropping one source; positional text alone still ignores an incidental pipe, as before. A pipe held open with no data is not waited on: Hollis checks stdin briefly, then reports the missing instruction. Documents cannot be mixed with `--image` in one request. Documents are prompt content; their boundaries do not isolate untrusted instructions.

## Images

`respond` accepts PNG and JPEG files through native local or the existing bridges:

```bash
hollis respond --image photo.jpg "What is this?"
hollis respond --model local --image photo.jpg "What is this?"
hollis respond --model cloud-pro --image a.png --image b.png "Compare them"
```

An image request with no selected or configured model defaults directly to Cloud.
A saved `auto` behaves like an unset model for images and also selects Cloud.
Explicit positional, flag or environment `auto` is still refused.
Cloud and Cloud Pro accept repeated `--image`; ChatGPT and native `local` accept
exactly one image. Local needs macOS 27 and a vision-capable model, supports
complete and streaming responses, and refuses images with tools or `--schema`.
Explicit `auto` and On-Device are rejected for images because the tested
On-Device Shortcut ignored the pixels, making automatic fallback unsafe.

Images must be direct regular PNG/JPEG files, at most 64 MiB and 64 million pixels each. Hollis rejects symlinks in the file or its parent directories, apart from macOS's standard root-owned `/var`, `/tmp` and `/etc` aliases. Use the real path for an image reached through a custom directory link. The runner validates a private byte snapshot and passes only that snapshot to the selected backend; staged images are removed after success, failure or cancellation. Native local applies EXIF orientation and downsizes the longest side to 4096 pixels before attaching the pixels, without an OCR text fallback.

When images are present, give the prompt as an argument, with `--prompt-file`, or through piped stdin. For Shortcuts routes, Hollis passes the private prompt file first, followed by the images as repeated inputs, and cleans up staging after the run subject to the limits described above. One-shot saved chat also accepts `--image`; its pixels are not replayed on later turns, and interactive chat does not accept attachments. The same model tiers accept inline images through the API, described in the [API guide](api.md#inline-image-input).

## Persistent chats

Shortcuts model calls are stateless. Hollis stores conversations locally and replays the transcript each turn:

When human chat output is attached to a terminal, Hollis renders terminal
control characters visibly so a model response or stored message cannot alter
terminal state. JSON output and redirected human output preserve the original
content for deterministic scripts.

```bash
hollis chat "Remember the codeword VANTA-ORBIT-7319"
hollis chat --continue <id> "What was the codeword?"
```

Run `hollis chat` with no argument in a terminal for an interactive session. Blank lines are skipped, Ctrl-D ends it, and `--continue <id>` resumes an existing conversation:

```
$ hollis chat
New chat · auto
Type help or /help for commands; Ctrl-D leaves.
> What is a closure?
< A closure is a function that captures variables from its surrounding scope.
> Give me one in Go.
< func counter() func() int { i := 0; return func() int { i++; return i } }
```

Interactive sessions accept a few commands instead of sending the line to the model:

| Input | Effect |
| --- | --- |
| `help`, `/help` | Show the commands |
| `/image <prompt>` | Generate an image (needs `--output` and a style or bridge) |
| `quit`, `exit`, `/quit`, `/exit` | Leave the chat; Ctrl-D also leaves |

Anything else is a prompt. Commands are matched exactly after trimming and case folding, so `/imageology` and `help me` still go to the model.

To manage saved chats:

```bash
hollis chats list
hollis chats show <id>            # metadata plus every message
hollis chats search VANTA-ORBIT   # full-text (FTS5)
hollis chats rename <id> "New title"
hollis chats delete <id> --yes
```

The whole search query is treated as one phrase, so hyphenated tokens like `VANTA-ORBIT` match verbatim. Archived conversations are skipped.
`chats search` accepts `--model` to filter by tier and `--limit`
to cap the result count (default 20).

Empty human output suggests one next command:

```text
$ hollis chats list
No chats yet. Run: hollis chat "hello"
$ hollis chats search missing
no chats match "missing"
hint: run 'hollis chats list' to see stored chats
```

An empty list writes the hint to stdout and exits 0. In JSON it returns an
empty array (`[]`), wrapped under `results` with `--agent`.

A no-match search leaves stdout empty in text mode, writes the existing error
message and then a `hint:` line to stderr, and exits 3. With `--json` or `--agent`, it
returns the existing not-found error envelope (`error.code: "missing"`) on
stdout with exit 3; `--agent` also includes `meta`. Hints appear only in human
output, never in JSON.

A continuation always uses the model stored with that conversation. Combining `--continue` with a positional model or explicit `--model` is rejected. Hollis stores complete turns atomically and preserves complete history. The 256-message storage limit remains a hard stop. Oversized rendered requests fail by default; [explicit context strategies](#opt-in-conversation-shortening) can shorten the current request without editing saved messages.

## Code output

`--code` prints only the first complete fenced code block with content, skipping empty or whitespace-only blocks, so an answer can go straight into a file:

```bash
hollis respond --code "Write a Go function that reverses a slice" > reverse.go
hollis respond --code --model local "A shell one-liner that lists PNG files"
```

A response without a complete fence is printed in full, with one trailing newline. An empty response exits 3 with "no code to print: the response contained no code". If complete fences exist but all are empty or whitespace-only, it exits 3 with "no code to print: the code blocks were empty", even when prose surrounds them. Exit 7 remains reserved for timeouts. `--code` is complete output only: it is refused with JSON or agent output, `--stream` and `--schema`, and a terminal local run does not stream.

## Scripts and agents

```bash
hollis respond --agent "Name three Go testing tips"   # JSON + non-interactive
hollis models --json
hollis doctor --json --select bridges
hollis agent-context                                  # machine-readable CLI description
```

### Model discovery and next steps

`hollis models` lists all text routes, even when unavailable. The configured
default has a `(default)` marker, or `auto` is marked when no default is saved.
Unavailable rows explain the missing prerequisite, such as a Shortcut not
imported, an unsupported macOS version, or native availability/settings.
Configured references that discovery cannot verify are labeled unverified,
not assumed missing. A discovered bridge whose platform could not be checked is
labeled unchecked.

`--json` and `--agent` add `default` and `available` booleans. Concrete routes
also include `readiness`; non-ready routes include `reason_code` and a short
`reason`. `auto` reports availability when either Cloud or On-Device is
callable, and includes a reason when neither is available. An unknown platform
takes precedence over discovery failures for both auto and concrete routes.
When the platform is known and Shortcuts
discovery fails, that reason says bridge presence is unknown, matching the
Cloud and On-Device rows. A helper that answers but does not match this Hollis
version is `helper_mismatch`, distinct from a missing helper or malformed or
incomplete status data (`native_protocol`). If Hollis cannot read the
configuration, rows omit `default`. Existing row fields
and metadata schema version 2 are unchanged. A configured UUID may remain
`available: true` while its readiness is unknown: it is callable but not
discovery-verified. `--select model,default,available,reason` can project these
fields. `--model local` limits discovery to native availability: it reads
configuration without changing it or its permissions, adds no CLI platform
probe, and still reports native status when the configuration is invalid. The
native runner retains its own bounded `sw_vers` platform check.

Discovery uses read-only status and bridge diagnostics, never an inference
request. Availability does not establish a successful model response.

Human `hollis doctor`, including `--model <route>`, ends with one `Next:`
action. The overview prefers a ready native route, then the first ready
Shortcut route. Otherwise it suggests a command for the first unresolved
native prerequisite; when this macOS can never run local, it moves on to the
first Shortcut route. Selected-route diagnostics target only that route. The
suggestion acts on the observed state: the
`hollis config set bridge <route> <name-or-uuid>` line for a missing bridge,
Shortcuts for a pending import, and System Settings for an unavailable model.
For missing transport or unsupported platforms, doctor suggests repairs instead
of cycling through other diagnostic commands. For native repair, use the same
installation source to restore the matched CLI and sibling helper: the formula
for Homebrew, the same revision for source builds, or the matched release
bundle for downloads. Doctor does not guess installation ownership or select a
different installer or release. For example, when local is ready:

```text
Next: hollis respond --model local "hello"
```

The action is only suggested, never executed. Selected-doctor JSON and models
use the same native diagnostic reason codes. Plain `hollis doctor` exits 0 as
an overview, including when a bridge is missing even if local is ready.
Use `hollis doctor --model <route>` for a nonzero exit when the selected route
is unready.

JSON output carries both `model_requested` (what you asked for) and `model_used` (what
answered). When `auto` falls back from Cloud to On-Device, the fields differ
because the two routes are not interchangeable. Hollis reports the substitution. In
human mode that fallback prints one line to stderr, leaving stdout clean for
pipelines. The local API reports the serving tier in its `model` field, as
OpenAI does.

`--agent` is shorthand for `--json --no-input` on data commands. In `--no-input` mode hollis never waits on a terminal, and destructive commands require explicit confirmation flags (`hollis chats delete <id> --yes`). Long-running `serve` and shell `completion` support human output only and reject JSON/agent mode clearly.

`--select` projects declared result fields in both `--json` and `--agent`
output, including each row of an array. Use comma-separated names or dotted
paths, for example `--select model,capabilities.streaming` on `models`.
Names are case-insensitive; spaces around comma-separated names are ignored.
Nested array paths project each element, not a numeric index. Selecting a
parent includes its whole value. Agent `meta` and diagnostic `error` payloads
are never removed by projection. `agent-context` keeps its intrinsic JSON
shape and supports selection with `--json` or `--agent`.
`agent-context --pretty` indents that contract for reading.

Intentional compatibility correction: unknown fields, malformed paths and
paths through scalar values now fail with a clear usage error (exit 2), rather
than being silently omitted. Validation uses the command's declared schema
before model calls or other command work, not the first result row. Fields that
occur only in later rows are valid. Valid optional fields may be absent, and
valid empty results stay empty; empty arrays still reject unknown fields.
For commands with multiple result forms, the schema includes their declared
fields, whether or not this invocation emits each optional field.

Exit codes are stable and parseable:

| Code | Meaning |
| --- | --- |
| 0 | Success |
| 1 | Unexpected internal error |
| 2 | Usage error — bad flag, unknown model, empty prompt, near-miss command name |
| 3 | Missing resource — unknown conversation id, no search hits, bridge not installed, or no code to print with `--code` |
| 5 | Discovery, transport, Shortcut execution, declined request, or Apple rate-limit failure |
| 7 | Timeout — the run exceeded its deadline and was killed |
| 10 | Config or database error |

Specific JSON codes such as `request_declined`, `shortcut_failed`,
`local_unavailable` and `native_protocol` keep exit 5 while naming the cause.
See [failure meanings and next actions](errors.md).

### Overview versus readiness gate

Plain `hollis doctor` is an overview and exits 0 even if routes are unavailable.
Use `hollis doctor --model <route>` as an installation/readiness gate; it exits
nonzero when that route is unready. Neither command performs inference.
Overview JSON and agent results include `overview: true`,
`readiness: "not_assessed"`, `inference_tested: false` and a `diagnostics` array.
These fields survive `--select`, so a successful overview cannot be mistaken
for a ready route. Diagnostics do not use a top-level failure envelope.

Plain `hollis setup` prints route guidance. Selected standalone setup still
reports `runtime_unverified`: it cannot verify installation files, which does
not prove a helper is missing. Use selected doctor to inspect availability.

### Explicit Shortcut imports

`hollis setup --import [cloud cloud-pro on-device chatgpt image]` opens selected
bundled Shortcut imports; omit routes for all nine. A text route selects both
its text and JSON bridge; a `-json` suffix selects only that JSON bridge
(for example `cloud-pro-json`). It verifies the archive
against the sibling bundle manifest, signs private copies and independently skips
installed bridges unless `--force`. Use the complete release bundle or Homebrew layout.
Noninteractive, JSON and agent invocations require `--yes`. Planning without
`--import` remains read-only. Import-window launch is not proof of completed
imports or successful inference.

```sh
hollis setup --import
hollis setup --import cloud chatgpt
hollis setup --import cloud-pro --force --yes --no-input
```

Confirm the listed imports, choose **Add Shortcut** in each window, then run
`hollis doctor`. See [selected-route setup](selected-route-setup.md) for consent,
archive validation and retained private import files.

## Shell completions

Generate a completion script without reading configuration or invoking a runner:

```bash
hollis completion bash
hollis completion zsh
hollis completion fish
hollis completion powershell
```

All four shell subcommands accept `--no-descriptions` to omit completion descriptions.

The installer does not install completions. It ends with a hint naming the
setup help for your login shell, `hollis completion <shell> --help`. To load
completions in every new session:

```bash
# zsh: put the script in a directory on fpath, before compinit runs
mkdir -p ~/.zfunc
hollis completion zsh > ~/.zfunc/_hollis
# then add to ~/.zshrc, above any existing compinit line:
#   fpath=(~/.zfunc $fpath)
#   autoload -U compinit && compinit

# bash: requires the bash-completion package, for example Homebrew's bash-completion@2
hollis completion bash > "$(brew --prefix)/etc/bash_completion.d/hollis"

# fish
mkdir -p ~/.config/fish/completions
hollis completion fish > ~/.config/fish/completions/hollis.fish
```

For PowerShell, add the output of `hollis completion powershell` to your
profile. Start a new shell afterwards. Regenerate the script after upgrading
Hollis so new commands and flags complete.

The prepared Homebrew formula generates Bash, Zsh and Fish completions during
installation. It places the PowerShell script at
`$(brew --prefix)/share/hollis/completion/hollis.ps1`. The template is preparation
only; this does not announce a published tap.

## Your data

Configuration, chat history and run diagnostics live in one directory:

```
~/Library/Application Support/hollis/
├── hollis.db       conversations, messages, and per-run diagnostics
├── hollis.db.chat.lock  cross-process chat-continuation lock
├── hollis.db.migration.lock  schema migration coordination lock
├── config.json.lock     cross-process config update lock
└── config.json          default model and any bridge overrides
```

The state directory is mode `0700`; its database, config, lock, migration backups, and temporary files are mode `0600`. Set `HOLLIS_STATE_DIR` to an **absolute** directory for an isolated test or alternate state location. Existing Hollis-owned modes are tightened without changing broader parent directories.

Generated images and batch results live at the output paths you choose, outside this state directory. They are not deleted when you delete a chat.

Run diagnostics contain only request ID, requested/used tier, timing, exit code, error class, fallback, and byte counts. They never contain prompts, replies, or raw Apple stderr. Conversation deletion removes its messages, run records, and search entries in one transaction. To delete everything Hollis knows, remove that directory. To delete one conversation, `hollis chats delete <id>`.

### Database upgrades and recovery

Close older Hollis processes before upgrading. Schema v3 adds an indexed UTC
nanosecond key for chronological chat listing; readable timestamps and stored
transcripts are preserved. Malformed timestamps or instants outside the signed
64-bit nanosecond range (approximately September 1677 to April 2262) stop the
upgrade with an error rather than inventing a timestamp.

Schema changes, backfills, search-index recovery, and schema-version updates
commit together. A failed migration rolls back its uncommitted changes.
Before migrating or repairing an existing database, Hollis retains a private
`.hollis.db.backup-*` SQLite snapshot in the state directory. It includes committed
WAL data at the time of the snapshot, but it does not promise to capture later
writes from an already-running process. The migration lock coordinates compatible
Hollis openers; it does not make simultaneous old/new runtime use safe.

**Changing the executable is not a database rollback.** Runtime 0.4.0 supports
schema v2 and refuses a v3 database with a newer-schema error. A plugin's previous
runtime pointer does not downgrade data or restore a backup. Use a runtime that
supports the current schema.

Backup recovery is a separate, explicit action. A pre-upgrade snapshot omits
conversations and edits made after that snapshot. Hollis never silently restores
it on startup failure or an executable switch. To recover deliberately:

1. Stop all Hollis processes and other database users.
2. Preserve the current `hollis.db` and any `hollis.db-wal`/`hollis.db-shm` files
   together in a separate private directory. Do not overwrite or discard them.
3. Copy the selected backup into a **new**, private state directory as
   `hollis.db` (directory mode `0700`, file mode `0600`). Keep the original backup.
4. Set `HOLLIS_STATE_DIR` to that new directory and run `hollis chats list` with a
   compatible runtime. Check the recovered chats before choosing which directory
   to use. A newer runtime may migrate the recovered copy; the original snapshot
   and preserved upgraded database remain available.

## Quick reference

```bash
hollis respond "prompt"                    # one-shot response
hollis "prompt"                            # same as hollis respond "prompt"
hollis respond --model cloud-pro "prompt"  # explicit Cloud Pro
hollis respond --code "prompt" > file.go   # first complete non-empty fenced code block
hollis respond --image photo.jpg "describe" # image input; defaults to Cloud
hollis respond --file notes.md "summarize"   # attach a text document
hollis respond --file report.pdf "summarize"  # read a PDF on-device
hollis image generate "A lighthouse" --style sketch --output lighthouse.png
hollis batch resume --job ./job.json --max-calls 12
hollis chat "remember this"                # start a persistent chat
hollis chat --continue <id> "follow up"     # continue a chat
hollis chats list                           # list stored chats
hollis chats show <id>                      # show one chat
hollis chats search "query"                 # search stored chats
hollis serve --token-file hollis.token      # authenticated local API (background: docs/serve.md)
hollis doctor                               # check transport and bridges
hollis doctor --model local --json          # route-scoped diagnostics
hollis setup --model cloud-pro --agent      # plan setup for one route
hollis models                               # show all tiers, default and unavailable reasons
hollis config set model cloud-pro           # save a default tier
```

`hollis setup --model <route>` without `--import` and
`hollis doctor --model <route>` inspect one concrete route
without requesting inference, importing Shortcuts or changing settings.
Statuses, actions and exit codes are in
[selected-route setup and diagnostics](selected-route-setup.md).

[Back to Hollis](../README.md)

A lone positional containing a command name followed by flag tokens, such as
`hollis --model local "doctor --json"`, exits 2 with a split-command suggestion.
An unknown lone kebab-case word such as `help-agent` also exits 2 before any
Shortcut starts. Use `respond` or `--` as literal-prompt escapes.
`hollis --model local "Explain closures"` remains a prompt.

Chat deletion enables SQLite `secure_delete` to clear deleted table records.
This is not secure erasure: SQLite full-text index terms, WAL files, filesystem
snapshots, backups and previously exported copies can retain data.
