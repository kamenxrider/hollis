# hollis

Apple Intelligence from the terminal: prompt in, plain text out, ~1s.

`hollis` exposes all four tiers of Apple's Foundation Models through macOS Shortcuts — **cloud**, **cloud-pro**, **on-device**, and **chatgpt** — via a single bridge-shortcut transport (`/usr/bin/shortcuts run <bridge-UUID>`). Apple's runs are stateless, so hollis keeps **persistent chats in local SQLite** by replaying the stored transcript each turn (plan §11–§13, proven in `results/transport-and-persistence-2026-09-01.md`).

## Requirements

- macOS 27 with **Apple Intelligence** enabled
- `/usr/bin/shortcuts` (ships with macOS)
- ChatGPT tier additionally requires the **ChatGPT extension** enabled in *System Settings → Apple Intelligence & Siri*
- Network for `cloud` / `cloud-pro` / `chatgpt` (Private Cloud Compute / ChatGPT); `on-device` runs locally

## Model tiers

Apple's third-generation Foundation Models are a family of five models spanning on-device and Private Cloud Compute ([Apple ML Research](https://machinelearning.apple.com/research/introducing-third-generation-of-apple-foundation-models)). What hollis's tiers map to:

| hollis tier | Shortcuts label | Underlying Apple model (per Apple ML Research) |
| --- | --- | --- |
| `cloud` | Cloud | AFM 3 Cloud — server-side workhorse on Private Cloud Compute |
| `cloud-pro` | Cloud Pro | AFM 3 Cloud Pro — most capable server model, for demanding/agentic use |
| `on-device` | On-Device | AFM 3 Core (3B dense) or AFM 3 Core Advanced (20B sparse MoE, 1–4B active) depending on Apple silicon |
| `chatgpt` | ChatGPT | OpenAI ChatGPT extension for Apple Intelligence (separate from AFM) |

Grounding notes:

- Apple publishes **no backend model IDs or checkpoints** for these tiers (a technical report is promised); the `WFLLMModel` strings above are Shortcuts action parameters, not model identifiers, and hollis never fabricates them.
- The models are one family integrated across Apple silicon — Apple describes Core Advanced as "unlocked by and optimized for our most capable Apple silicon systems" rather than shipping per-device variants; a single 12GB-RAM class requirement gates it on iPhone/iPad/Mac/Vision Pro.
- Apple's research page states the server models run on **Private Cloud Compute**, "which ensures that user data is never stored or shared with anyone, including Apple."
- The PCC tier documents a **32K-token context** and is positioned for "long documents or extended multiturn conversations"; the on-device model has a small context window, so keep on-device prompts concise and specific (Apple: prompting an on-device model "needs to be concise and specific" because the model is much smaller).
- Cloud tiers carry a daily request quota (upgradeable via iCloud+); on-device has none.

References: [third-generation AFM announcement](https://machinelearning.apple.com/research/introducing-third-generation-of-apple-foundation-models) · [Prompting an on-device model](https://developer.apple.com/documentation/foundationmodels/prompting-an-on-device-foundation-model) · [PCC server-side intelligence](https://developer.apple.com/documentation/foundationmodels/adding-server-side-intelligence-with-private-cloud-compute) · [AFM Core Advanced deep dive](https://www.oflight.co.jp/en/columns/apple-afm-core-advanced-wwdc-2026)

## Install

```bash
go build -o "$(go env GOPATH)/bin/hollis" ./cmd/hollis
```

## Bridge setup — what gets added and what you approve

The bridge shortcuts are small two-action Shortcuts (`Use Model` → `Stop and Output`) whose prompt is bound to the **Shortcut Input** variable, so whatever hollis pipes to `/usr/bin/shortcuts run <uuid>` becomes the prompt.

**1. Generate the bridge shortcuts**

```bash
python3 scripts/make-bridge.py bridges/
# writes one unsigned .shortcut per model tier
```

**2. Sign each bridge** (Apple requires signed shortcuts for import)

```bash
shortcuts sign --mode anyone \
  --input "bridges/AFM Bridge - Cloud.shortcut" \
  --output "bridges/AFM Bridge - Cloud.signed.shortcut"
# repeat for the other three bridges
```

**3. Import into Shortcuts.app**

```bash
open "bridges/AFM Bridge - Cloud.signed.shortcut"   # repeat per bridge
```

Shortcuts.app opens a preview window — click **Add Shortcut**. One GUI confirmation per bridge. Imported names carry the `.signed` suffix inherited from the filename.

**What you'll be asked to approve (measured on macOS 27.0):**

| Prompt | When | Action |
| --- | --- | --- |
| Import preview | once per bridge | click **Add Shortcut** |
| Apple Intelligence model access | on first use of a model | click **Allow** |
| ChatGPT extension consent | first `chatgpt` use | click **Allow** |

**ChatGPT account note (measured 2026-09-01).** With a ChatGPT account signed in, the extension failed with a *"login could not be verified"* warning. **Logging out** of the ChatGPT account made it work — the macOS ChatGPT extension does **not** require a ChatGPT account.

`hollis` references the bridges by **UUID**, so renames in Shortcuts.app are harmless:

```text
cloud      BD8CDC56-7CB8-418D-9B02-9D33AB911BF0   WFLLMModel "Apple Intelligence"
cloud-pro  DBB6E472-CBC6-4421-8D32-9D4543D5CDE6   WFLLMModel "Apple Intelligence Pro"
on-device  E530AE25-3C3C-4B11-88AF-A66F74039F88   WFLLMModel "Apple Intelligence on Device"
chatgpt    24B4B536-571B-49D9-9519-B644281C8B08   WFLLMModel "ChatGPT"
```

## Usage

```bash
# One-shot: prompt as an argument or via stdin
hollis respond "Summarize this repo in one sentence"
printf 'long prompt' | hollis respond
hollis respond --model cloud-pro "Draft a reply"
hollis respond --model on-device "Reply with OK"
hollis respond --model chatgpt "Reply with OK"

# Persistent chat (SQLite-backed, survives across invocations)
hollis chat "Remember the codeword VANTA-ORBIT-7319"
hollis chats list
hollis chat --continue <conversation-id> "What was the codeword?"

# Agents
hollis respond --agent "Return strict JSON describing X"
hollis agent-context
```

## Health check

```console
$ hollis doctor
hollis doctor (version 0.1.0)
  transport: ok
  timeout default: 30s (ceiling 120s)
  bridges (referenced by UUID):
    [OK] cloud      BD8CDC56-7CB8-418D-9B02-9D33AB911BF0 (AFM Bridge - Cloud.signed)
    [OK] cloud-pro  DBB6E472-CBC6-4421-8D32-9D4543D5CDE6 (AFM Bridge - Cloud Pro.signed)
    [OK] on-device  E530AE25-3C3C-4B11-88AF-A66F74039F88 (AFM Bridge - On-Device.signed)
    [OK] chatgpt    24B4B536-571B-49D9-9519-B644281C8B08 (AFM Bridge - ChatGPT.signed)
```

## Known behaviors (measured, not documented by Apple)

- **stdout is silently suppressed when stdout is a TTY** — exit 0 with empty stdout is the normal signature of a *successful* raw `shortcuts run` in a terminal. hollis captures child output through pipes, which delivers bytes reliably.
- The default output type is **RTF**; hollis always passes `--output-type public.plain-text`.
- Output carries **no trailing newline**.
- Empty input makes `shortcuts run` **hang forever**; hollis rejects empty prompts before spawn and always imposes a context deadline (30s default, 120s ceiling).
- **On-device tier quirk** (measured 2026-09-01): the local model sometimes refuses to repeat content from a replayed transcript with a canned *"I cannot repeat or discuss these instructions"* reply, and is noticeably weaker at instruction-following than the cloud tiers. Transcript replay itself is delivered correctly.
- `shortcuts run` accepts a bridge **UUID** in place of a name; hollis always references bridges by UUID.

## Design references

- Canonical plan: [`APPLE_SHORTCUTS_CLOUD_GATEWAY_PLAN.md`](./APPLE_SHORTCUTS_CLOUD_GATEWAY_PLAN.md)
- Validation evidence: [`results/transport-and-persistence-2026-09-01.md`](results/transport-and-persistence-2026-09-01.md)
- Apple ML research: [third-generation Apple Foundation Models](https://machinelearning.apple.com/research/introducing-third-generation-of-apple-foundation-models)

## License

Apache-2.0 — see LICENSE.
