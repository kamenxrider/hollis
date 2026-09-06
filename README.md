# hollis

**Apple Intelligence from your terminal — including both cloud tiers.**

Apple's own `fm` CLI, on the macOS 27 builds tested here, offers exactly one model: the on-device one. Its Private Cloud Compute option is gone.

The Shortcuts **Use Model** action, on the same machine at the same moment, offers four choices — and two of them are separate cloud tiers:

![The Use Model action on macOS 27.0 (26A5421a): Cloud, Cloud Pro, On-Device, ChatGPT](results/img/use-model-picker-26A5421a.png)

`fm --model pcc`, back when it worked, was one generic Private Cloud Compute target with no way to choose between cloud models. Shortcuts draws the distinction, and `/usr/bin/shortcuts` makes it scriptable. Hollis is the CLI and local API over that surface.

```bash
hollis respond "Summarize this repo in one sentence"
hollis respond --model cloud-pro "Analyze this bug"
hollis respond --image photo.jpg "Describe this image"
printf 'long prompt' | hollis respond
hollis chat                       # interactive, remembers the conversation
hollis serve --token-file /private/path/hollis.token  # local OpenAI-shaped API
```

Chats and configuration stay on your Mac. Model requests go to whichever provider you select through Shortcuts.

Measured on **macOS 27.0 (26A5421a and 26A5425a)**. macOS 26 is untested — see [Compatibility](#compatibility).

## Contents

- [Quickstart](#quickstart)
- [Models](#models) and [everyday use](#everyday-use)
- [Persistent chats](#persistent-chats) and [scripts and agents](#scripts-and-agents)
- [Local OpenAI-shaped API](#local-openai-shaped-api)
- [Limits](#what-it-deliberately-does-not-do), [data](#your-data), and [transport](#how-it-works)
- [Why not `fm`?](#why-not-fm)
- [Doctor](#doctor) and [compatibility](#compatibility)
- [Testing](#testing), [evidence](#evidence-and-references), and [quick reference](#quick-reference)

## Quickstart

**1. Download, verify, and install every release artifact.** This secure path
requires the [GitHub CLI](https://cli.github.com/) for build-provenance
verification. It uses a fresh private directory so older files cannot satisfy a
checksum accidentally. The commands run in a fail-fast subshell: a missing or
mismatched checksum or attestation stops before installation, extraction, or
signing.

```bash
(
set -euo pipefail

# Apple Silicon. On Intel, use: HOLLIS_ASSET=hollis-darwin-amd64
HOLLIS_ASSET=hollis-darwin-arm64
HOLLIS_INSTALL_DIR="$(mktemp -d)"
chmod 700 "$HOLLIS_INSTALL_DIR"
cd "$HOLLIS_INSTALL_DIR"
HOLLIS_VERSION="$(gh release view --repo kamenxrider/hollis --json tagName --jq .tagName)"
printf '%s\n' "$HOLLIS_VERSION" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+$'
HOLLIS_RELEASE_URL="https://github.com/kamenxrider/hollis/releases/download/$HOLLIS_VERSION"

curl -fsSL -o "$HOLLIS_ASSET" "$HOLLIS_RELEASE_URL/$HOLLIS_ASSET"
curl -fsSL -o hollis-bridges.zip "$HOLLIS_RELEASE_URL/hollis-bridges.zip"
curl -fsSL -o SHA256SUMS "$HOLLIS_RELEASE_URL/SHA256SUMS"
awk -v asset="$HOLLIS_ASSET" '
  $2 == asset { binary++ }
  $2 == "hollis-bridges.zip" { bridges++ }
  $2 == asset || $2 == "hollis-bridges.zip" { print }
  END { if (binary != 1 || bridges != 1) exit 1 }
' SHA256SUMS > SELECTED_SHA256SUMS
shasum -a 256 -c SELECTED_SHA256SUMS
gh attestation verify "$HOLLIS_ASSET" --repo kamenxrider/hollis
gh attestation verify hollis-bridges.zip --repo kamenxrider/hollis

chmod +x "$HOLLIS_ASSET" && sudo mv "$HOLLIS_ASSET" /usr/local/bin/hollis
unzip hollis-bridges.zip -d bridges

for f in bridges/*.shortcut; do
  shortcuts sign --mode anyone --input "$f" --output "${f%.shortcut}.signed.shortcut"
  open "${f%.shortcut}.signed.shortcut"
done
)
```

**2. Add the bridge shortcuts.** Hollis reaches Apple Intelligence through four
small Shortcuts, one per model tier. They ship unsigned, because a signed
shortcut is an artifact of the Mac that signed it. Shortcuts.app asks you to
**Add Shortcut** for each one. This step is **blocking for model calls**:
`doctor` still runs without the bridges, but reports `MISSING` and exits 3.

The first time a bridge runs, macOS may also ask you to **Allow** model access.

**3. Check it.**

```bash
hollis doctor
hollis respond "Reply with OK"
```

`doctor` tells you which tiers actually resolve on your machine. If a bridge is missing it says so rather than failing later.

### Other install routes

```bash
go install github.com/kamenxrider/hollis/cmd/hollis@latest   # needs Go 1.27+
go build -o "$(go env GOPATH)/bin/hollis" ./cmd/hollis        # from a clone
```

From a clone you can generate the bridges yourself instead of downloading them: `python3 scripts/make-bridge.py bridges/`.

Release binaries and Shortcut files are **unsigned by a Developer ID and not notarized**. Verify the downloaded checksum as shown above; releases also carry a GitHub build-provenance attestation and an SPDX SBOM. For the smallest trust chain, inspect the source and build it yourself with the second command above. Hollis does not claim Gatekeeper approval. If a command seems to be missing after a `git pull`, rebuild: an older binary on `PATH` is usually the cause.

### What you need

A Mac with **Apple Intelligence** enabled, macOS 27 for the measured setup, and `/usr/bin/shortcuts` (included with macOS). Cloud, Cloud Pro and ChatGPT need network access; On-Device works offline. For the ChatGPT bridge, enable the extension in *System Settings → Apple Intelligence & Siri*.

### If something breaks

- `doctor` says `MISSING`: install the bridges from Quickstart step 2. If you renamed one in Shortcuts.app, run `hollis config set bridge <tier> "new name"`.
- A command seems missing after `git pull`: rebuild it with `go build -o "$(go env GOPATH)/bin/hollis" ./cmd/hollis`; an older binary may still be first on `PATH`.
- An OpenAI client fails with `stream: true`: set `stream: false` in the JSON body. Hollis does not read a streaming preference from custom headers.

## Models

| You type | Shortcuts model | Notes |
| --- | --- | --- |
| `auto` | Cloud → On-Device | Default; one local fallback only for unavailable, rate-limited, transient, or empty Cloud results |
| `cloud` | Cloud | "Great, fast answers" — Apple's server model on Private Cloud Compute |
| `cloud-pro` | Cloud Pro | "Increased reasoning" — macOS 27+; slower than Cloud, stronger on harder prompts |
| `on-device` | On-Device | Runs locally, works offline |
| `chatgpt` | ChatGPT | Apple's ChatGPT extension, not an Apple model |

Cloud and Cloud Pro are genuinely different selections, not two names for one endpoint — that is the distinction `fm` never exposed. Apple publishes no stable backend model IDs for these choices, and hollis does not invent them.

```bash
hollis models                        # what resolves on this Mac
hollis config set model cloud-pro    # persist a default
```

## Everyday use

```bash
hollis respond "Summarize this repo in one sentence"
printf 'Explain closures in Go' | hollis respond
hollis respond --model cloud-pro "Analyze this bug"
hollis respond model cloud-pro "same thing, model before the prompt"
hollis respond --timeout 90s "A question worth waiting for"
```

The prompt comes from the argument, `--prompt-file`, or stdin, so pipelines work. Each `respond` call is stateless. Default timeout is 30 seconds, ceiling 120. Hollis rejects a rendered prompt over 128 KiB before invoking Apple.

### Text documents (unreleased)

```bash
hollis respond --prompt-file instructions.txt
hollis respond "Summarize the differences" --file first.md --file second.txt
```

`--prompt-file` reads the instruction exactly as UTF-8 text. Repeat `--file` to append local `.txt` and `.md` documents in order, with their basenames and explicit boundaries. The instruction, boundaries and document contents together must fit the 128 KiB prompt limit; Hollis rejects oversized input without truncation. Empty, invalid UTF-8 and nonregular files are rejected. PDF is not supported.

Choose one instruction source. File requests require positional text or `--prompt-file` and reject nonempty piped stdin. Documents cannot be mixed with `--image` in one request. Documents are prompt content; their boundaries do not isolate untrusted instructions.

### Image generation (unreleased, experimental)

Generate one PNG through a separately installed Image Playground Shortcut:

```sh
hollis config set image-bridge "Hollis Image - Reference Input v2"
hollis image generate "A red circle on a white background" \
  --style animation --output circle.png
hollis image generate "A blue square on a white background" \
  --style animation --output square.png --agent
```

This makes one attempt, with no retries or model fallback. The destination must
end in `.png`; existing files and symlinks are never overwritten. The parent
folder must already exist. Output is private by default. JSON/agent output
returns the saved path, format, bytes, dimensions and SHA-256 checksum.

The source-generated, signed **Hollis Image - Reference Input v2** Shortcut can
serve all six style IDs: `any`, `animation`, `genmoji`, `illustration`, `sketch`,
and `chatgpt`. Generate and import it using the setup in the
[image-generation guide](docs/image-generation.md#setup-generated-parameterized-shortcut),
then configure it with `hollis config set image-bridge <shortcut-name>` and use
`--style <style>`. `hollis image styles` shows the routes. Existing per-style
fixed Shortcut mappings remain supported. The guide records the paced runtime
acceptance and its visual-continuity boundary.

Explicit `--aspect-ratio W:H --fit crop|pad` or `--size WIDTHxHEIGHT --fit
crop|pad` processes the output locally. These are not native model controls.
Image generation is also available explicitly during chats and through the
API. Local/API reference inputs and automatic reuse of a trusted prior image
are implemented; they guide a new generation and do not promise pixel-perfect
editing. See the [image reference contract](docs/image-references.md).
The default and maximum timeout is 120 seconds. Hollis limits each generated
PNG to 16 MiB and 16 million pixels; these are application limits, not Apple
quotas or selectable resolution settings.

See [image-generation setup and capability evidence](docs/image-generation.md).
The four standard text bridges do not provide image generation; this optional
Shortcut is configured separately. `hollis doctor` checks the standard bridges,
not the image Shortcut passed with `--bridge`.

### Images

`respond` accepts PNG and JPEG files through the existing bridges:

```bash
hollis respond --image photo.jpg "What is this?"
hollis respond --model cloud-pro --image a.png --image b.png "Compare them"
```

An image request with no selected or configured model defaults directly to Cloud. Cloud and Cloud Pro accept repeated `--image`; ChatGPT accepts one image. `auto` and On-Device are rejected for images because the tested On-Device Shortcut ignored the pixels, making automatic fallback unsafe.

Images must be direct regular PNG/JPEG files, at most 64 MiB and 64 million pixels each. Hollis rejects symlinks in the file or its parent directories, apart from macOS's standard root-owned `/var`, `/tmp` and `/etc` aliases. Use the real path for an image reached through a custom directory link. The runner validates a private byte snapshot and passes only that snapshot to Shortcuts; staged images are removed after success, failure or cancellation.

When images are present, give the prompt as an argument or with `--prompt-file`. Hollis writes it to a private temporary UTF-8 text file and passes that file plus the images as repeated Shortcuts inputs; the temporary prompt is deleted after the run. Do not pipe a second prompt through stdin with `--image`. Image chat history remains unsupported. The unreleased HTTP image-input contract is described below; released `v0.2.0` supports images through the CLI only.

## Paced folder processing (unreleased)

```bash
hollis batch plan --input-dir ./inbox --prompt-file instructions.txt \
  --model cloud --output-dir ./results --job ./job.json
hollis batch run --job ./job.json --max-calls 12
hollis batch resume --job ./job.json --max-calls 12
```

`plan` reads a sorted, nonrecursive inventory of `.txt`, `.md`, `.png`, `.jpg` and `.jpeg` files and records skipped entries. It makes no model calls. Instructions must be outside the input inventory; job and output destinations must be outside the input folder. Symlinks, existing reserved outputs, invalid text and oversized prepared prompts are rejected. The instruction and each text document together must fit 128 KiB. Image files have a 64 MiB per-file Hollis limit; planning checks readable bytes. Execution also validates PNG/JPEG content and a 64-million-pixel limit before calling a model.

Choose a concrete model explicitly. On-Device supports text-only jobs; image jobs use Cloud, Cloud Pro or ChatGPT. Every run or resume requires a new `--max-calls` budget from 1 to 100. A budget-limited run pauses cleanly. Output distinguishes attempts in this invocation from lifetime attempts. There is one call per file, no automatic model switching, and no automatic retry.

Cloud and ChatGPT calls wait at least 15 seconds after completion before the next call; Cloud Pro waits 45 seconds. The pacing checkpoint survives a restart. These are conservative Hollis defaults, not an Apple quota guarantee. Cancellation, a provider failure or a persistence failure stops further calls.

Resume verifies completed results and checks source hashes before continuing. If an interrupted call has no verifiable saved result, its status is **uncertain**. Choose `--retry-uncertain` to risk repeating that call, or `--skip-uncertain` to leave it unresolved and process pending items. Failed items require `--retry-failed` before more work proceeds. Exactly-once provider execution across a crash cannot be guaranteed. Changed inputs require a new plan.

Job manifests contain local paths, hashes and status, but no prompt or response content. Private `.response.json` files intentionally contain the model response and recovery metadata. Existing result files are never overwritten; the job manifest is updated atomically. An existing result directory must already be private (0700 or stricter); Hollis does not change its permissions. Inspect the manifest and results before sharing them. This is a finite command, not a background folder watcher.

Run and resume keep a stable `.lock` beside the manifest. Hollis rejects a job
location when that directory or one of its resolved ancestors is owned by an
untrusted local account, or is writable by other accounts without sticky-bit
protection. Put job manifests in a private directory you own; user-owned
directories beneath a standard sticky temporary directory remain supported.
On macOS, Hollis also checks extended ACLs on the lock and its directory chain.
Allow entries that grant mutation rights are rejected; read-only and deny
entries remain supported. An unreadable or unrecognized ACL fails closed.

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

Run `hollis chat` with no argument in a terminal for an interactive session — blank lines are skipped, Ctrl-D ends it, and `--continue <id>` resumes an existing conversation:

```
$ hollis chat
New chat · auto · 6f2c…
> What is a closure?
< A closure is a function that captures variables from its surrounding scope.
> Give me one in Go.
< func counter() func() int { i := 0; return func() int { i++; return i } }
```

Managing them:

```bash
hollis chats list
hollis chats show <id>            # metadata plus every message
hollis chats search VANTA-ORBIT   # full-text (FTS5)
hollis chats rename <id> "New title"
hollis chats delete <id> --yes
```

The whole search query is treated as one phrase, so hyphenated tokens like `VANTA-ORBIT` match verbatim. Archived conversations are skipped.

A continuation always uses the model stored with that conversation. Combining `--continue` with a positional model or explicit `--model` is rejected. Hollis stores complete turns atomically and preserves complete history; when a chat reaches 256 stored messages or its rendered prompt exceeds 128 KiB, it fails clearly instead of trimming or summarizing it.

## Scripts and agents

```bash
hollis respond --agent "Name three Go testing tips"   # JSON + non-interactive
hollis models --json
hollis doctor --json --select bridges
hollis agent-context                                  # machine-readable CLI description
```

JSON output carries both `model_requested` (what you asked for) and `model_used` (what
answered). They differ when `auto` falls back from Cloud to On-Device — the two
are not interchangeable, so the substitution is reported rather than hidden. In
human mode that fallback prints one line to stderr, leaving stdout clean for
pipelines. The local API reports the serving tier in its `model` field, as
OpenAI does.

`--agent` is shorthand for `--json --no-input` on data commands. In `--no-input` mode hollis never waits on a terminal, and destructive commands require explicit confirmation flags (`hollis chats delete <id> --yes`). Long-running `serve` and shell `completion` support human output only and reject JSON/agent mode clearly.

Exit codes are stable and parseable:

| Code | Meaning |
| --- | --- |
| 0 | Success |
| 1 | Unexpected internal error |
| 2 | Usage error — bad flag, unknown model, empty prompt, no matching command |
| 3 | Missing resource — unknown conversation id, no search hits, bridge not installed |
| 5 | Discovery, transport, or Apple rate-limit failure |
| 7 | Timeout — the run exceeded its deadline and was killed |
| 10 | Config or database error |

## Local OpenAI-shaped API

```bash
umask 077
openssl rand -base64 48 > hollis.token
{ printf 'Authorization: Bearer '; cat hollis.token; } > hollis.headers
hollis serve --token-file hollis.token          # http://127.0.0.1:1978
curl -s localhost:1978/health
curl -s localhost:1978/v1/models -H @hollis.headers
curl -s localhost:1978/v1/chat/completions \
  -H @hollis.headers \
  -H 'Content-Type: application/json' \
  -d '{"model":"cloud-pro","stream":false,"messages":[{"role":"user","content":"Explain closures in Go."}]}'

curl -s localhost:1978/v1/responses \
  -H @hollis.headers \
  -H 'Content-Type: application/json' \
  -d '{"model":"cloud-pro","stream":false,"input":"Explain closures in Go."}'
```

The Responses reply text is at `output[0].content[0].text`. Its `input` may be a string or a message array, with optional `instructions`. `/v1/models` lists `auto` plus only the tiers whose bridges resolve here, so `cloud-pro` disappears when its bridge is not installed.

### Inline image input (unreleased)

Both endpoints accept PNG/JPEG images as base64 data URLs in the final user message, alongside nonempty text. Earlier messages must remain text-only. Remote URLs and server file paths are rejected.

Chat Completions uses this message content shape:

```json
[{"type":"text","text":"Describe this image"},{"type":"image_url","image_url":{"url":"data:image/png;base64,..."}}]
```

Responses uses this input message content shape:

```json
[{"type":"input_text","text":"Describe this image"},{"type":"input_image","image_url":"data:image/png;base64,..."}]
```

Omitting `model` on an image request selects `cloud`. Explicit `auto` and `on-device` are rejected. Cloud and Cloud Pro accept up to three images; ChatGPT accepts one. The HTTP request body remains limited to 8 MiB. Decoded images together may occupy at most 4 MiB, with at most 16 million pixels per image and 24 million pixels combined. These are Hollis limits, not reported Apple quotas. Invalid formats return 400 and oversized input returns 413 before a model runs.

Hollis validates and stages image bytes privately, holds a concurrency slot through staging and execution, then removes staged files. Responses remain complete text, without streaming or invented usage counts.

Port **1978**, not 1976 — `fm serve` uses 1976 throughout Apple's own examples, and two servers cannot share a port.

Model work is serialized by default. `--max-concurrency` accepts 1–4; work beyond the limit is rejected immediately with HTTP 429 and `Retry-After: 1`. `/health` remains available independently of model capacity. Request headers have 5 seconds, reads 15 seconds, writes 125 seconds, idle connections 60 seconds, and shutdown gets five seconds to finish.

### Clients: listing is not the same as working

Point an OpenAI-compatible client at `http://127.0.0.1:1978/v1` and it will usually **see** the models. That does not mean it can **call** them.

Set `stream: false` in the **JSON body**. A custom HTTP header does not count; Hollis never reads streaming from headers. `stream: true` returns **400** (`use stream=false`). Clients that always stream — Osaurus Chat is one — will populate the model picker from `/v1/models` and then fail every completion. Clients that can turn streaming off (Aider: `stream: false` plus per-model `streaming: false`) do work.

The v0.2 API does not accept `tools` or return native function calls. The
underlying Shortcut returns one block of text. Separate live probes show that
the models can sometimes follow a prompt-defined, client-executed tool protocol,
including consuming a supplied tool result, but emitting the call was not
reliable enough to ship. That path remains explicitly experimental work for a
later release; Hollis never executes tools server-side.

### What it deliberately does not do

Shortcuts returns a complete response rather than a token stream, so `stream: true` returns **400** instead of a faked stream. Apple exposes no token counts through this path, so no `usage` field is invented. The v0.2 HTTP contract has no `tools` / function calls. `system` and `instructions` are advisory prompt content, not hard isolation boundaries. Both model routes reject malformed or trailing JSON, unknown fields, unsupported parameters/content, empty input, and prompts over 128 KiB before calling a model.

Authentication is required by default and is configured with `--token-file <private-file>` or `HOLLIS_API_TOKEN`; the token must contain at least 32 bytes and is never printed. `/v1/*` expects `Authorization: Bearer <token>`, while `/health` stays unauthenticated. Hollis deliberately has no command-line `--token`, because process arguments can be visible through `ps`. `--no-auth` is an explicit opt-out for a resolved loopback listener only; it lets every local process invoke the configured models and cannot be combined with `--allow-remote`.

Binding outside loopback requires **both** `--allow-remote` and authentication. Hollis does not provide TLS: expose it only through an encrypted trusted path such as Tailscale, WireGuard, or an SSH tunnel.

## Your data

Everything hollis stores lives in one directory:

```
~/Library/Application Support/hollis/
├── hollis.db       conversations, messages, and per-run diagnostics
├── hollis.db.chat.lock  cross-process chat-continuation lock
├── config.json.lock     cross-process config update lock
└── config.json          default model and any bridge overrides
```

The state directory is mode `0700`; its database, config, lock, migration backups, and temporary files are mode `0600`. Set `HOLLIS_STATE_DIR` to an **absolute** directory for an isolated test or alternate state location. Existing Hollis-owned modes are tightened without changing broader parent directories.

Run diagnostics contain only request ID, requested/used tier, timing, exit code, error class, fallback, and byte counts — never prompts, replies, or raw Apple stderr. Conversation deletion removes its messages, run records, and search entries in one transaction. To delete everything Hollis knows, remove that directory. To delete one conversation, `hollis chats delete <id>`.

## How it works

```text
hollis → /usr/bin/shortcuts → Use Model → Cloud / Cloud Pro / On-Device / ChatGPT
```

Each bridge shortcut is three actions: **Receive** input, **Use Model**, **Stop and Output**. Text-only requests feed the prompt on stdin. Image requests pass a private temporary prompt file and the image files together as repeated inputs. Hollis captures plain text back. It does not patch or modify `fm`, and it holds no credentials — the transport is the local Shortcuts app, running as you.

### Bridge discovery

Shortcuts UUIDs differ on every Mac, so hollis resolves each tier at runtime, in order:

1. An explicit configured bridge
2. A stable bridge name found via `shortcuts list`
3. A compiled development UUID — kept only as an unverified candidate, never treated as proof the bridge exists

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
| With file input, piped stdin does not reach the model | Passes the prompt text file and image files together through repeated `--input-path` arguments |
| Responses are complete, not streamed | Does not fake streaming |

The measured evidence behind these rules is summarized in [EVIDENCE.md](EVIDENCE.md).

### The ChatGPT quirk

Measured on macOS 27 during development: a **signed-in** ChatGPT account made the Shortcuts extension fail with `login could not be verified`, and logging out let the bridge work. The macOS ChatGPT extension does not require an account for basic use.

Image Playground's separate **ChatGPT** style has a different scoped result:
on macOS 27.0 build `26A5425a`, the fixed, parameterized, native-Shortcuts,
and fresh-extension routes failed before inference with Apple's Shortcuts
ToolKit database sandbox denial, while the native Image Playground app worked.
This does not prove universal impossibility; Hollis does not silently retry or
substitute that route. See the [image-generation qualification note](docs/image-generation.md#setup-generated-parameterized-shortcut).

## Why not `fm`?

Apple ships its own Foundation Models CLI, `fm`, and hollis neither patches nor replaces it. Two things make Shortcuts the more capable path today.

**Granularity.** Even when `fm` supported Private Cloud Compute, its selector was a single `pcc` target — one generic cloud model, with no way to ask for a specific tier. Shortcuts exposes Cloud and Cloud Pro as separate choices, so hollis can offer a distinction the CLI never had.

**Availability.** On macOS 27.0 builds `26A5421a` and `26A5425a`, `fm` lists only `system`, and `fm available --model pcc` is rejected at argument validation — including from Terminal.app, so this is not a Warp/PTY quirk. Whether that is deliberate or a beta regression, Apple has not said.

There is also a reason not to link the framework directly. Apple gates third-party PCC access behind an entitlement, App Store Small Business Program enrollment, and a two-million-download ceiling; a non-entitled binary calling `PrivateCloudComputeLanguageModel` fails with `ModelManagerError 1046`. That entitlement gates the **developer framework, not the user-facing automation surface** — Shortcuts is a shipped consumer feature, `shortcuts run` is a documented Apple CLI, and the bridges are shortcuts you could build by hand in a minute. Hollis automates a supported surface rather than working around a restriction.

That surface is one Apple can change in any build, exactly as it changed `fm` in this one, which is why every claim here names the build it was measured on.

Prior art: bridging to Apple Intelligence through a Shortcut was shown by **Joseph Humfrey** in [*The Shortcut to integrating Private Cloud Compute into my app*](https://joethephish.me/blog/the-shortcut-to-integrating-PCC/) (June 2025). Hollis is the hardened version of that idea plus the tier selection. A documented web and GitHub search on 2026-09-03 found many on-device or single-`pcc` CLIs, but no other public CLI exposing the two Shortcuts choices separately. Hollis is therefore, **to our knowledge**, the first public CLI to expose both Cloud and Cloud Pro—not the first Shortcut bridge or Apple-model CLI. Full scope, counterexamples, and falsification conditions: [EVIDENCE.md](EVIDENCE.md).

## Doctor

```bash
hollis doctor
hollis doctor --json
```

```text
$ hollis doctor
hollis doctor (version 0.2.0)
  transport: ok
  macos: 27.0 (26A5421a)
  support: macOS 27 measured; Cloud Pro unsupported on macOS 26
  timeout default: 30s (ceiling 120s)
  bridges (resolved at runtime):
    [OK]        cloud      AFM Bridge - Cloud.signed (shortcuts-list)
    [MISSING]   cloud-pro  DBB6E472-CBC6-4421-8D32-9D4543D5CDE6 (compiled-uuid)
    [OK]        on-device  AFM Bridge - On-Device.signed (shortcuts-list)
    [OK]        chatgpt    AFM Bridge - ChatGPT.signed (shortcuts-list)

  MISSING: install that bridge (README “Quickstart” step 2), or point config at one you already have:
  hollis config set bridge <tier> <name-or-uuid>
```

`doctor` exits 0 only when every tier supported by the detected macOS version is verified through `shortcuts list`. A missing supported bridge exits 3, discovery or transport failure exits 5, and unknown or unverified state exits 10. Cloud Pro is informationally unsupported on macOS 26. An explicit configured reference may still be attempted, but remains labelled unverified until its name is visible in discovery.

JSON output adds the macOS version and build, each bridge's `resolved_ref`, its resolution `source`, verification state, and `status` (`ok`, `missing`, `unsupported`, or `unverified`).

## Compatibility

**macOS 27 — measured.** On `26A5421a` and `26A5425a` the Shortcuts selector exposes Cloud, Cloud Pro, On-Device and ChatGPT, and all four bridges work.

**macOS 26 — experimental, untested.** macOS 26 Shortcuts exposed three model locations: Cloud, On-Device and ChatGPT. There was no Cloud Pro, and hollis refuses `cloud-pro` when that bridge is unavailable. The macOS 26 `cloud` choice is the earlier PCC model generation, not the 27 Cloud / Cloud Pro pair.

```bash
python3 scripts/make-bridge.py --os 26 bridges/
hollis doctor
hollis respond --model cloud "Reply with OK"
```

Please include `hollis doctor` output when reporting macOS 26 results — that is the fastest way for this to stop being untested.

## Model notes

The Shortcuts choices correspond, at family level, to Apple's [third-generation Foundation Models](https://machinelearning.apple.com/research/introducing-third-generation-of-apple-foundation-models):

| Hollis | Shortcuts | Model family |
| --- | --- | --- |
| `cloud` | Cloud | AFM 3 Cloud via Private Cloud Compute |
| `cloud-pro` | Cloud Pro | AFM 3 Cloud Pro |
| `on-device` | On-Device | AFM 3 Core family |
| `chatgpt` | ChatGPT | OpenAI through Apple's extension |

Apple exposes no stable backend IDs through this interface, so these mappings stay at the family level deliberately. A separate ADM 3 Cloud family powers image features such as Genmoji and is not reachable through the **Use Model** text action. [Private Cloud Compute](https://developer.apple.com/private-cloud-compute/) is documented with a 32K context window; on-device models have a smaller working context and are more sensitive to prompt length.

## Testing

```bash
test -z "$(gofmt -l .)"
go vet ./...
go test ./...
go test -race ./...
go build ./cmd/hollis
```

The default suite is provider-free: subprocess tests inject deterministic runners without a production backdoor, HTTP uses `httptest`, and bridge generation is checked for both macOS profiles. CI runs the race suite on an official macOS Go 1.27 runner before packaging.

Non-draft pull requests from branches in this repository to the default branch
also receive an automated Poolside review of a bounded PR diff. The reviewer
receives no repository tools and does not execute checks; normal provider-free
tests run in CI.

The separately gated live suite needs the exact built binary and invokes real Shortcuts models, so run it only when those calls are intended:

```bash
HOLLIS_LIVE=1 HOLLIS_BIN=/absolute/path/to/hollis \
  go test -tags=hollis_live ./internal/integration -run TestLiveRealSystem -v
```

It uses a temporary absolute `HOLLIS_STATE_DIR`, quiet prompts, an ephemeral loopback port, and cleans up only the conversation it creates. Its six Cloud Pro calls are serialized with at least 45 seconds between them, with no retries; any rate limit stops that lane.

## Evidence and references

* [Hollis evidence and prior-art scope](EVIDENCE.md)
* [Apple: Prompting an on-device foundation model](https://developer.apple.com/documentation/foundationmodels/prompting-an-on-device-foundation-model)
* [Apple: Adding server-side intelligence with Private Cloud Compute](https://developer.apple.com/documentation/foundationmodels/adding-server-side-intelligence-with-private-cloud-compute)

## Quick reference

```bash
hollis respond "prompt"                    # one-shot response
hollis respond --model cloud-pro "prompt"  # explicit Cloud Pro
hollis respond --image photo.jpg "describe" # image input; defaults to Cloud
hollis chat "remember this"                # start a persistent chat
hollis chat --continue <id> "follow up"     # continue a chat
hollis chats list                           # list stored chats
hollis chats show <id>                      # show one chat
hollis chats search "query"                 # search stored chats
hollis serve --token-file hollis.token      # authenticated local API
hollis doctor                               # check transport and bridges
hollis models                               # show available tiers
hollis config set model cloud-pro           # save a default tier
```

## License

Apache-2.0 — [LICENSE](LICENSE)
