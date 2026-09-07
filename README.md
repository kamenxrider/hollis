# hollis

**Apple Cloud and Cloud Pro in your terminal, scripts and agent conversations.**

Hollis makes the Apple Intelligence access on your Mac available to the tools
you already use. Choose Cloud, Cloud Pro, On-Device or ChatGPT; work with text
and images; keep a conversation; or call it from a local API.

On the tested macOS 27 builds, Shortcuts exposes all four choices:

![The Use Model action on macOS 27.0 (26A5421a): Cloud, Cloud Pro, On-Device, ChatGPT](results/img/use-model-picker-26A5421a.png)

Cloud and Cloud Pro are distinct selections. Hollis reaches them through Apple’s
Shortcuts command-line interface, using small, inspectable bridges you install.
[How this differs from Apple’s `fm` CLI](#why-not-fm).

```bash
hollis respond "Summarize this repo in one sentence"
hollis respond --model cloud-pro "Analyze this bug"
hollis respond --image photo.jpg "Describe this image"
printf 'long prompt' | hollis respond
hollis chat                       # interactive, remembers the conversation
hollis serve --token-file /private/path/hollis.token  # local OpenAI-shaped API
```

Chats and configuration stay on your Mac. Model requests go to the provider you
select through Shortcuts: Cloud and Cloud Pro use Private Cloud Compute; ChatGPT
uses Apple’s ChatGPT extension. An agent that calls Hollis can see the returned
answer. Apple’s processing privacy does not make that agent’s own service local.

Measured on **macOS 27.0 (26A5421a and 26A5425a)**. macOS 26 is untested — see [Compatibility](docs/compatibility.md).

<a id="quickstart"></a>

## Install (verified)

A Mac with **Apple Intelligence** enabled, macOS 27 for the measured setup, and `/usr/bin/shortcuts` (included with macOS). Cloud, Cloud Pro and ChatGPT need network access; On-Device works offline. For the ChatGPT bridge, enable the extension in *System Settings → Apple Intelligence & Siri*.

This verified quickstart installs the CLI and bridges. For guided setup inside Claude or Codex, use the [plugin guide](plugins/hollis/README.md#install).

**1. Download and verify the CLI and all five bridges.** This secure path
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

mkdir signed-bridges
for f in bridges/*.shortcut; do
  HOLLIS_SIGNED="signed-bridges/${f##*/}"
  shortcuts sign --mode anyone --input "$f" --output "$HOLLIS_SIGNED"
  open "$HOLLIS_SIGNED"
done
)
```

**2. Add the five shortcuts.** The bundle contains four model bridges and
**Hollis Image - Reference Input v2** for image generation. The commands above
sign them on your Mac and open them in Shortcuts. Choose **Add Shortcut** for
each. If updating an earlier image bridge, replace that copy with this one so
the corrected reference connection takes effect. This step is **blocking for model calls**:
`doctor` still runs without the bridges, but reports `MISSING` and exits 3.

The first time a bridge runs, macOS may also ask you to **Allow** model access.

Release binaries and Shortcut files are **unsigned by a Developer ID and not notarized**.
Hollis does not claim Gatekeeper approval.

### First answer and image

**3. Check it.**

```bash
hollis config set image-bridge "Hollis Image - Reference Input v2"
hollis doctor
hollis respond "Reply with OK"
```

`doctor` checks the four model bridges. To test the newly configured image
bridge, use a new filename in an existing directory:

```bash
hollis image generate "A small red sailboat on a calm blue lake" \
  --style illustration --output sailboat.png
```

Once the shortcuts and first-use permissions are in place, Hollis returns the
text or image directly. Image generation needs an unlocked Mac; it does not use
the visible Image Playground editor. No source checkout or extra image download
is needed.

[Other install routes and troubleshooting](docs/compatibility.md).

## Models

| You type | Shortcuts model | Notes |
| --- | --- | --- |
| `auto` | Cloud → On-Device | Default; one local fallback only for a confirmed missing bridge or recognized Cloud rate limit |
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

### Text documents

```bash
hollis respond --prompt-file instructions.txt
hollis respond "Summarize the differences" --file first.md --file second.txt
```

Attach local `.txt` and `.md` documents in order. PDF is not supported. The complete request must fit the 128 KiB prompt limit; Hollis rejects oversized input without truncation. [Input rules and file protections](docs/cli.md#text-documents).

### Image generation

Create and save a PNG without leaving your terminal or agent conversation.
The standard release bundle includes the image Shortcut. Complete its setup first:
[image setup](docs/image-generation.md#setup).

```bash
hollis image generate "A brass turtle carrying a tiny greenhouse" \
  --style illustration --output turtle.png
hollis image generate "A lighthouse on a cliff at sunset" \
  --style sketch --aspect-ratio 16:9 --fit crop --output banner.png
```

The tested Shortcut returns images without a per-image click on an already
set-up, unlocked Mac. Hollis does not launch an Image Playground window or
click its interface. Apple may ask for permissions during setup.

Animation, Illustration, Sketch, Genmoji and Any Style returned images in our
tests. **Any Style does not guarantee photographs. ChatGPT image generation
is blocked through Shortcuts on the tested macOS build.** The separate ChatGPT
text/image-understanding bridge works.

Use `/image` in a Hollis chat, or request generation through the API. A follow-up
can attach the previous image and ask for a new scene. Reference bytes are sent,
but reliable use of those pixels remains unproven, including subject preservation.
Ratios and sizes use local crop/pad/resize after generation, rather than native
model controls.

[Generation and styles](docs/image-generation.md) ·
[Image references and conversation examples](docs/image-references.md).

### Images

`respond` accepts PNG and JPEG files through the existing bridges:

```bash
hollis respond --image photo.jpg "What is this?"
hollis respond --model cloud-pro --image a.png --image b.png "Compare them"
```

An image request with no selected or configured model defaults directly to Cloud. Cloud and Cloud Pro accept repeated `--image`; ChatGPT accepts one image. `auto` and On-Device are rejected for images because the tested On-Device Shortcut ignored the pixels, making automatic fallback unsafe.

[File limits and safe image input](docs/cli.md#images) · [API images](docs/api.md#inline-image-input).

## Paced folder processing

Process a folder of documents or images, save each result, and resume later:

```bash
hollis batch plan --input-dir ./inbox --prompt-file instructions.txt \
  --model cloud --output-dir ./results --job ./job.json
hollis batch run --job ./job.json --max-calls 12
hollis batch resume --job ./job.json --max-calls 12
```

Planning makes no model calls. Runs have an explicit call budget, wait between
requests, and skip completed results after verifying them. Text works on all
four tiers; images use Cloud, Cloud Pro or ChatGPT. A failure stops the run.
This processes an existing folder once; it does not watch for new files.

[File types, pacing, recovery and private storage](docs/batch.md).

## Persistent chats

Shortcuts model calls are stateless. Hollis stores conversations locally and replays the transcript each turn:

```bash
hollis chat
hollis chat --continue <id> "What did we decide?"
hollis chats list
```

[Continue, search and manage conversations](docs/cli.md#persistent-chats).

## Use Apple in your agent conversation

The new [Hollis plugin](plugins/hollis/README.md) brings these capabilities into
Claude Code and Codex, including guided setup and a bundled Mac runtime and
bridges. Ask Apple to assess your plan, compare documents or create an image,
then continue in the same conversation. It works alongside gstack without
requiring an upstream change.

Plugin **0.1.0** bundles the released, provenance-verified Hollis **0.3.1**
runtime. The plugin is a local review package pending its separate publication.
Start with [Claude/Codex installation](plugins/hollis/README.md#install),
[everyday requests](plugins/hollis/docs/usage.md) or the
[recorded gstack demonstration](plugins/hollis/examples/recorded-demo.md).
The [plugin overview](docs/plugin.md) links setup, compatibility and validation;
the [folder map](plugins/hollis/docs/package.md) explains the portable package
and native host adapters.

## What it deliberately does not do

- Shortcuts returns a complete response rather than a token stream, so `stream: true` returns **400** instead of a faked stream.
- Apple exposes no token counts through this path, so no `usage` field is invented.
- The HTTP contract has no `tools` / function calls.
- `system` and `instructions` are advisory prompt content, not hard isolation boundaries.
- Hollis deliberately has no command-line `--token`, because process arguments can be visible through `ps`.

The local API requires authentication by default. [Authentication, limits and client settings](docs/api.md).

## Reference

<a id="scripts-and-agents"></a>
<a id="quick-reference"></a>
<a id="your-data"></a>
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
| [CLI and local data](docs/cli.md) | File input, chats, agent JSON, exit codes, storage and command reference |
| [Local API](docs/api.md) | Chat Completions, Responses, images, authentication and “listing is not the same as working” |
| [Shortcuts transport](docs/shortcuts.md) | Bridge discovery, measured quirks and model-family notes |
| [Compatibility and troubleshooting](docs/compatibility.md) | macOS support, doctor output and alternate installation |
| [Testing](docs/testing.md) | Offline checks, live-test boundaries and the harness index |

## Why not `fm`?

Apple ships its own Foundation Models CLI, `fm`, and hollis neither patches nor replaces it. Two things make Shortcuts the more capable path today.

**Granularity.** Even when `fm` supported Private Cloud Compute, its selector was a single `pcc` target — one generic cloud model, with no way to ask for a specific tier. Shortcuts exposes Cloud and Cloud Pro as separate choices, so hollis can offer a distinction the CLI never had.

**Availability.** On macOS 27.0 builds `26A5421a` and `26A5425a`, `fm` lists only `system`, and `fm available --model pcc` is rejected at argument validation — including from Terminal.app, so this is not a Warp/PTY quirk. Whether that is deliberate or a beta regression, Apple has not said.

There is also a reason not to link the framework directly. Apple gates third-party PCC access behind an entitlement, App Store Small Business Program enrollment, and a two-million-download ceiling; a non-entitled binary calling `PrivateCloudComputeLanguageModel` fails with `ModelManagerError 1046`. That entitlement gates the **developer framework, not the user-facing automation surface** — Shortcuts is a shipped consumer feature, `shortcuts run` is a documented Apple CLI, and the bridges are shortcuts you could build by hand in a minute. Hollis automates a supported surface rather than working around a restriction.

That surface is one Apple can change in any build, exactly as it changed `fm` in this one, which is why every claim here names the build it was measured on.

Prior art: bridging to Apple Intelligence through a Shortcut was shown by **Joseph Humfrey** in [*The Shortcut to integrating Private Cloud Compute into my app*](https://joethephish.me/blog/the-shortcut-to-integrating-PCC/) (June 2025). Hollis adds explicit tier selection, persistent chats, bounded batch processing and CLI/API access. A documented web and GitHub search on 2026-09-03 found many on-device or single-`pcc` CLIs, but no other public CLI exposing the two Shortcuts choices separately. Hollis is therefore, **to our knowledge**, the first public CLI to expose both Cloud and Cloud Pro—not the first Shortcut bridge or Apple-model CLI. Full scope, counterexamples, and falsification conditions: [EVIDENCE.md](EVIDENCE.md).

## Evidence and references

* [Hollis evidence and prior-art scope](EVIDENCE.md)
* [Apple: Prompting an on-device foundation model](https://developer.apple.com/documentation/foundationmodels/prompting-an-on-device-foundation-model)
* [Apple: Adding server-side intelligence with Private Cloud Compute](https://developer.apple.com/documentation/foundationmodels/adding-server-side-intelligence-with-private-cloud-compute)

<a id="whats-in-031"></a>
<a id="included-from-030"></a>

[Release notes and upgrade instructions](docs/releases/README.md).

## License

Apache-2.0 — [LICENSE](LICENSE)
