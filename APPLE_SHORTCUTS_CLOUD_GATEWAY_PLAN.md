# Apple Shortcuts Cloud Gateway — Project Outline

> **Status:** Design + validation phase  
> **Date:** 2026-08-31  
> **Workspace:** `/Users/zandbak/PLAYGROUND/shortcuts-playground/hollis`  
> **Binary/project name:** **`hollis`** (chosen 2026-09-01; avoids `pcc`, which Apple already uses for Private Cloud Compute)

## 1. Executive Summary

Build a small, durable **CLI-first gateway** that exposes Apple Intelligence **Cloud** and **Cloud Pro** through macOS Shortcuts, with an optional local HTTP server and persistent local chat history.

The key idea is:

```text
human / agent / local app
          │
          ▼
       CLI binary
          │
          ├── one-shot responses
          ├── persistent chats
          └── OpenAI-compatible local HTTP endpoint
          │
          ▼
   /usr/bin/shortcuts
          │
          ├── bridge shortcut → Cloud
          └── bridge shortcut → Cloud Pro
          │
          ▼
   Apple Private Cloud Compute
```

Shortcuts is deliberately kept as a **thin privileged transport**. The CLI owns model selection, chat persistence, HTTP compatibility, diagnostics, configuration, concurrency, and error handling.

The first implementation decision should **not** be coding the whole gateway. First prove that replaying stored conversation history into Cloud Pro gives reliable multi-turn behavior.

---

## 2. Why This Project Exists

On macOS 27 beta 7, Apple removed Private Cloud Compute from the public `/usr/bin/fm` CLI/server surface on the tested machine, while PCC remains present through the Foundation Models framework and works through Apple Shortcuts.

The useful replacement path discovered is:

```text
CLI
  ↓
shortcuts run
  ↓
Use Model → Cloud / Cloud Pro
  ↓
Private Cloud Compute
```

The project turns that working path into something practical for humans, agents, scripts, and local applications.

---

## 3. Verified Local Findings

These results were observed directly on:

```text
macOS 27.0
Build 26A5421a
Apple Silicon M4 Pro
```

### 3.1 `fm` CLI / server

Observed:

```text
/usr/bin/fm
```

`fm --help` exposes only:

```text
MODELS
  system
```

`fm available --model pcc`:

```text
Error: The value 'pcc' is invalid for '--model <model>'.
Please provide one of 'system'.
```

`fm quota-usage`:

```text
Error: Unknown command 'quota-usage'.
```

`fm serve --help` exposes only:

```text
MODELS
  system
```

Live `fm serve`:

```text
GET /v1/models
→ system only
```

Direct request:

```json
{
  "model": "pcc"
}
```

returns:

```text
400
Unknown model 'pcc'. Available models: system
```

Control request with `"model":"system"` succeeds.

### 3.2 FoundationModels framework

Installed framework:

```text
/System/Library/Frameworks/FoundationModels.framework
CFBundleIdentifier: com.apple.FoundationModels
CFBundleShortVersionString: 1.0
CFBundleVersion: 2.0.68.1.402
```

A Swift type-check successfully resolves:

```swift
PrivateCloudComputeLanguageModel()
```

A runtime probe reports:

```text
PCC availability: available
PCC isAvailable: true
```

An actual request from an ad-hoc unsigned/unentitled executable fails later with:

```text
FoundationModels.LanguageModelError -1
└── ModelManagerServices.ModelManagerError 1046
```

The test executable was confirmed to be:

```text
Signature=adhoc
TeamIdentifier=not set
```

and had no PCC entitlement.

**Conclusion:** the PCC framework/runtime path remains present. The failure is at the authorization/request boundary, while the `fm` path itself no longer exposes PCC.

### 3.3 Shortcuts

On the tested beta 7 machine, the **Use Model** action visibly exposes:

```text
Cloud
Cloud Pro
On-Device
ChatGPT
```

Observed in Shortcuts.app:

- **Cloud** successfully returns model output.
- **Cloud Pro** successfully returns model output.
- Cloud Pro is fast and may perform increased reasoning internally, but does not expose hidden reasoning traces.

A test shortcut used:

```text
Use Cloud Pro model
  ↓
Stop and Output [Response]
```

Apple documents that command-line shortcuts can pass output to another process when the shortcut ends with an output-producing action or **Stop and Output**.

### 3.4 Verified `shortcuts run` output contract

Measured 2026-09-01 on the same machine, repeatedly, against `PCC Test Pro`:

```text
-o <file>                     → full output, exit 0
-o -            (redirected)  → full output, exit 0
bare run        (redirected)  → full output, exit 0
any form        (real TTY)    → 0 bytes,     exit 0
-o /dev/stdout                → SIGABRT,     exit 134
```

The decisive facts:

- **stdout is silently suppressed when stdout is a TTY.** The same shortcut that returns nothing in an interactive terminal returns full output the moment stdout is a pipe or file. Verified by forcing a pty with `script(1)`: 0 bytes.
- **The default output type is RTF**, not plain text. A bare run returns `{\rtf1\ansi\ansicpg1252\cocoartf2907...}` wrapping the answer.
- `--output-type public.plain-text` yields clean UTF-8 text.
- **Output carries no trailing newline.**
- `-o /dev/stdout` aborts the CLI. Use `-o -` instead.

None of this is documented by Apple. Two consequences:

1. **Exit `0` with empty stdout proves nothing.** It is the normal result of a fully successful run in a terminal. The gateway must treat empty output as an error condition (`shortcut_no_output`), never as an empty response.
2. **Production is unaffected.** A Go runner using `exec.Command` captures stdout through a pipe, which is the working path. This trap only breaks interactive testing and hand-written shell probes.

### 3.5 Verified shortcut internals

Decoded from `~/Library/Shortcuts/Shortcuts.sqlite`, table `ZSHORTCUTACTIONS`, column `ZDATA` (an Apple binary plist; copy the DB before reading):

```text
is.workflow.actions.askllm          ← Use Model
  WFLLMModel  "Apple Intelligence"      ← UI label "Cloud"
  WFLLMModel  "Apple Intelligence Pro"  ← UI label "Cloud Pro"
  WFGenerativeResultType "Text"
  WFLLMPrompt <literal string or attachment>

is.workflow.actions.output          ← Stop and Output
  WFOutput / WFResponse → ActionOutput of the askllm action UUID
  WFNoOutputSurfaceBehavior "Do Nothing"
```

These are real, reproducible **Shortcuts-layer** identifiers, suitable for generating or validating bridge shortcuts. They are **not** Apple backend model IDs and must not be presented as such.

The original `PCC Test` / `PCC Test Pro` shortcuts reported:

```text
ZHASSHORTCUTINPUTVARIABLES = 0
ZINPUTCLASSESDATA          = <empty array>
```

They accepted **no input** and carried a hardcoded `WFLLMPrompt`, which is why they could not serve as bridges. The working bridges bind `WFLLMPrompt` to an `ExtensionInput` attachment instead:

```text
WFLLMPrompt = WFTextTokenString
  string             "\uFFFC"
  attachmentsByRange {0, 1} → { Type: "ExtensionInput" }
```

Generated by `scripts/make-bridge.py`. A hand-built plist is accepted by `shortcuts sign`; import then needs one GUI confirmation per shortcut.

### 3.6 Verified end-to-end (2026-09-01)

Full evidence: `results/transport-and-persistence-2026-09-01.md`.

```text
stdin → Use Model            PASS   exact token returned
multi-line stdin             PASS   17-line transcript intact
native cross-run memory      NONE   separate runs are stateless
transcript replay            PASS   codeword recovered
10-turn replay + correction  PASS   early/middle/late facts, correction won
replay on Cloud (non-Pro)    PASS   not a Pro-only capability
Unicode + JSON round-trip    PASS   byte-exact
4 parallel invocations       PASS   no interference, no GUI prompts
1124-word output             PASS   no truncation
empty input                  HANG   never returns; caller must prevent
```

**The architectural gate is cleared.** Separate runs retain no state, and replayed transcripts reliably restore context on both models. The plan's core premise — application-owned persistence via replay — is proven rather than assumed.

### 3.7 What is still NOT verified

Do not silently promote these to facts:

- Exact underlying Apple backend model IDs behind **Cloud** and **Cloud Pro**.
- Maximum usable context length through Shortcuts. Replay is proven to 624 bytes / 36 lines only; larger transcripts are untested.
- Behavior of a **system-like instruction** surviving a long history (§15 Test D remains unrun).
- Concurrency above 4, and whether sustained parallel load triggers rate limiting.
- Errors when Apple Intelligence is disabled, or when offline.
- Whether model selection or behavior changes in later macOS 27 seeds.

---

## 4. Official Shortcuts Behavior Relevant to the Project

Apple documents the `shortcuts` command as a supported command-line interface.

Important behaviors:

```bash
shortcuts run "Shortcut Name"
shortcuts list
shortcuts view "Shortcut Name"
shortcuts sign ...
```

Apple documents that:

- Shortcuts can accept text, documents, images, and other input from the command line.
- Piped input is treated as text.
- `-i` / `--input-path` is for treating supplied values as file paths.
- A shortcut that ends in an output-producing action, or **Stop and Output**, can pipe output to another process.
- `-o` / `--output-path` writes output to a file; `-` means stdout.
- `--output-type` forces the output format via a Uniform Type Identifier, e.g. `public.plain-text`.
- `shortcuts list --show-identifiers` exposes stable UUIDs; `shortcuts run` accepts a UUID in place of a name.
- The CLI exits `0` on success and `1` on error.
- "The most efficient shortcuts are ones that don't show alerts or ask for input. When a shortcut asks for input, the command line process pauses, awaiting user input." For a headless bridge, that pause is a hang.

Apple does **not** document the TTY suppression or the RTF default described in §3.4. Both were established by local measurement.

Reference:

- https://support.apple.com/en-euro/guide/shortcuts-mac/apd455c82f02/mac

Apple also documents **Use Model** as a Shortcuts action that can use Apple Intelligence on-device or Private Cloud Compute.

Reference:

- https://support.apple.com/en-hk/guide/mac-help/mchl91750563/mac

Local source copies currently in the playground:

```text
shortcuts-docs.md
shotcuts-mac-apple-intelligence.md
```

---

## 5. Product Goals

### Primary goals

1. Provide a clean one-shot CLI for Cloud and Cloud Pro.
2. Support prompts from arguments and stdin.
3. Return plain text for humans and stable JSON for agents.
4. Provide persistent local chat conversations.
5. Expose an optional local HTTP endpoint.
6. Make the HTTP surface close enough to OpenAI Chat Completions for common local clients.
7. Keep Shortcuts itself minimal and replaceable.
8. Provide strong diagnostics through `doctor`.
9. Be honest about unsupported features and unavailable metadata.
10. Remain useful if Apple later restores PCC to `fm` or another supported transport appears.

### Secondary goals

- Agent-friendly output and small context usage.
- Simple installation of the required bridge shortcuts.
- Local-only by default.
- Easy future addition of On-Device as another backend/model.
- Easy future replacement of the Shortcuts runner.

---

## 6. Non-Goals for v1

Do **not** make v1 larger than necessary.

Not in initial scope:

- MCP server.
- GUI.
- Fake token streaming.
- Fake token counts.
- Exposing chain-of-thought/reasoning traces.
- Remote internet service.
- Cloud-hosted chat history.
- Multi-user authentication system.
- Full compatibility with every OpenAI API field.
- Silent reverse-engineering of private Apple network endpoints.
- Bypassing Apple entitlements.
- Depending on undocumented generated `.shortcut` internals as a core production contract.

---

## 7. Runtime Choice

### Recommended: Go

Why:

- Single native binary.
- Excellent subprocess control.
- Excellent HTTP server support.
- Simple concurrency control.
- Easy SQLite support.
- Fits the existing Printing Press CLI ecosystem and conventions.
- Straightforward distribution to other local tools and agents.

Reference local CLI style:

```text
/Users/zandbak/PLAYGROUND/printingpress
```

Existing CLIs worth borrowing conventions from:

```text
delpher/bin/delpher-pp-cli
superserve-cli/bin/superserve-pp-cli
```

Do **not** blindly copy their large generic command surfaces. Reuse the useful ergonomics:

- `doctor`
- `--json`
- `--agent`
- `--select`
- stable exit codes
- clear diagnostics
- one durable binary
- explicit health checks

---

## 8. Core Architecture

Use a transport abstraction from the beginning.

```go
type Runner interface {
    Respond(ctx context.Context, req Request) (Response, error)
}
```

Initial implementation:

```text
ShortcutRunner
```

Possible future implementations:

```text
FMRunner
FoundationModelsRunner
MockRunner
```

Everything above the runner should be transport-independent:

```text
CLI
Chat persistence
HTTP server
Diagnostics
Tests
```

Architecture:

```text
                  ┌────────────────────┐
                  │      CLI / API     │
                  └─────────┬──────────┘
                            │
                    Application Core
                  ┌─────────┴──────────┐
                  │                    │
             Chat Store             Runner
               SQLite                  │
                                       ▼
                               ShortcutRunner
                                       │
                              /usr/bin/shortcuts
                              ┌────────┴────────┐
                              │                 │
                        Cloud bridge      Cloud Pro bridge
                              │                 │
                              └────────┬────────┘
                                       ▼
                             Apple cloud models
```

---

## 9. Shortcuts Bridge Design

Keep Shortcuts intentionally stupid.

### Canonical bridge 1

Suggested internal name:

```text
AFM Bridge - Cloud
```

Flow:

```text
Shortcut Input
      ↓
Use Model → Cloud
      ↓
Stop and Output → Response
```

### Canonical bridge 2

Suggested internal name:

```text
AFM Bridge - Cloud Pro
```

Flow:

```text
Shortcut Input
      ↓
Use Model → Cloud Pro
      ↓
Stop and Output → Response
```

### Automation rules

For bridge shortcuts:

- **Follow Up: OFF**
- No menus.
- No alerts.
- No interactive Ask for Input.
- No model-selection logic.
- No local persistence.
- Input comes from Shortcut Input.
- Output is explicit text through Stop and Output.

All orchestration belongs in Go.

### Installation strategy

For v1, prefer:

1. Create canonical shortcuts in Shortcuts.app.
2. Export them.
3. Preserve known-good `.shortcut` artifacts.
4. Sign shared shortcut artifacts with Apple’s documented `shortcuts sign`. Note that signing transmits a copy of the shortcut to Apple for validation, and `-m anyone` requires iCloud; prefer signing only artifacts intended for distribution.
5. CLI checks that the expected shortcut names are installed.
6. If missing, `shortcuts install` can open the bundled signed shortcut for user import.

Do not make v1 depend on reverse-engineered Shortcuts plist internals if it can be avoided.

---

## 10. CLI Shape

**Binary name: `hollis`** (chosen 2026-09-01; `pcc` was never on the table — Apple owns PCC).

Target surface:

```text
hollis
├── respond
├── chat
├── chats
│   ├── list
│   ├── show
│   ├── rename
│   ├── delete
│   └── export
├── models
├── doctor
├── shortcuts
│   ├── install
│   ├── status
│   └── test
├── serve
└── version
```

### One-shot usage

```bash
hollis respond "Explain quantum tunnelling simply"
```

Default model:

```text
cloud
```

Cloud Pro:

```bash
hollis respond --model cloud-pro "Solve this carefully"
```

stdin:

```bash
echo "Explain recursion" | hollis respond
```

file/shell pipelines:

```bash
cat prompt.txt | hollis respond --model cloud-pro
```

### Human output

Default stdout should be only the useful response:

```text
The answer...
```

Diagnostics/progress go to stderr.

### JSON

```bash
hollis respond --model cloud-pro --json "What is 17 * 23?"
```

Example:

```json
{
  "model": "cloud-pro",
  "response": "391",
  "duration_ms": 824
}
```

Never invent unavailable fields such as:

```text
prompt_tokens
completion_tokens
reasoning_tokens
context_window
server_model_id
```

unless they become directly observable.

### Agent mode

Borrow the Printing Press idea:

```bash
hollis --agent ...
```

Possible semantics:

```text
--json
--no-color
--no-input
compact diagnostics
stable machine-readable errors
```

`--select` can be added only where it actually saves context.

---

## 11. Persistent Chat

### Problem

Apple documents **Follow Up** during a running Use Model interaction, but no persistent cross-run:

```text
session_id
thread_id
conversation_id
resume
```

has been established for separate `shortcuts run` invocations.

Therefore persistent chat should initially be treated as an **application responsibility**.

### Design

```text
SQLite conversation
       │
       ├── prior user turns
       ├── prior assistant turns
       └── optional summary
                 │
                 ▼
        render conversation
                 +
            new user turn
                 │
                 ▼
        Cloud / Cloud Pro
                 │
                 ▼
           save response
```

This is **replayed context**, not a native Apple persistent session.

The user-facing experience can still be a real persistent chat.

---

## 12. SQLite Data Model

Suggested initial schema:

### `conversations`

```sql
id            TEXT PRIMARY KEY
title         TEXT
model         TEXT NOT NULL
summary       TEXT
created_at    TEXT NOT NULL
updated_at    TEXT NOT NULL
archived      INTEGER NOT NULL DEFAULT 0
```

### `messages`

```sql
id                INTEGER PRIMARY KEY AUTOINCREMENT
conversation_id   TEXT NOT NULL
seq               INTEGER NOT NULL
role              TEXT NOT NULL
content           TEXT NOT NULL
created_at        TEXT NOT NULL
metadata_json     TEXT
```

Roles:

```text
system
user
assistant
```

### `runs`

Useful for diagnostics and performance:

```sql
id                INTEGER PRIMARY KEY AUTOINCREMENT
conversation_id   TEXT
model             TEXT NOT NULL
started_at        TEXT NOT NULL
duration_ms       INTEGER
exit_code         INTEGER
error_class       TEXT
stderr_excerpt    TEXT
```

Do not store secrets in run records.

---

## 13. Conversation Rendering

The model needs a deterministic transcript built from stored messages.

Initial renderer can use explicit roles:

```text
You are continuing an existing conversation.

SYSTEM:
...

USER:
...

ASSISTANT:
...

USER:
<new message>

Respond to the final USER message while preserving the conversation context.
```

Alternative formats should be tested rather than assumed superior.

Potential candidates:

1. Plain role blocks.
2. JSON message array.
3. XML-like role blocks.
4. Compact summarized history + recent full turns.

The chosen format should be based on empirical reliability with Cloud and Cloud Pro.

---

## 14. Context Growth / Compaction

Replaying full history will eventually grow too large.

Do not guess Apple’s Shortcuts context limit.

v1 approach:

1. Keep all messages in SQLite.
2. Replay full history while reasonably small.
3. Track character/byte size as an approximate local metric.
4. Add configurable soft limits.
5. When needed, summarize older turns into `conversations.summary`.
6. Keep recent turns verbatim.
7. Preserve original messages in SQLite even after prompt compaction.

Do not call approximate character counts “tokens”.

Possible future strategy:

```text
[conversation summary]
+
[last N verbatim messages]
+
[new user message]
```

---

## 15. Required Persistence Experiments Before Implementing Chat

> **Status 2026-09-01: Tests A, B, C and E have PASSED.** Results and exact
> transcripts are in `results/transport-and-persistence-2026-09-01.md`.
> Test D (system-like instruction preservation) is still unrun.
> The test definitions below are retained as the reproducible specification.

### Test A — Native cross-run memory

Purpose:

Determine whether separate Shortcuts runs retain any model state without replay.

Run 1:

```text
Remember this exact codeword for our conversation:
VANTA-ORBIT-7319

Reply only:
ACK
```

End the shortcut completely.

Run 2 as a **new** invocation:

```text
What exact codeword did I give you in the previous conversation turn?
If you cannot know it, reply exactly:
NONE
```

Interpretation:

```text
VANTA-ORBIT-7319
```

would be evidence of native cross-run persistence.

```text
NONE
```

or failure to recall supports the assumption that separate runs are stateless.

Repeat several times with fresh random codewords before concluding.

### Test B — Replayed transcript

Purpose:

Prove that our own stored history can restore context.

Send in one fresh invocation:

```text
Continue the following conversation faithfully.

USER:
Remember this exact codeword: VANTA-ORBIT-7319.

ASSISTANT:
ACK

USER:
We are building a CLI that accesses Apple Cloud Pro through Shortcuts.

ASSISTANT:
Understood.

USER:
What was the exact codeword I gave you earlier?
Reply with only the codeword.
```

Expected:

```text
VANTA-ORBIT-7319
```

Repeat with multiple random codewords.

### Test C — Multi-turn replay

If B passes:

- 5 turns
- 10 turns
- 20 turns
- references to facts from early, middle, and recent turns
- changed instructions
- corrected facts
- nested code blocks
- Unicode
- long responses

### Test D — System-like instruction preservation

Replay:

```text
SYSTEM:
Always answer in valid JSON.

USER:
...
```

Measure whether that instruction remains reliable as history grows.

### Test E — Cloud vs Cloud Pro

Run the same transcript-replay suite against both.

Do not assume the two behave identically.

---

## 16. Test Harness Requirements

Create a reproducible harness in the repo.

Suggested layout:

```text
scripts/
  test-shortcut-io.sh
  test-chat-persistence.sh

docs/
  chat-persistence-test.md

results/
  chat-persistence-YYYY-MM-DD.md
```

Capture:

```text
macOS version
build
shortcut name
model selection
timestamp
exact input
exact stdout
stderr
exit code
duration
pass/fail
notes
```

Use random or run-specific codewords to prevent accidental contamination.

Example codeword generation:

```text
VANTA-ORBIT-<random>
```

Never fabricate output in result files.

---

## 17. First Transport Test

Before persistence testing, prove the production shortcut I/O contract.

Two corrections from measured behavior (§3.4). The original version of this test would have produced a false negative:

- `shortcuts run` prints nothing when stdout is a TTY, so the test must pipe or redirect.
- The default output type is RTF, so an exact string match requires `--output-type public.plain-text`.

### Step 1 — output half (already proven for a hardcoded prompt)

```bash
shortcuts run "AFM Bridge - Cloud Pro" --output-type public.plain-text | cat
```

The trailing `| cat` is load-bearing: it makes stdout a non-TTY. Without it the command prints nothing and still exits `0`.

### Step 2 — input half (NOT yet proven; this is the real gate)

Requires a bridge whose `WFLLMPrompt` is bound to **Shortcut Input** rather than a literal string.

```bash
printf '%s' 'Reply exactly SHORTCUT_IO_OK' \
  | shortcuts run "AFM Bridge - Cloud Pro" --output-type public.plain-text \
  | cat
```

### Expected stdout

```text
SHORTCUT_IO_OK
```

with **no trailing newline**.

### Exit code

```text
0
```

Repeat for Cloud.

**Treat empty stdout with exit `0` as a failure, not a pass.**

If stdout does not behave as expected:

- confirm stdout is not a TTY,
- confirm `--output-type public.plain-text`,
- confirm the prompt field is bound to Shortcut Input, not a literal,
- verify Stop and Output is wired to the Use Model **Response**,
- inspect `shortcuts view`,
- capture stderr,
- test `-o <file>`,
- do not proceed to higher-level implementation until the transport is stable.

Do not use `-o /dev/stdout`; it aborts the CLI with exit `134`.

---

## 18. Interactive Chat UX

Target:

```bash
hollis chat
```

Example:

```text
New chat · cloud-pro

> I'm building an HTTP gateway around Apple Shortcuts.

< Sounds good. What constraints do you have?

> Remind me what transport I said I was using.

< Apple Shortcuts.
```

Useful options:

```bash
hollis chat --model cloud-pro
hollis chat --continue <id>
hollis chat --new
hollis chats list
hollis chats show <id>
hollis chats rename <id> "Gateway design"
hollis chats delete <id>
```

Deletion should require explicit confirmation unless `--yes` / agent-safe semantics are clearly defined.

---

## 19. HTTP Server

Same binary:

```bash
hollis serve
```

Default:

```text
127.0.0.1:1976
```

Initial endpoints:

```text
GET  /health
GET  /v1/models
POST /v1/chat/completions
```

### `/v1/models`

Expose only what the gateway actually supports.

Example:

```json
{
  "object": "list",
  "data": [
    {
      "id": "cloud",
      "object": "model",
      "owned_by": "Apple"
    },
    {
      "id": "cloud-pro",
      "object": "model",
      "owned_by": "Apple"
    }
  ]
}
```

Do not claim exact underlying AFM model IDs unless verified.

### `/v1/chat/completions`

Support a useful subset:

```json
{
  "model": "cloud-pro",
  "messages": [
    {"role": "system", "content": "..."},
    {"role": "user", "content": "..."},
    {"role": "assistant", "content": "..."},
    {"role": "user", "content": "..."}
  ],
  "stream": false
}
```

The server serializes `messages` into the tested transcript format and makes one Shortcuts call.

### Streaming

v1:

```text
stream=false only
```

If:

```json
"stream": true
```

return a clear unsupported-feature error.

Do **not** fake streaming by splitting an already-complete response.

---

## 20. Server-Side Persistent Conversations

OpenAI Chat Completions clients normally send their own full `messages` array, so standard compatibility can remain stateless.

For our own persistent API, consider an optional extension later:

```text
conversation_id
```

or separate endpoints:

```text
POST /v1/conversations
POST /v1/conversations/{id}/messages
GET  /v1/conversations/{id}
```

Do not make this necessary for basic OpenAI-compatible clients.

---

## 21. Models

v1 user-facing names:

```text
cloud
cloud-pro
```

Possible future:

```text
on-device
```

Do not expose `chatgpt` initially; it defeats the purpose of this project.

Do not call the binary `pcc`.

Verified Shortcuts-layer mapping (§3.5), safe to rely on for building and validating bridges:

```text
cloud      → WFLLMModel "Apple Intelligence"      → UI "Cloud"
cloud-pro  → WFLLMModel "Apple Intelligence Pro"  → UI "Cloud Pro"
```

Do not equate UI labels or these `WFLLMModel` strings with exact Apple backend model identifiers unless verified by Apple or reproducible technical evidence. They are Shortcuts action parameters, nothing more.

---

## 22. `doctor`

This should be a first-class feature.

Target human output:

```text
$ hollis doctor

macOS                    27.0 (26A5421a)       ✓
Shortcuts CLI            /usr/bin/shortcuts    ✓
Apple Intelligence                              ✓
Cloud shortcut           installed              ✓
Cloud Pro shortcut       installed              ✓
Cloud inference          working · 0.72s        ✓
Cloud Pro inference      working · 1.06s        ✓
SQLite store             writable               ✓

Ready.
```

Machine form:

```bash
hollis doctor --json
```

Check:

- macOS version/build.
- `/usr/bin/shortcuts`.
- expected shortcuts installed.
- `shortcuts list`.
- Cloud probe.
- Cloud Pro probe.
- exit code behavior.
- stdout behavior.
- config directory.
- SQLite database creation/open.
- optional server port availability.

Do not require `/usr/bin/fm` for core operation.

It can be reported as informational diagnostics only.

---

## 23. Error Model

Errors should distinguish at least:

```text
shortcut_not_installed
shortcut_failed
shortcut_timeout
shortcut_no_output
model_unavailable
invalid_model
invalid_input
database_error
context_too_large
unsupported_streaming
server_busy
internal_error
```

For JSON:

```json
{
  "error": {
    "code": "shortcut_failed",
    "message": "Cloud Pro shortcut exited with status 1"
  }
}
```

stdout:

- response/JSON only.

stderr:

- diagnostics.

Exit codes:

- `0`: success
- non-zero: real failure

Measured `shortcuts` CLI behavior to map from:

```text
exit 1    missing/renamed shortcut
          stderr: "Error: The operation couldn't be completed. Couldn't find shortcut"
exit 64   usage error, e.g. invalid --output-type
          stderr: full usage text
exit 134  SIGABRT, produced by -o /dev/stdout; never use that form
exit 0 + empty stdout
          ambiguous: this is ALSO what a successful run looks like on a TTY.
          Map to shortcut_no_output and treat as failure.
no exit   empty input hangs forever. The runner MUST impose its own deadline.
```

Real failures do surface nonzero exits with useful stderr, so this error model is implementable. Two cases cannot be detected from the exit code alone and must be handled by the runner: empty output, and the indefinite hang.

Avoid dumping huge raw Apple errors by default; preserve useful detail under verbose/debug modes.

---

## 24. Concurrency

Measured 2026-09-01: **4 parallel invocations succeeded cleanly.** All four returned their own distinct correct answers, with no cross-talk, no GUI prompts, and no errors. Wall clock was 1.92s against roughly 4.6s of serial-equivalent work, so the calls genuinely overlap rather than queueing.

Safe v1 default remains:

```text
max concurrent Shortcuts model calls = 1
```

Keep the queue/semaphore, but make the limit configurable. The evidence supports raising it to 4 once the HTTP server exists; it does not yet cover sustained load or rate limiting.

Still to measure:

- concurrency above 4,
- sustained parallel load over time,
- rate limiting or quota behavior on Cloud Pro.

---

## 25. Timeouts

Observed latency, three samples each, trivial prompt:

```text
Cloud      0.86s  0.91s  0.91s
Cloud Pro  1.15s  1.12s  1.14s
```

A 1124-word generation also completed well inside the bound. The provisional 120s was roughly 100x the observed p50.

Revised guidance:

```text
default timeout   30s   (generous vs ~1s observed, still fails fast)
hard ceiling      120s  (for very long generations, via config/flag)
```

**A timeout is not optional.** Empty input causes `shortcuts run` to hang indefinitely with no output on either stream, and macOS ships no `timeout(1)`. The runner must always apply a context deadline and kill the child itself.

Timeout errors must terminate child processes cleanly. Verified that signal-based termination leaves no orphaned `shortcuts run` processes.

---

## 26. Security

Default server bind:

```text
127.0.0.1
```

Do not expose remotely by default.

If binding:

```text
0.0.0.0
```

require or strongly enforce authentication.

Suggested:

```bash
hollis serve --host 0.0.0.0 --api-key-env <ENV_NAME>
```

Never log API-key values.

Do not automatically expose through Tailscale/Funnel.

Remote exposure should be an explicit later decision.

Chat history remains local SQLite by default.

---

## 27. Configuration

Suggested config path:

```text
~/Library/Application Support/hollis/config.toml
```

or another normal macOS per-user path.

Possible config:

```toml
default_model = "cloud"
cloud_shortcut = "AFM Bridge - Cloud"
cloud_pro_shortcut = "AFM Bridge - Cloud Pro"
timeout = "120s"
max_concurrency = 1
database_path = "..."
```

CLI flags override config.

Keep initial config minimal.

---

## 28. Suggested Repo Layout

```text
shortcuts-playground/
├── README.md
├── PROJECT_PLAN.md
├── go.mod
├── cmd/
│   └── hollis/
│       └── main.go
├── internal/
│   ├── app/
│   ├── runner/
│   │   ├── runner.go
│   │   └── shortcuts.go
│   ├── chat/
│   ├── store/
│   │   └── sqlite.go
│   ├── server/
│   ├── doctor/
│   └── config/
├── shortcuts/
│   ├── cloud/
│   └── cloud-pro/
├── scripts/
│   ├── test-shortcut-io.sh
│   └── test-chat-persistence.sh
├── docs/
│   ├── architecture.md
│   └── chat-persistence-test.md
├── results/
└── testdata/
```

Do not scaffold all packages before the persistence experiment proves the approach.

---

## 29. Implementation Order

### Phase 0 — Preserve evidence

Write a concise local note containing the verified beta 7 findings listed above.

Purpose:

- prevent rediscovery,
- distinguish `fm` removal from framework availability,
- preserve exact build-specific evidence.

### Phase 1 — Stabilize Shortcuts transport ✅ COMPLETE (2026-09-01)

Built, signed, imported and verified:

```text
AFM Bridge - Cloud.signed      BD8CDC56-7CB8-418D-9B02-9D33AB911BF0
AFM Bridge - Cloud Pro.signed  DBB6E472-CBC6-4421-8D32-9D4543D5CDE6
```

Generator: `scripts/make-bridge.py`. Both halves of the I/O contract confirmed.

Note the imported names carry a `.signed` suffix from the filename. Either rename them in Shortcuts.app or reference them by UUID from config; prefer UUIDs, since names collide and get renamed.

### Phase 2 — Persistence experiments ✅ PASSED (2026-09-01)

Tests A, B, C and E all passed; Test D remains outstanding. See
`results/transport-and-persistence-2026-09-01.md`.

**Decision gate:**

If transcript replay is unreliable, stop and reconsider the project.

If transcript replay is reliable, continue.

### Phase 3 — Minimal Go runner ✅ COMPLETE (2026-09-01)

`internal/runner` (ShortcutRunner, 8 non-negotiable rules) and `hollis respond`
shipped and verified end-to-end against the Cloud bridge.

No database or HTTP server yet.

Tests:

- argument prompt,
- stdin,
- Cloud,
- Cloud Pro,
- text output,
- JSON output,
- timeout,
- missing shortcut.

### Phase 4 — `doctor` ✅ COMPLETE (2026-09-01)

`hollis doctor` probes the shortcuts CLI and both bridge UUIDs; `shortcuts
list` name checks are warnings only, since UUID invocation is verified.

### Phase 5 — SQLite persistent chat ✅ COMPLETE (2026-09-01)

Implemented `chat` (one-shot + `--continue` + interactive REPL),
`chats list/show/rename/delete` on `modernc.org/sqlite` (pure Go, no cgo).
Transcript replay verified through the CLI: turn 2 with `--continue` recovered
the planted codeword (see results addendum). `chats delete --yes` cleaned up
the test conversation afterward.

### Phase 6 — Context compaction

Only after real chats become large enough to need it.

### Phase 7 — HTTP endpoint

Add:

```text
/health
/v1/models
/v1/chat/completions
```

Non-streaming only.

### Phase 8 — Hardening

Test:

- concurrency,
- offline failures,
- Apple Intelligence disabled,
- shortcut removed/renamed,
- Unicode,
- huge prompts,
- JSON-shaped content,
- shell pipelines,
- server auth,
- clean shutdown.

### Phase 9 — Packaging

Add:

- install command,
- bundled/signed shortcuts,
- README,
- agent usage docs,
- versioning,
- release build.

---

## 30. Testing Matrix

At minimum:

| Area | Test |
|---|---|
| Shortcuts input | stdin text reaches Use Model |
| Shortcuts output | Stop and Output reaches stdout |
| stdout is a TTY | output suppressed; must pipe/redirect |
| Output type | RTF default vs `public.plain-text` |
| Trailing newline | absent; reader must tolerate |
| Empty output | exit 0 + empty treated as error |
| Cloud | one-shot success |
| Cloud Pro | one-shot success |
| Native memory | separate runs without replay |
| Replay memory | explicit history |
| 10+ turn chat | old facts remain recoverable |
| Corrections | later correction beats earlier fact |
| System behavior | system-like instructions survive replay |
| Unicode | emoji / Japanese / Dutch |
| Markdown | code fences / tables |
| JSON content | quotes / braces / escaped strings |
| Empty input | clean validation |
| Missing shortcut | useful error |
| Shortcuts exit 1 | useful error |
| Timeout | child process cleaned up |
| Offline | useful cloud failure |
| Concurrency | 1 / 2 / 4 |
| SQLite | persistence across tool restart |
| HTTP | `/health`, `/v1/models`, completions |
| Streaming request | explicit unsupported error |
| Remote bind | auth required |

---

## 31. Metrics Worth Recording

For every test/run where useful:

```text
model
wall-clock duration
input character count
output character count
shortcut exit code
error category
conversation turn count
rendered transcript size
```

Do not invent token counts.

These measurements will tell us when compaction becomes necessary.

---

## 32. Naming

Requirements:

- Do not use `pcc`.
- Short.
- Shell-friendly.
- Not easily confused with Apple’s `fm`.
- Should still make sense if Shortcuts is replaced as the backend later.
- Prefer a name that suggests Apple/Foundation Models/cloud gateway rather than the current workaround.

**Decision (2026-09-01): the binary is `hollis`.**

Chosen from a same-vibe candidate batch (plain folksy names) after a sweep across every registry this tool would ever touch — Homebrew-core, crates.io, PyPI, npm, RubyGems (HTTP check: 200 = taken, 404 = free):

```text
hollis  → free in ALL five registries ✅
gomer   → free in ALL five registries
lamar   → free in ALL five registries
hank    → taken (crates.io, PyPI, npm, RubyGems — incl. a brew-tappable AI error explainer)
gus     → taken in all four
otis    → taken (an AI terminal agent — same domain, disqualified)
```

`hollis` also has no in-domain conflict: no existing AI/terminal-agent tool uses the name.

---

## 33. Printing Press Relationship

This is CLI-first and should borrow Printing Press conventions, but it is not a normal third-party HTTP API wrapper.

Use these local references:

```text
/Users/zandbak/PLAYGROUND/printingpress/AGENTS.md
/Users/zandbak/PLAYGROUND/printingpress/README.md
/Users/zandbak/PLAYGROUND/printingpress/delpher
/Users/zandbak/PLAYGROUND/printingpress/superserve-cli
```

Good patterns to reuse:

```text
doctor
--agent
--json
stable errors
clear human vs machine output
installed binary smoke tests
agent-facing docs
```

Avoid importing generic functionality that this tool does not need.

If Printing Press is used to scaffold later, keep the final surface intentionally small.

---

## 34. Open Questions

These should be answered empirically where possible.

Resolved 2026-09-01 (evidence in `results/transport-and-persistence-2026-09-01.md`):

- ~~Can a bridge receive stdin and route it into the Use Model prompt?~~ **Yes.**
- ~~Does Cloud Pro have native cross-run state?~~ **No. Runs are stateless.**
- ~~Does transcript replay work reliably?~~ **Yes, on both models, including corrections.**
- ~~Does Shortcuts preserve output exactly?~~ **Yes. Unicode and JSON are byte-exact.**
- ~~What happens under parallel invocations?~~ **4 in parallel work cleanly.**
- ~~Can bundled signed shortcuts be installed with acceptable UX?~~ **Yes, one GUI confirmation each.**
- ~~Which final CLI name has no meaningful collision?~~ **`hollis` — free across Homebrew-core, crates.io, PyPI, npm, and RubyGems (checked 2026-09-01).**

Still open:

3. How large can replayed context become? Proven only to 624 bytes / 36 lines.
4. Does a system-like instruction survive a long history (§15 Test D)?
7. What errors occur when Apple Intelligence is disabled?
8. What errors occur offline?
10. Can shortcut model selection drift after macOS updates?
11. Does Apple later restore PCC to `fm`?
12. Does Apple later expose Cloud/Cloud Pro through a supported developer API?
13. Should On-Device become a third backend after v1?

---

## 35. Key Design Principles

1. **Test first.**
2. **Shortcuts is transport, not application logic.**
3. **SQLite owns persistence.**
4. **Replay is not native session persistence; say so clearly.**
5. **Never invent unavailable model metadata.**
6. **No fake streaming.**
7. **Local-only by default.**
8. **Serialize calls until concurrency is measured.**
9. **Keep the runner replaceable.**
10. **Make `doctor` excellent.**
11. **Prefer a small useful CLI over framework bloat.**
12. **Preserve exact evidence when Apple beta behavior changes.**

---

## 36. Immediate Next Action for the Next Agent

**The architectural gate has been cleared.** Phases 1 and 2 are done; the transport and the replay-based persistence model are both proven (§3.6, §15, `results/transport-and-persistence-2026-09-01.md`).

Proceed to **Phase 3 — minimal Go runner**, carrying these hard-won constraints into the very first implementation:

1. Always invoke with `--output-type public.plain-text`. The default is RTF.
2. Capture stdout through a pipe (`exec.Command` does this). Never assume a terminal.
3. **Always** apply a context deadline and kill the child. Empty input hangs forever, and macOS has no `timeout(1)`.
4. Reject empty prompts before invoking the runner.
5. Treat exit `0` with empty stdout as `shortcut_no_output`, never as an empty response.
6. Do not expect a trailing newline; do not add one to stored history.
7. Reference bridges by UUID, not name; the imported names currently carry a `.signed` suffix.
8. Keep the default concurrency at 1, but make it configurable. 4 is known to work.

Remaining validation can proceed in parallel with Phase 3, since none of it blocks the runner: §15 Test D, larger replay transcripts, and the offline / Apple-Intelligence-disabled error paths.

---

## 37. Definition of a Successful v1

A successful v1 lets a user do:

```bash
echo "Explain this code" | hollis respond --model cloud-pro
```

and:

```bash
hollis chat --model cloud-pro
```

with chat surviving tool restarts through local SQLite.

It also lets a local application do:

```text
POST http://127.0.0.1:1976/v1/chat/completions
```

and receive a real Cloud/Cloud Pro response through Apple Shortcuts.

The project should be able to say truthfully:

> This is a local gateway to Apple Intelligence Cloud models through the supported macOS Shortcuts execution path. Shortcuts provides the model invocation; the gateway provides CLI ergonomics, optional HTTP compatibility, diagnostics, and local conversation persistence.

It should **not** claim:

> This is Apple’s official PCC developer API.

or:

> This exposes native persistent PCC sessions.

unless future evidence changes that.
