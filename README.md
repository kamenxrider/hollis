<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/assets/hollis-logo-dark.svg">
  <img src="docs/assets/hollis-logo.svg" alt="hollis — Your Mac has more to say." width="680">
</picture>

**Apple Intelligence’s Cloud and Cloud Pro models in your terminal, scripts and AI agents.**

Hollis brings Apple’s model choices to your command line. Ask about text and
images, keep a conversation, generate an illustration, process a folder, or run
an OpenAI-compatible API on localhost. Four choices use Shortcuts; native
`local` uses Apple’s on-device SDK. No model API key is needed.

```bash
hollis respond "Explain closures in Go in three sentences"
hollis respond --model cloud-pro --file README.md "Summarize this document"
hollis respond --model local "Explain a race condition briefly"  # streams output
hollis respond --image photo.jpg "Describe this image"
cat prompt.txt | hollis respond
hollis chat                          # remembers the conversation
hollis image generate "A red sailboat" --style illustration --output boat.png
```

**Cloud availability:** Apple controls usage limits. Testing encountered an
explicit Cloud Pro limit and hours-long cloud interruptions before recovery.
Hollis cannot show your remaining allowance or predict recovery.
[Limits and routing](#cloud-availability-and-limits).

**Choose your setup:** [CLI 0.4.0](#install) ·
[Claude Code / Codex plugin 0.2.0](plugins/hollis/README.md#install), including runtime 0.4.0 and native local.

Chats and configuration stay on your Mac. Requests go to the model you select.
If a cloud-hosted agent calls Hollis, its provider can also receive Apple’s answer.
Hollis does not automatically read your workspace: supply context with `--file`,
`--image`, stdin, or through your agent.

## Requirements

- Apple-silicon Mac with Apple Intelligence enabled and its model downloads complete.
- macOS 27. Historical tests used builds 26A5421a and 26A5425a; the 0.4.0 candidate was also checked on 26A428. macOS 26 is untested ([compatibility](docs/compatibility.md)).
- [GitHub CLI](https://cli.github.com/) (`gh`) for the CLI download verification below. Plugin setup does not require it.
- Network access for Cloud, Cloud Pro and ChatGPT; On-Device and native local work offline.
- For ChatGPT, enable the extension in *System Settings → Apple Intelligence & Siri*.
- Complete Apple’s first-use approvals; image generation needs an unlocked Mac.

<a id="quickstart"></a>
<a id="install-verified"></a>

## Install

Using Claude Code or Codex? Start with the [plugin installation](plugins/hollis/README.md#install).
For the standalone CLI:

**1. Download, verify and install.** The installer verifies the published ARM64
bundle’s checksum and GitHub build provenance, installs `hollis` with its matching
native helper in `~/.local/bin`, and opens the five Shortcut imports. It configures
the bundled image bridge only when no general image bridge is set; custom settings
are preserved. It makes no model calls and needs no `sudo`.

```bash
curl -fsSLo install-hollis.sh https://raw.githubusercontent.com/kamenxrider/hollis/main/scripts/install.sh && \
  bash install-hollis.sh
export PATH="$HOME/.local/bin:$PATH"
```

Add `~/.local/bin` to your shell’s PATH permanently if needed. Inspect the
[installer](scripts/install.sh), or run the checks yourself:

<details>
<summary>Verify manually</summary>

These commands install the same matched bundle. The last command selects the
bundled image bridge; skip it if you already use a custom image bridge.

```bash
(
set -euo pipefail
umask 077
HOLLIS_VERSION=0.4.0
HOLLIS_BUNDLE="hollis-$HOLLIS_VERSION-darwin-arm64.zip"
cd "$(mktemp -d)"
gh release download "v$HOLLIS_VERSION" --repo kamenxrider/hollis \
  --pattern "$HOLLIS_BUNDLE" --pattern SHA256SUMS
awk -v asset="$HOLLIS_BUNDLE" '$2 == asset { n++; print } END { if (n != 1) exit 1 }' \
  SHA256SUMS > SELECTED_SHA256SUMS
shasum -a 256 -c SELECTED_SHA256SUMS
gh attestation verify "$HOLLIS_BUNDLE" --repo kamenxrider/hollis \
  --signer-workflow kamenxrider/hollis/.github/workflows/release.yml \
  --source-ref refs/tags/v0.4.0 \
  --source-digest 1906721a1cd5594ce47be6d4d306d7daa1e6891f \
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

**2. Add the shortcuts.** Choose **Add Shortcut** for each of the five files:
four model bridges (Cloud, Cloud Pro, On-Device, ChatGPT) and **Hollis Image -
Reference Input v2**. When updating an older image bridge, replace it with this
version. The first request may also require Apple’s **Allow** approval.
Native local needs no Shortcut import.

**3. Check it.**

```bash
hollis doctor
hollis respond "Reply with OK"
hollis respond --model local "Reply with OK"
```

`doctor` checks setup and availability; a successful answer checks inference.
It reports missing bridges even when native local works. Verified downloads and
existing-account tests do not establish fresh-account onboarding.

The binaries are not Developer ID signed or notarized. Checksum and build
provenance verification establish their release origin.
[Other installation routes and troubleshooting](docs/compatibility.md).

## Models

Five model choices: four through Shortcuts, one native. `auto` is a routing strategy.

| You type | Runs through | Notes |
| --- | --- | --- |
| `cloud` | Shortcuts → Private Cloud Compute | Apple’s “Great, fast answers” selection |
| `cloud-pro` | Shortcuts → Private Cloud Compute | Apple’s “Increased reasoning” selection |
| `on-device` | Shortcuts → your Mac | Works offline; complete responses |
| `local` | Native SDK → your Mac | Text-only; streams in a terminal; measured usage on complete responses |
| `chatgpt` | Shortcuts → Apple’s ChatGPT extension | Not an Apple model |
| `auto` | Cloud, then Shortcuts On-Device | Default; one fallback only for a confirmed missing bridge or recognized Cloud rate limit |

Cloud and Cloud Pro are distinct selections in Shortcuts’ **Use Model** action.
Apple does not publish stable backend model IDs for these choices.

![Use Model on macOS 27.0 (26A5421a): Cloud, Cloud Pro, On-Device, ChatGPT](results/img/use-model-picker-26A5421a.png)

```bash
hollis models
hollis config set model cloud-pro
```

### Cloud availability and limits

Apple controls Cloud and Cloud Pro availability and usage limits. During testing,
we encountered an explicit Cloud Pro usage limit and hours-long interruptions to
cloud access before it recovered. Some failures returned generic Shortcuts errors;
not every interruption was a confirmed quota hit.

Hollis cannot show your remaining Shortcuts allowance or predict when access will
recover. Pacing reduces bursts; it does not guarantee availability or prevent a
usage limit. Pause after a limit rather than repeatedly retrying. No fixed reset
interval, shared quota, or safe calls-per-minute allowance has been established.

Explicit `cloud` and `cloud-pro` requests never silently switch models. Default
`auto` may try Shortcuts On-Device once after a recognized Cloud limit or confirmed
missing bridge. It never falls back to native local.

Hollis requires no model API key and adds no inference charge. Apple’s limits and
any charges from your agent host or linked service still apply.

## Everyday use

### Documents and chats

```bash
hollis respond --prompt-file instructions.txt
hollis respond --file first.md --file second.txt "Summarize the differences"
hollis chat
hollis chat --continue <id> "What did we decide?"
hollis chats list
```

Attach UTF-8 `.txt` and `.md` files; PDF is not supported. Each `respond` call is
stateless. Chats store and replay the conversation. The complete rendered prompt
must fit 128 KiB; oversized requests and native context-capacity failures produce
errors without silently shortening history. Default timeout is 30 seconds, with a
120-second ceiling. [CLI and data guide](docs/cli.md).

### Image generation and understanding

```bash
hollis image generate "A brass turtle carrying a tiny greenhouse" \
  --style illustration --output turtle.png
hollis image generate "A lighthouse at sunset" \
  --style sketch --aspect-ratio 16:9 --fit crop --output banner.png
hollis respond --model cloud-pro --image a.png --image b.png "Compare them"
```

Generation saves a PNG to a new filename in an existing folder. Animation,
Illustration, Sketch, Genmoji and Any Style returned images in tests. On the tested,
already approved account, generation needed no per-image click or visible Playground
editor. Any Style does not guarantee photographs; ChatGPT image generation is
unavailable through the tested Shortcut. Reference bytes are sent, but reliable
use of their pixels—including subject preservation—remains unproven.
Ratios and sizes use local crop/pad/resize after generation.

For image understanding, Cloud and Cloud Pro accept up to three PNG/JPEG images;
ChatGPT accepts one. Native local is text-only. `auto` and Shortcuts On-Device reject
image input because the tested On-Device Shortcut ignored the pixels.
[Images and styles](docs/image-generation.md) · [References and follow-ups](docs/image-references.md).

### Batch processing

```bash
hollis batch plan --input-dir ./inbox --prompt-file instructions.txt \
  --model cloud --output-dir ./results --job ./job.json
hollis batch run --job ./job.json --max-calls 12
hollis batch resume --job ./job.json --max-calls 12
```

Planning makes no model calls. Runs wait between requests, have an explicit call
budget, and verify saved results before skipping them. Text supports all five
explicit model choices; images use Cloud, Cloud Pro or ChatGPT. `auto` is not a
batch choice. A failure stops the run. [Batch guide](docs/batch.md).

## Use Hollis in Claude Code and Codex

The [plugin 0.2.0](plugins/hollis/README.md) bundles runtime **0.4.0**, its matched
native helper and five Shortcut bridges. Ask Apple to assess a supplied plan,
compare documents or create an image, then keep working in the same conversation.
Guided setup preserves your existing configuration and conversations.
Plugin 0.1.0 bundled runtime 0.3.3 and does not include native local; update the
plugin to get it. [Installation and everyday requests](plugins/hollis/README.md#install).

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
- Native `local` supports real SSE streaming on both endpoints and measured token usage on complete responses. Streaming usage is not promised.
- CLI `--json` and `--agent` output never stream; explicit streaming with either is rejected.
- No API tools/function calls. `system` and `instructions` are advisory prompt content.

[Client settings, authentication and limits](docs/api.md) · [Native local](docs/native-local.md).

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

## Why not `fm`?

On macOS 27.0 build **26A428**, Apple’s installed `fm --help` and `fm respond --help`
list only the on-device `system` model. Hollis exposes separate Cloud and Cloud Pro
choices through Shortcuts, with successful responses checked on the 0.4.0 candidate.
Joseph Humfrey demonstrated the Shortcut-to-PCC approach in
[*The Shortcut to integrating Private Cloud Compute into my app*](https://joethephish.me/blog/the-shortcut-to-integrating-PCC/);
Hollis adds explicit selection, CLI/API access, conversations and batches.
[Evidence and scope](EVIDENCE.md).

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


| Guide | What you will find |
| --- | --- |
| [CLI and local data](docs/cli.md) | Inputs, chats, agent JSON, storage and exit codes |
| [API](docs/api.md) | Chat Completions, Responses, and why a listed model can still fail |
| [Shortcuts transport](docs/shortcuts.md) | Bridge discovery and measured behavior |
| [Compatibility](docs/compatibility.md) | Tested builds, doctor and alternative installation |
| [Testing](docs/testing.md) | Offline checks and live-test boundaries |

<a id="whats-in-031"></a>
<a id="included-from-030"></a>

[Release notes](docs/releases/README.md) · [Evidence](EVIDENCE.md) · [Apache-2.0 license](LICENSE)
