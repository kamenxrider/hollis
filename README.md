<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/assets/hollis-logo-dark.svg">
  <img src="docs/assets/hollis-logo.svg" alt="hollis — Your Mac has more to say." width="680">
</picture>

**Apple Intelligence’s Cloud and Cloud Pro models in your terminal, scripts and AI agents.**

Hollis brings Apple’s model choices to your command line and your agents. No
model API key, no account to create and no per-request charge from Hollis.

- **Apple’s Cloud and Cloud Pro models.** Two distinct Private Cloud Compute
  selections from Shortcuts’ **Use Model** action, chosen explicitly and never
  silently switched.
- **On-device answers with `local`.** Apple’s native Foundation Models SDK on
  your Mac: offline, streaming in the terminal, with guided JSON output, one
  image per request and tool calling.
- **PDFs and scans.** Text and OCR extraction on your Mac before any model sees
  the document.
- **Image generation.** Animation, Illustration, Sketch, Genmoji and Any Style,
  saved as PNG files, from the terminal, a chat or the API.
- **Saved chats.** Conversations stay on your Mac and can be continued and searched.
- **Batch processing.** A folder of documents or images with a preview, a call
  budget and resumable progress.
- **An agent plugin** for Claude Code and Codex, and an OpenAI-compatible API on
  localhost for other clients.

```bash
hollis respond --model local "Explain a race condition briefly"  # streams output
hollis respond "Explain closures in Go in three sentences"
hollis respond --model cloud-pro --file README.md "Summarize this document"
hollis respond --image photo.jpg "Describe this image"
cat prompt.txt | hollis respond
hollis chat                          # remembers the conversation
hollis image generate "A red sailboat" --style illustration --output boat.png
```

Local-only `respond` and `chat` accept opt-in `--retry` (3 extra attempts) or
`--retry=N` (0–5); retry is off unless supplied. Cloud, Cloud Pro, on-device,
ChatGPT, auto and image generation are never retried.

## What's new in 0.7.0

- [`-s` / `--system-file`](docs/cli.md#instructions-one-shot-conversations-and-local-checks): inline instructions or an instruction file.
- [`--messages`](docs/cli.md#instructions-one-shot-conversations-and-local-checks): pass a one-shot conversation, including host context.
- [`-q`](docs/cli.md#instructions-one-shot-conversations-and-local-checks): hide notes and progress, not answers or errors.
- [`--count-tokens`](docs/cli.md#instructions-one-shot-conversations-and-local-checks): check Local input fit without generating.
- [`--permissive`](docs/cli.md#instructions-one-shot-conversations-and-local-checks): opt-in Local text transformations; refusals remain possible.
- [`--retry`](docs/cli.md#opt-in-local-retry): opt into bounded retries for transient Local busy failures only.
- [`--context-strategy`](docs/cli.md#opt-in-conversation-shortening): explicitly shorten a request without changing stored history.
- [`benchmark`](docs/cli.md#local-speed-test): measure Local speed with fixed prompts and serial calls.
- [`update`](docs/cli.md#checking-for-updates): explicitly check for a stable release and get upgrade guidance.
- [`stream_options`](docs/api.md#chat-completions-app-compatibility): request a final Local stream usage chunk.
- [`json_object`](docs/api.md#guided-json-output): validated, complete-only JSON on Local (guided output) and Shortcut routes (JSON bridges plus validation).
- [`n` / `logprobs` / `user`](docs/api.md#chat-completions-app-compatibility): accept single-answer, disabled-probability and ignored-label client fields.

<!-- SCREENSHOT PLACEHOLDER: replace with a real terminal screenshot of a
`hollis respond --model local` run before release. Do not use a mock-up. -->

**Choose your setup:** [CLI](#quick-start) ·
[Claude Code / Codex plugin](plugins/hollis/README.md#install) ·
[What leaves your machine](#what-leaves-your-machine) ·
[Limits and caveats](#limits-and-caveats)

JSON requests on Shortcut routes use four `AFM Bridge JSON - <Route>`
Dictionary bridges. On success, `respond --format json` and API `json_object`
return one validated JSON object; invalid output fails explicitly with
`invalid_json_output`. A request can still fail or return wrong data.
Shortcut routes do not enforce requested fields, types or values: models may
add keys, use `false` or `"null"` instead of null, nest answers or change
punctuation. Local `--schema` constrains structure and types to the supported
schema subset, not factual correctness.
`hollis setup --import cloud` imports both Cloud bridges; missing JSON bridges
retain text behavior with an import hint. See [JSON output](docs/cli.md#json-object-responses).
Shortcuts may spell 19.95 as `19.949999999999999`; Hollis preserves that spelling,
so financial or exact-decimal callers should use decimal strings or round explicitly.

## Requirements

Check these before installing:

- An **Apple-silicon Mac** running **macOS 27**. macOS 26 is untested, and
  Intel Macs do not support Apple Intelligence inference
  ([compatibility](docs/compatibility.md)).
- **Apple Intelligence** enabled in *System Settings → Apple Intelligence &
  Siri*, with its model downloads complete.
- Optional [GitHub CLI](https://cli.github.com/) (`gh`) for installer build-provenance
  verification. Without it, the published installer verifies SHA-256 checksums only.
  Plugin setup does not require it.
- Network access for Cloud, Cloud Pro and ChatGPT. On-Device and native local
  work offline.
- For ChatGPT, enable the extension in *System Settings → Apple Intelligence & Siri*.
- Complete Apple’s first-use approvals. Image generation needs an unlocked Mac.

<a id="quickstart"></a>
<a id="install-verified"></a>
<a id="install"></a>

## Quick start

Start with `local`: it runs on your Mac and needs no Shortcut import. Using
Claude Code or Codex? Start with the [plugin installation](plugins/hollis/README.md#install) instead.

**1. Install.** Homebrew installs the matched CLI and native helper from the
Hollis tap:

```bash
brew install kamenxrider/tap/hollis
```

Or download and inspect the [curl installer](scripts/install.sh), then run it.
It installs the published ARM64 bundle's matched CLI and native helper in
`~/.local/bin`, without model calls or `sudo`. The published installer works
without `gh`: it verifies the release-pinned SHA-256 and release `SHA256SUMS`
only. With `gh`, it also verifies GitHub build provenance. The source
placeholder must be pinned before publication.

```bash
curl -fsSLo install-hollis.sh https://raw.githubusercontent.com/kamenxrider/hollis/main/scripts/install.sh && \
  bash install-hollis.sh
export PATH="$HOME/.local/bin:$PATH"
```

Add `~/.local/bin` to your shell’s PATH permanently if needed. Inspect the
[installer](scripts/install.sh) first, or [verify manually](#verification-and-trust).

The formula checks the bundle's file hashes. Homebrew users can optionally run
the loopback API with `brew services start hollis`; the default service has no
authentication, so any local process can use it. See the
[Homebrew notes](packaging/homebrew/README.md#background-api) for token-file
configuration and service upgrade steps.

**2. Check native local and ask.**

```bash
hollis doctor --model local
hollis respond --model local "Reply with OK"
```

`doctor --model local` checks only native availability, without Shortcuts
discovery, and exits nonzero when the route is not ready
([selected-route diagnostics](docs/selected-route-setup.md)).
`hollis models --model local` shows the same availability. A successful answer
checks inference. If the model is not ready, check that Apple Intelligence is
enabled and its downloads are complete.

Measure Local speed with `hollis benchmark --runs 3`: fixed prompts, serial calls, no network; [measurements and JSON](docs/cli.md#local-speed-test).

You can also skip `respond`: `hollis "Reply with OK"` sends a prompt to your
default model, `hollis chat` starts an interactive session (type `help` or
`quit`), and `hollis examples` prints copyable recipes.

<a id="add-shortcuts"></a>

**3. Optionally add the Shortcuts routes.** Shortcuts are optional for native
Local. For Cloud, Cloud Pro, On-Device, ChatGPT or images, run
`hollis setup --import`, or select routes with
`hollis setup --import cloud chatgpt`. This command needs the complete release
bundle or Homebrew layout. Confirm the listed imports, choose **Add Shortcut**
in each window, then run `hollis doctor`.

The curl installer also opens the five bundled import windows; Homebrew does
not. When updating an older image bridge, replace it with
**Hollis Image - Reference Input v2**. Existing custom image-bridge settings
are preserved. The first request may also require Apple's **Allow** approval.

```bash
hollis doctor
hollis respond "Reply with OK"
```

`doctor` checks setup and availability for every route, and reports missing
bridges even when native local works.

**Upgrading?** A newer release may upgrade local chat storage to a schema that an
older binary cannot read, and switching back does not undo that upgrade. Close
older Hollis processes, including `hollis serve`, first and read the
[database upgrade and recovery steps](docs/cli.md#database-upgrades-and-recovery).
Run `hollis update` to check the latest stable GitHub release and print the
upgrade command for your installation (`--json`/`--agent` are supported).
Checks happen only when you run this command; there are no automatic checks.
For Homebrew installs, `hollis update --apply` asks before running
`brew upgrade kamenxrider/tap/hollis`; scripts need `--apply --yes`.
Installer and manual installs only receive instructions, never an automatic
download or installer execution. See [update details](docs/cli.md#checking-for-updates).

## Models

Five model choices: four through Shortcuts, one native. `auto` is a routing strategy.

| You type | Runs through | Notes |
| --- | --- | --- |
| `cloud` | Shortcuts → Private Cloud Compute | Apple’s “Great, fast answers” selection |
| `cloud-pro` | Shortcuts → Private Cloud Compute | Apple’s “Increased reasoning” selection |
| `on-device` | Shortcuts → your Mac | Works offline; complete responses |
| `local` | Native SDK → your Mac | Text and one PNG/JPEG image; streams in a terminal; measured usage on complete responses; `--schema` and HTTP guided JSON output; caller tools and MCP tools |
| `chatgpt` | Shortcuts → Apple’s ChatGPT extension | Not an Apple model; enable the extension in System Settings → Apple Intelligence & Siri |
| `auto` | Cloud, then Shortcuts On-Device | Default; one fallback only for a confirmed missing bridge. Provider limits stop without retry or fallback |

Cloud and Cloud Pro are distinct selections in Shortcuts’ **Use Model** action.
Apple does not publish stable backend model IDs for these choices.

![Use Model on macOS 27.0 (26A5421a): Cloud, Cloud Pro, On-Device, ChatGPT](results/img/use-model-picker-26A5421a.png)

```bash
hollis models
hollis config set model cloud-pro
```

## What leaves your machine

Hollis reads only what you supply with an argument, `--prompt-file`, `--file`,
`--image`, `--system-file`, `--messages`, stdin, the API or your agent. It does not read your workspace on its
own. Where the request goes depends on the route you select.

| Route or feature | Where your prompt and attachments go |
| --- | --- |
| `local` | Stays on your Mac. Apple’s on-device model runs through the native SDK. |
| `on-device` | Stays on your Mac. Shortcuts runs Apple’s on-device model selection. |
| `cloud`, `cloud-pro` | Apple’s Private Cloud Compute, through Shortcuts. The rendered prompt, attached document text, chat history being replayed and images are sent. |
| `chatgpt` | Apple’s ChatGPT extension sends the request to ChatGPT under that extension’s terms. |
| `auto` | Cloud first; at most one fallback to Shortcuts On-Device. Never native local. |
| Image generation | Apple’s Image Playground, through your image Shortcut, with the prompt and any reference image. Apple decides where Image Playground processes the request; Hollis has not measured it. |
| PDF input | The helper extracts text on your Mac. Extracted text, not PDF bytes, travels with the prompt to whichever route answers, including Cloud, Cloud Pro and ChatGPT; `auto` follows its routing policy. Extraction alone does not make the request local. |
| MCP tools | Explicit `--mcp`/`--mcp-server` calls send approved arguments to the selected local program or HTTP server. Framed results go only to the local model and, for chat, its stored transcript. Servers are never attached implicitly or by `serve`/`batch`. See [MCP servers](docs/mcp.md). |
| `hollis serve` | Listens on `127.0.0.1` by default. Requests go to the route they name, as above. |
| Agent plugin | Your agent host sees the context it sends and Apple’s answer. A cloud-hosted agent’s provider receives that too, under its own privacy policy. |

Configuration, chats and run history stay in a local state directory
([your data](#your-data-and-uninstalling)). Prompts use private temporary files,
removed after requests.

## Everyday use

### Documents and chats

```bash
hollis respond --prompt-file instructions.txt
hollis respond --file first.md --file second.txt "Summarize the differences"
hollis respond --file report.pdf --pages 1,4-6 "Summarize these pages"
hollis chat
hollis chat --continue <id> "What did we decide?"
hollis chats list
```

Attach UTF-8 text files of any extension, or PDFs. A PDF is read on this Mac by the
paired native helper, from its text layer with OCR for pages that have none;
`--pages` selects pages of a single PDF. Extraction is CLI-only, not an HTTP
feature. The extracted page text, not the PDF bytes, goes to whichever answering
route you select, including cloud, cloud-pro, or ChatGPT. Use `--model local`
to keep the answer generation on this Mac too. Each `respond` call is
stateless. Chats store and replay the conversation. The complete rendered prompt
must fit 128 KiB; oversized requests and native context-capacity failures produce
errors without silently shortening history. Default timeout is 30 seconds, with a
120-second ceiling. [CLI and data guide](docs/cli.md).

Opt in to request-only shortening with `chat --context-strategy drop-oldest`
(also `sliding-window` or Local-only `summarize`); `respond --messages` supports
the same policies. Stored history is unchanged and every drop is disclosed.

### Image generation and understanding

```bash
hollis image generate "A brass turtle carrying a tiny greenhouse" \
  --style illustration --output turtle.png
hollis image generate "A lighthouse at sunset" \
  --style sketch --aspect-ratio 16:9 --fit crop --output banner.png
hollis respond --model cloud-pro --image a.png --image b.png "Compare them"
```

Generation saves a PNG to a new filename in an existing folder. Animation,
Illustration, Sketch, Genmoji and Any Style returned images in tests. Ratios and
sizes use local crop/pad/resize after generation.

For image understanding, Cloud and Cloud Pro accept up to three PNG/JPEG images;
ChatGPT accepts one. Native local accepts one PNG/JPEG on macOS 27 with a
vision-capable model, including streaming; tools and guided output cannot be
combined with images. `auto` and Shortcuts On-Device reject
image input because the tested On-Device Shortcut ignored the pixels.
[Images and styles](docs/image-generation.md) · [References and follow-ups](docs/image-references.md).

### Batch processing

```bash
hollis batch plan --input-dir ./inbox --prompt-file instructions.txt \
  --model cloud --output-dir ./results --job ./job.json
hollis batch run --job ./job.json --max-calls 12
hollis batch resume --job ./job.json --max-calls 12
```

Planning makes no model calls. Runs have an explicit call budget and verify
saved results before skipping them. After each completion, Cloud waits at least
30 seconds, Cloud Pro 45 seconds and ChatGPT 15 seconds; On-Device and native
local run without a pause. Apple rate-limits cloud requests per Mac without
publishing the limit; we observed a block of about 17–24 hours after roughly
115 Cloud requests in 34 minutes. Pacing reduces risk but cannot guarantee
access. Text supports all five
explicit model choices; images use Cloud, Cloud Pro or ChatGPT. `auto` is not a
batch choice. A failure stops the run. [Batch guide](docs/batch.md).

## Use Hollis in Claude Code and Codex

[Plugin 0.4.0](plugins/hollis/README.md) targets runtime **0.7.0**, its matched
native helper and five Shortcut bridges. The checked-in lock still pins runtime
**0.4.0**; setup and packaging remain blocked until the published 0.7.0 assets
are verified and pinned. Ask Apple to assess a supplied plan,
compare documents or create an image, then keep working in the same
conversation. Guided setup preserves your existing configuration and
conversations.

Plugin 0.2.0 was withdrawn. Plugin 0.1.0 bundled runtime 0.3.3 without native
local.
[Installation and everyday requests](plugins/hollis/README.md#install).

## OpenAI-compatible API

Chat Completions and Responses run on localhost. First create private authentication
files and start the server:

```bash
umask 077
openssl rand -base64 48 > hollis.token
{ printf 'Authorization: Bearer '; cat hollis.token; } > hollis.headers
hollis serve --token-file hollis.token
```

In another terminal in the same directory:

```bash
curl -sS http://127.0.0.1:1978/v1/chat/completions \
  -H @hollis.headers -H 'Content-Type: application/json' \
  -d '{"model":"cloud","stream":false,"messages":[{"role":"user","content":"Explain closures briefly."}]}'
```

- Through Shortcuts, answers are complete and token usage is unavailable; `stream: true` returns 400.
- Native `local` supports real SSE streaming on both endpoints and measured token usage on complete responses. Chat Completions can request a final usage chunk with `stream_options`; unavailable counts stay null.
- Guided JSON output on `local` is available through `--schema` on the CLI and through `response_format` / `text.format` on both HTTP endpoints. The object is returned as a JSON string.
- A paired native helper that speaks another protocol or version is 502 `native_protocol` on `local` requests, guided or plain, matching CLI exit 5 and naming the matching bundle; a missing or unavailable helper stays 400 `model_unavailable`.
- CLI `--json` and `--agent` output never stream; explicit streaming with either is rejected.
- Caller-provided tools on `local` only (`tools`, `role:"tool"` / `function_call_output` results, `finish_reason:"tool_calls"`); Hollis never runs a tool. `system` and `instructions` are advisory prompt content.

[Client settings, authentication and limits](docs/api.md) · [Native local](docs/native-local.md).

<a id="cloud-availability-and-limits"></a>

## Limits and caveats

### Cloud availability

Apple controls Cloud and Cloud Pro availability and usage limits. During testing,
we encountered an explicit Cloud Pro usage limit and hours-long interruptions to
cloud access before it recovered. Some failures returned generic Shortcuts errors;
not every interruption was a confirmed quota hit.

Hollis cannot show your remaining Shortcuts allowance or predict when access will
recover. Pacing reduces bursts; it does not guarantee availability or prevent a
usage limit. Pause after a limit rather than repeatedly retrying. No fixed reset
interval, shared quota, or safe calls-per-minute allowance has been established.

Explicit `cloud` and `cloud-pro` requests never silently switch models. Default
`auto` may try Shortcuts On-Device once only after a confirmed missing bridge.
Provider limits stop without retry or fallback. It never falls back to native local.

Hollis requires no model API key and adds no inference charge. Apple’s limits and
any charges from your agent host or linked service still apply.

### Image generation

On the tested, already approved account, generation needed no per-image click or
visible Playground editor. Any Style does not guarantee photographs; ChatGPT
image generation is unavailable through the tested Shortcut. Reference bytes are
sent, but reliable use of their pixels, including subject preservation, remains
unproven.

### Tools on local

Local tools use native SDK callbacks and return control to Go for validation,
approval and sequential MCP execution. Caller tools remain external.
Required and named choices are supported. Call-shaped prose never executes,
and invalid native arguments stop without repair. The default limit is eight
model decisions. [Native tools and sampling](docs/native-local.md#tools) and
[MCP servers](docs/mcp.md).

### Documents

PDF extraction returns the document's text exactly, but the answer still comes
from the selected model. The on-device model can misread figures in long
pages; check numbers that matter.

### Versions

Guided JSON output, PDF extraction, single-image local input, tool calling,
MCP and `doctor --model` arrived in 0.7.0. Runtime 0.4.0 and plugin 0.2.0 do
not have them. The CLI and native helper must come from the same release. See
[native-local version requirements](docs/native-local.md#guided-output).

## Verification and trust

The binaries are not Developer ID signed or notarized. The installer checks
release checksums; with `gh`, it also verifies GitHub build provenance. Without
`gh`, it verifies the release-pinned SHA-256, not provenance. For the
smallest trust chain, build from source
([other install routes](docs/compatibility.md#other-install-routes)). To run the
installer’s checks yourself:

<a id="verified-install-step-by-step"></a>
<!-- updated at release tagging -->

<details>
<summary>Verify manually</summary>

These commands verify and install a matched release bundle. Set `HOLLIS_VERSION`
and `HOLLIS_SOURCE_DIGEST` from that release's [release notes](docs/releases/README.md)
(the version and full source commit). Artifact names and source pins are fixed
at publication; do not substitute a candidate build. The last command selects
the bundled image bridge; skip it if you
already use a custom image bridge. Do not install a bundle that fails either
the checksum or the attestation check.

```bash
(
set -euo pipefail
umask 077
: "${HOLLIS_VERSION:?Set the version from the pinned release notes}"
: "${HOLLIS_SOURCE_DIGEST:?Set the full source commit from the pinned release notes}"
HOLLIS_BUNDLE="hollis-$HOLLIS_VERSION-darwin-arm64.zip"
cd "$(mktemp -d)"
gh release download "v$HOLLIS_VERSION" --repo kamenxrider/hollis \
  --pattern "$HOLLIS_BUNDLE" --pattern SHA256SUMS
awk -v asset="$HOLLIS_BUNDLE" '$2 == asset { n++; print } END { if (n != 1) exit 1 }' \
  SHA256SUMS > SELECTED_SHA256SUMS
shasum -a 256 -c SELECTED_SHA256SUMS
gh attestation verify "$HOLLIS_BUNDLE" --repo kamenxrider/hollis \
  --signer-workflow kamenxrider/hollis/.github/workflows/release.yml \
  --source-ref "refs/tags/v$HOLLIS_VERSION" \
  --source-digest "$HOLLIS_SOURCE_DIGEST" \
  --deny-self-hosted-runners
unzip -q "$HOLLIS_BUNDLE" -d runtime
mkdir -p "$HOME/.local/bin"
install -m 755 runtime/hollis runtime/hollis-native "$HOME/.local/bin/"
unzip -q runtime/hollis-bridges.zip -d bridges
mkdir signed
for f in bridges/*.shortcut; do
  shortcuts sign --mode anyone --input "$f" --output "signed/${f##*/}"
  open "signed/${f##*/}"
done
)
export PATH="$HOME/.local/bin:$PATH"
hollis config set image-bridge "Hollis Image - Reference Input v2"
```

</details>

`doctor` and `setup` check setup and availability, never inference. Verified
downloads and existing-account tests do not establish fresh-account onboarding.
Dated live checks and what they qualify are in [Evidence](EVIDENCE.md), which
also compares Hollis with Apple’s own [`fm` tool](EVIDENCE.md#why-not-fm).

<a id="your-data"></a>

## Your data and uninstalling

Configuration (`config.json`), conversations and run history (`hollis.db`) live in
`~/Library/Application Support/hollis`, unless `HOLLIS_STATE_DIR` overrides it.
The plugin keeps its managed runtime under that directory’s `plugin` folder.
Prompts use private temporary files, removed after requests; abrupt termination can
leave files behind. [Transport and cleanup](docs/shortcuts.md#how-it-works).

For the CLI installer above, remove the executables:

```bash
rm -i "$HOME/.local/bin/hollis" "$HOME/.local/bin/hollis-native"
```

For an earlier `/usr/local/bin` install, remove those copies instead. Remove the
plugin through its host’s plugin manager. Delete the five Hollis shortcuts in
Shortcuts if no remaining installation uses them. Your chats and settings remain;
to erase them too, delete the Hollis Application Support folder in Finder after
backing up anything you want to keep. [Plugin state and removal](plugins/hollis/docs/setup.md).

## Reference

<!-- Preserved anchors: external deep links from release notes and EVIDENCE.md. Do not remove. -->
<a id="scripts-and-agents"></a>
<a id="quick-reference"></a>
<a id="local-openai-shaped-api"></a>
<a id="inline-image-input"></a>
<a id="clients-listing-is-not-the-same-as-working"></a>
<a id="generate-an-image-through-the-api"></a>
<a id="how-it-works"></a>
<a id="bridge-discovery"></a>
<a id="shortcuts-quirks-hollis-handles"></a>
<a id="the-chatgpt-quirk"></a>
<a id="model-notes"></a>
<a id="doctor"></a>
<a id="compatibility"></a>
<a id="other-install-routes"></a>
<a id="what-you-need"></a>
<a id="if-something-breaks"></a>
<a id="testing"></a>
<a id="why-not-fm"></a>


| Guide | What you will find |
| --- | --- |
| [CLI and local data](docs/cli.md) | Inputs, chats, agent JSON, storage and exit codes |
| [Selected-route setup](docs/selected-route-setup.md) | `hollis setup` and `doctor --model <route>` plans, statuses and exit codes |
| [API](docs/api.md) | Chat Completions, Responses, and why a listed model can still fail |
| [Shortcuts transport](docs/shortcuts.md) | Bridge discovery and measured behavior |
| [Compatibility](docs/compatibility.md) | Tested builds, doctor, verified and alternative installation |
| [Testing](docs/testing.md) | Offline checks and live-test boundaries |
| [Contributing](CONTRIBUTING.md) | Building, offline checks and the privacy gate |
| [AI pull-request review](scripts/ai-review/README.md) | Bounded diff review, provider configuration and the manual smoke run |

<a id="whats-in-031"></a>
<a id="included-from-030"></a>

[Release notes](docs/releases/README.md) · [Evidence](EVIDENCE.md) · [Apache-2.0 license](LICENSE)
