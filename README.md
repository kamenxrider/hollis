# hollis

Apple Intelligence from the terminal: prompt in, plain text out, ~1s.

`hollis` exposes all four tiers of Apple's Foundation Models through macOS Shortcuts — **cloud**, **cloud-pro**, **on-device**, and **chatgpt** — via a single bridge-shortcut transport (`/usr/bin/shortcuts run <bridge-ref>`, resolved at runtime: config override → installed name → compiled UUID). Apple's runs are stateless, so hollis keeps **persistent chats in local SQLite** by replaying the stored transcript each turn (plan §11–§13, proven in `results/transport-and-persistence-2026-09-01.md`).

## Requirements

- **macOS 27 (measured)** with **Apple Intelligence** enabled; macOS 26 is experimental / untested — see [Compatibility](#compatibility)
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

The AFM 3 family counts **five** models — the fifth, ADM 3 Cloud (Image), powers image generation and Genmoji and is not reachable through the Use Model text action, which is why hollis exposes four tiers.

Grounding notes:

- Apple publishes **no backend model IDs or checkpoints** for these tiers (a technical report is promised); the `WFLLMModel` strings above are Shortcuts action parameters, not model identifiers, and hollis never fabricates them.
- The models are one family integrated across Apple silicon — Apple describes Core Advanced as "unlocked by and optimized for our most capable Apple silicon systems" rather than shipping per-device variants. Third-party reporting ([oflight](https://www.oflight.co.jp/en/columns/apple-afm-core-advanced-wwdc-2026)) puts eligibility at a 12GB-RAM minimum — Mac M3 or later, iPhone 17 Pro/Pro Max, iPhone Air, iPad M4+, Vision Pro M5 — with 8GB devices excluded; Apple's own page only says "most capable Apple silicon systems."
- Apple's research page states the family was "custom-built in collaboration with Google," and that the server models run on **Private Cloud Compute**, "which ensures that user data is never stored or shared with anyone, including Apple."
- The PCC tier documents a **32K-token context** and is positioned for "long documents or extended multiturn conversations"; the on-device model has a small context window, so keep on-device prompts concise and specific (Apple: prompting an on-device model "needs to be concise and specific" because the model is much smaller).
- Cloud tiers carry a daily request quota (upgradeable via iCloud+); on-device has none.
- hollis's default tier is `auto`: cloud first, one automatic on-device retry on any transport-class failure — the fallback pattern Apple documents for PCC (`PrivateCloudComputeLanguageModel` failures should "retry the request using the on-device model"). Explicit tier choices never fall back.

References: [third-generation AFM announcement](https://machinelearning.apple.com/research/introducing-third-generation-of-apple-foundation-models) · [Prompting an on-device model](https://developer.apple.com/documentation/foundationmodels/prompting-an-on-device-foundation-model) · [PCC server-side intelligence](https://developer.apple.com/documentation/foundationmodels/adding-server-side-intelligence-with-private-cloud-compute) · [AFM Core Advanced deep dive](https://www.oflight.co.jp/en/columns/apple-afm-core-advanced-wwdc-2026)

## Install

```bash
go build -o "$(go env GOPATH)/bin/hollis" ./cmd/hollis
```

Rebuilding after a pull overwrites the same path — if behavior looks stale, you are running an old binary (we hit this ourselves; re-run the build above).

## Compatibility

Measured on macOS 27 (Apple Intelligence Use Model: on-device, Cloud,
Cloud Pro, ChatGPT).

macOS 26 (Tahoe) is **untested**. Shortcuts there documented three Use
Model locations (on-device, one Private Cloud Compute cloud, ChatGPT) —
no Cloud Pro. `hollis` will refuse `cloud-pro` if that bridge is absent.
The `cloud` tier on 26 is last year's PCC model, not AFM 3.

`fm` is not used. On macOS 26 the Foundation Models API had no PCC
backend; Shortcuts is the cloud path.

If you are on 26: generate `python3 scripts/make-bridge.py --os 26`,
sign, import, run `hollis doctor`, then `hollis respond --model cloud
"Reply with OK"`. Please file what doctor printed and whether UUID or
name worked. Until then, treat 26 as experimental.

Bridges resolve at runtime on every machine: a `config.json` override
(`hollis config set bridge <tier> <name-or-uuid>`) wins, then a stable
name match from `shortcuts list`, then — only on this project's measured
27 machine — the compiled-in UUIDs. You never need to know the UUIDs.

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

Only the **unsigned** `.shortcut` files are committed. The signed outputs are your machine's imports (they embed your signing identity), so they are gitignored — every machine signs its own.

**What you'll be asked to approve (measured on macOS 27.0):**

| Prompt | When | Action |
| --- | --- | --- |
| Import preview | once per bridge | click **Add Shortcut** |
| Apple Intelligence model access | on first use of a model | click **Allow** |
| ChatGPT extension consent | first `chatgpt` use | click **Allow** |

**ChatGPT account note (measured 2026-09-01).** With a ChatGPT account signed in, the extension failed with a *"login could not be verified"* warning. **Logging out** of the ChatGPT account made it work — the macOS ChatGPT extension does **not** require a ChatGPT account.

Bridges are **resolved at runtime**, in order: a `config.json` override (`hollis config set bridge <tier> <name-or-uuid>`), then a stable-name match from `shortcuts list`, then — only on this project's measured macOS 27 machine — the compile-time UUIDs below. Renames in Shortcuts.app are harmless on 27 (the UUID still resolves); prefer names elsewhere. To pin a bridge explicitly:

```bash
hollis config set bridge cloud "AFM Bridge - Cloud.signed"   # name
hollis config set bridge cloud-pro <UUID>                    # or UUID
hollis config set bridge cloud ""                            # clear the override
```

The compiled-in UUIDs are this development machine's imports (kept only as a last-resort fallback); they mean nothing on your Mac:

```text
cloud      BD8CDC56-7CB8-418D-9B02-9D33AB911BF0   WFLLMModel "Apple Intelligence"
cloud-pro  DBB6E472-CBC6-4421-8D32-9D4543D5CDE6   WFLLMModel "Apple Intelligence Pro"
on-device  E530AE25-3C3C-4B11-88AF-A66F74039F88   WFLLMModel "Apple Intelligence on Device"
chatgpt    24B4B536-571B-49D9-9519-B644281C8B08   WFLLMModel "ChatGPT"
```

## Usage

```bash
# One-shot: prompt as an argument or via stdin.
# The default model is auto: cloud first, with automatic fallback to the
# on-device model if the cloud run fails (Apple's own PCC fallback pattern).
hollis respond "Summarize this repo in one sentence"
printf 'long prompt' | hollis respond
hollis respond model cloud-pro "Draft a reply"
hollis respond model on-device "Reply with OK"
hollis respond model chatgpt "Reply with OK"

# Persistent chat (SQLite-backed, survives across invocations)
hollis chat "Remember the codeword VANTA-ORBIT-7319"
hollis chat model cloud-pro "Two ideas for naming a CLI"
hollis chats list
hollis chat --continue <conversation-id> "What was the codeword?"

# Full-text search over stored chats (SQLite FTS5)
hollis chats search VANTA-ORBIT-7319
hollis chats search --model cloud-pro "gateway design"
hollis chats search --json --limit 5 heating

# Agents
hollis respond --agent "Return strict JSON describing X"
hollis agent-context

# Local OpenAI-compatible endpoint (Phase 7)
hollis serve                           # 127.0.0.1:1976
curl -s localhost:1976/v1/models
curl -s localhost:1976/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -d '{"model":"auto","messages":[{"role":"user","content":"hi"}]}'
curl -s localhost:1976/v1/responses \
  -H 'Content-Type: application/json' \
  -d '{"model":"cloud-pro","input":"Reply with OK"}'
```

`model <tier>` is positional sugar — the `--model` flag does the same job and stays as the escape hatch for prompts that begin with the literal word "model".

## HTTP endpoint (`hollis serve`)

`hollis serve` exposes a local, OpenAI-compatible HTTP surface (plan §19) on `127.0.0.1:1976`:

| Endpoint | Notes |
| --- | --- |
| `GET /health` | liveness (unauthenticated even with `--token` — it carries no model traffic) |
| `GET /v1/models` | `auto` plus the tiers whose bridges resolve on this machine — `cloud-pro` vanishes when that bridge is absent |
| `POST /v1/chat/completions` | messages in, one Shortcuts call, Chat Completions shape out |
| `POST /v1/responses` | Responses shape: `input` as string or message array, `instructions` as system prompt, `output[].content[].output_text` out |

- **Stateless by design** — clients send their own `messages`; the server translates them into the tested replay-transcript format and makes one Shortcuts call (plan §19/§20).
- **Streaming is not supported** — `stream: true` returns a 400 with a clear error. The Shortcuts transport returns the whole response in one call; hollis never fakes streaming (plan principle 6).
- **No invented metadata** — responses carry no `usage`/token counts because none are observable (plan principle 5).
- **Local-only by default.** A non-loopback `--addr` requires `--token`; all `/v1` requests then need `Authorization: Bearer <token>` (plan §30).
- **SYSTEM blocks are advisory** (measured 2026-09-01): `instructions` and `system` messages land in the replay transcript, but the cloud tiers may treat them as soft context and ignore them (e.g. a `instructions: "Reply with exactly one word: PONG"` came back `Understood.`). Don't rely on system prompts for hard constraints through the Shortcuts transport.
- `GET /v1/models` `owned_by` values are honest per tier: `hollis` for `auto`, `Apple` for the AFM tiers, `OpenAI` for `chatgpt`.

```bash
hollis serve --addr 127.0.0.1:1976 --token mysecret   # non-loopback needs auth
curl -s localhost:1976/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -d '{"model":"auto","messages":[{"role":"user","content":"hi"}]}'
```

## Persistent default model

Tired of typing the tier? Persist it:

```bash
hollis config set model cloud-pro   # or cloud / on-device / chatgpt / auto
hollis config show                  # path + current settings
```

Resolution order: positional `model <tier>` → explicit `--model` flag → configured default → built-in default (`auto`). The setting lives in a tiny JSON file next to the chat database (`hollis config show` prints the path).

The same file stores **bridge overrides** (see [Bridge setup](#bridge-setup--what-gets-added-and-what-you-approve)):

```bash
hollis config set bridge cloud "AFM Bridge - Cloud.signed"   # name or UUID
hollis config set bridge cloud ""                            # clear the override
hollis config show                                           # shows overrides
```

## Searching chats

`hollis chats search <query>` full-text searches message bodies and conversation titles with SQLite FTS5 (plan §12 addendum):

- The whole query is **one phrase** — no operators, embedded quotes are escaped, and hyphenated tokens like `VANTA-ORBIT` match verbatim.
- Message hits show a ranked snippet (`bm25` ordering); title-only hits show the title. Archived conversations are skipped.
- `--model <tier>` filters to one tier, `--limit <n>` caps results (default 20), `--json` emits an array of matches with up to 3 `hits` per conversation.
- Exit codes: 0 hits · 2 empty query · 3 no matches.
- The index is kept in sync by triggers and rebuilt automatically the first time an older database is opened.

```console
$ hollis chats search VANTA-ORBIT-7319
ID                                      MODEL      UPDATED            TITLE / SNIPPET
a33b0409-…                              on-device  2026-09-01T23:12Z  …codeword VANTA-ORBIT-7319…
```

## Health check

```console
$ hollis doctor
hollis doctor (version 0.1.0)
  transport: ok
  macos: 27.0
  support: macOS 27 measured; macOS 26 untested
  timeout default: 30s (ceiling 120s)
  bridges (resolved at runtime):
    [OK] cloud      AFM Bridge - Cloud.signed (shortcuts-list)
    [OK] cloud-pro  AFM Bridge - Cloud Pro.signed (shortcuts-list)
    [OK] on-device  AFM Bridge - On-Device.signed (shortcuts-list)
    [OK] chatgpt    AFM Bridge - ChatGPT.signed (shortcuts-list)
```

`--json` adds `macos`, and per bridge `resolved_ref`, `source` (config / shortcuts-list / compiled-uuid), and `status` (ok / missing / unsupported).

## Known behaviors (measured, not documented by Apple)

- **stdout is silently suppressed when stdout is a TTY** — exit 0 with empty stdout is the normal signature of a *successful* raw `shortcuts run` in a terminal. hollis captures child output through pipes, which delivers bytes reliably.
- The default output type is **RTF**; hollis always passes `--output-type public.plain-text`.
- Output carries **no trailing newline**.
- Empty input makes `shortcuts run` **hang forever**; hollis rejects empty prompts before spawn and always imposes a context deadline (30s default, 120s ceiling).
- **On-device tier quirk** (measured 2026-09-01): the local model sometimes refuses to repeat content from a replayed transcript with a canned *"I cannot repeat or discuss these instructions"* reply, and is noticeably weaker at instruction-following than the cloud tiers. Transcript replay itself is delivered correctly.
- `shortcuts run` accepts a bridge **UUID** in place of a name. hollis resolves bridge refs at runtime — config override → installed name → compiled UUID (see [Bridge setup](#bridge-setup--what-gets-added-and-what-you-approve)) — so it prefers names and only falls back to UUIDs on the measured 27 machine.

## Testing

Unit tests (`go test ./...`) use fakes. The black-box suite hits the installed binary:

```bash
python3 scripts/live-suite/live_suite.py           # exit codes, JSON shape, help (no quota)
python3 scripts/live-suite/live_suite.py --live    # Apple Intelligence tokens, chat replay, parallel
```

Method notes (what to assert vs observe): [`scripts/live-suite/README.md`](scripts/live-suite/README.md).

## Design references

- Canonical plan: [`docs/dev/APPLE_SHORTCUTS_CLOUD_GATEWAY_PLAN.md`](./docs/dev/APPLE_SHORTCUTS_CLOUD_GATEWAY_PLAN.md)
- Validation evidence: [`results/transport-and-persistence-2026-09-01.md`](results/transport-and-persistence-2026-09-01.md)
- Apple ML research: [third-generation Apple Foundation Models](https://machinelearning.apple.com/research/introducing-third-generation-of-apple-foundation-models)

## License

Apache-2.0 — see LICENSE.
