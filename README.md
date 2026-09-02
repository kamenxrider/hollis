# hollis

**Apple Intelligence Cloud and Cloud Pro from your terminal.**

Hollis turns the models exposed through macOS Shortcuts into a normal command-line tool and local API.

On the macOS 27 builds tested here, Apple's `fm` CLI exposes only the **on-device** model. Shortcuts still exposes **Cloud**, **Cloud Pro**, **On-Device**, and Apple's **ChatGPT** extension.

Hollis bridges that gap.

```bash
hollis respond --model cloud-pro "What's a closure in Go, in one sentence?"
hollis chat "Remember the project is called hollis"
printf 'long prompt' | hollis respond
hollis serve
```

After a one-time Shortcuts import, Hollis gives you:

* Cloud, Cloud Pro, On-Device, and ChatGPT model selection
* stdin/stdout for shell pipelines
* persistent local chats
* JSON output for scripts and agents
* an OpenAI-shaped local HTTP API
* automatic Cloud → On-Device fallback

Chats and configuration stay on your Mac. Model requests go to whichever provider you select through Shortcuts.

Tested on **macOS 27**. macOS 26 support is experimental — see [Compatibility](#compatibility).

## Models

| You type | Shortcuts model | Notes |
| --- | --- | --- |
| `auto` | Cloud → On-Device | Default; falls back locally if Cloud fails |
| `cloud` | Cloud | Apple server model via Private Cloud Compute |
| `cloud-pro` | Cloud Pro | macOS 27+; measured slower than Cloud and often stronger on harder prompts |
| `on-device` | On-Device | Runs locally and works offline |
| `chatgpt` | ChatGPT | Apple's ChatGPT extension, not an Apple model |

Apple does not publish stable backend model IDs for these Shortcuts choices. Hollis does not invent them.

See what this Mac can actually run:

```bash
hollis models
```

Set a persistent default:

```bash
hollis config set model cloud-pro
```

## What you need

* A Mac with **Apple Intelligence** enabled
* macOS 27 for the measured setup
* `/usr/bin/shortcuts` — included with macOS
* Network access for Cloud, Cloud Pro, and ChatGPT
* ChatGPT extension enabled in *System Settings → Apple Intelligence & Siri* if you want the ChatGPT bridge

On-Device works without a network connection.

## Install

Download the latest release and put the binary on your `PATH`:

```bash
# Apple Silicon
curl -fsSL -o hollis https://github.com/kamenxrider/hollis/releases/latest/download/hollis-darwin-arm64

# Intel
curl -fsSL -o hollis https://github.com/kamenxrider/hollis/releases/latest/download/hollis-darwin-amd64

chmod +x hollis
sudo mv hollis /usr/local/bin/
hollis version
```

The release binaries are not codesigned. Files downloaded with `curl` carry no quarantine flag, so macOS runs them without a Gatekeeper prompt.

Or install with Go 1.27+:

```bash
go install github.com/kamenxrider/hollis/cmd/hollis@latest
```

Or build from source:

```bash
go build -o "$(go env GOPATH)/bin/hollis" ./cmd/hollis
```

If a command appears to be missing after a `git pull`, rebuild first. An older binary on `PATH` is usually the cause.

### Create the bridge shortcuts

Hollis talks to Apple Intelligence through four small Shortcuts.

Generate them:

```bash
python3 scripts/make-bridge.py bridges/
```

Sign and import the Cloud bridge:

```bash
shortcuts sign --mode anyone \
  --input "bridges/AFM Bridge - Cloud.shortcut" \
  --output "bridges/AFM Bridge - Cloud.signed.shortcut"

open "bridges/AFM Bridge - Cloud.signed.shortcut"
```

Repeat the sign + `open` step for the other three bridges: Cloud Pro, On-Device, and ChatGPT.

Shortcuts.app will ask you to **Add Shortcut**. The first time a bridge runs, macOS may also ask you to **Allow** model access.

Only unsigned `.shortcut` files live in git. Signed files are created locally on your Mac.

Then check the installation:

```bash
hollis doctor
```

## Everyday use

Ask one question:

```bash
hollis respond "Summarize this repo in one sentence"
```

Pipe input:

```bash
printf 'Explain closures in Go' | hollis respond
```

Pick a model:

```bash
hollis respond --model cloud "Draft a short reply"
hollis respond --model cloud-pro "Analyze this bug"
hollis respond --model on-device "Reply with OK"
hollis respond --model chatgpt "Explain this API"
```

You can also put the model before the prompt:

```bash
hollis respond model cloud-pro "Analyze this bug"
```

## Persistent chats

Shortcuts model calls themselves are stateless.

Hollis adds local conversation persistence:

```bash
hollis chat "Remember the codeword VANTA-ORBIT-7319"
```

List conversations:

```bash
hollis chats list
```

Continue one later:

```bash
hollis chat --continue <id> "What was the codeword?"
```

Search them:

```bash
hollis chats search VANTA-ORBIT-7319
hollis chats search --model cloud-pro --json --limit 5 heating
```

The whole search query is treated as one phrase. Hyphens such as `VANTA-ORBIT` work.

Search exit codes: `2` empty query, `3` no matches.

## Scripts and agents

Return structured JSON instead of normal terminal output:

```bash
hollis respond --agent "Name three Go testing tips"
```

Inspect the context Hollis exposes to agents:

```bash
hollis agent-context
```

## Local OpenAI-shaped API

Start the server:

```bash
hollis serve
```

Default address: `127.0.0.1:1976`.

Health and models:

```bash
curl -s localhost:1976/health
curl -s localhost:1976/v1/models
```

Chat Completions:

```bash
curl -s localhost:1976/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -d '{
    "model": "cloud-pro",
    "messages": [
      {
        "role": "user",
        "content": "Explain closures in Go in one sentence."
      }
    ]
  }'
```

Hollis also exposes `POST /v1/responses`, with `input` as either a string or message array and optional `instructions`.

`/v1/models` lists `auto` plus the bridges Hollis can actually resolve on this Mac. For example, `cloud-pro` disappears when its bridge is not installed.

### API behavior

Shortcuts returns complete model responses rather than token streams, so Hollis does not fake streaming.

* `stream: true` returns **400**
* Apple does not expose token counts here
* `system` / `instructions` are advisory rather than hard isolation boundaries
* responses arrive as complete text

Binding outside loopback requires `--token`.

When a token is configured, `/v1/*` expects `Authorization: Bearer <token>`.

`/health` remains unauthenticated.

## Why not `fm`?

Apple ships its own Foundation Models CLI, `fm`.

On the macOS 27 build used to develop and test Hollis, the current public `fm` surface exposes only `system`. Its earlier Private Cloud Compute model option is no longer present.

Shortcuts, however, still exposes Cloud, Cloud Pro, On-Device, and ChatGPT.

Hollis does **not** patch or modify `fm`.

Its path is simply:

```text
hollis
   ↓
/usr/bin/shortcuts
   ↓
Use Model
   ↓
Cloud / Cloud Pro / On-Device / ChatGPT
```

Hollis is automation around Apple's existing Shortcuts model surface.

## How the bridge works

Each generated bridge is intentionally tiny:

```text
Shortcut Input
      ↓
Use Model
      ↓
Stop and Output
      ↓
hollis
```

Hollis feeds the prompt into the shortcut and captures its output as plain text.

### Bridge discovery

Hollis does not require Shortcuts UUIDs to be identical across Macs.

For each model tier it tries, in order:

1. An explicit configured bridge
2. A stable bridge name found through `shortcuts list`
3. A compiled development UUID as a last resort on the original development machine

Set a bridge manually:

```bash
hollis config set bridge cloud "AFM Bridge - Cloud.signed"
```

Clear it:

```bash
hollis config set bridge cloud ""
```

## Doctor

If something is not working:

```bash
hollis doctor
```

Example:

```text
$ hollis doctor
hollis doctor (version 0.1.0)
  transport: ok
  macos: 27.0
  support: macOS 27 measured; macOS 26 untested
  …
  bridges (resolved at runtime):
    [OK] cloud      AFM Bridge - Cloud.signed (shortcuts-list)
```

Machine-readable diagnostics:

```bash
hollis doctor --json
```

JSON output includes:

* macOS version
* bridge `resolved_ref`
* resolution `source`
* bridge `status`

Possible sources: `config`, `shortcuts-list`, `compiled-uuid`.

Possible states: `ok`, `missing`, `unsupported`.

## Compatibility

### macOS 27

**Measured.**

The tested Shortcuts model selector exposes Cloud, Cloud Pro, On-Device, and ChatGPT. All four bridge locations work with Hollis.

### macOS 26

**Experimental / untested.**

macOS 26 Shortcuts exposed three model locations — Cloud, On-Device, and ChatGPT. There was no Cloud Pro choice.

Generate macOS 26 bridges with:

```bash
python3 scripts/make-bridge.py --os 26
```

Then test:

```bash
hollis doctor
hollis respond --model cloud "Reply with OK"
```

Hollis refuses `cloud-pro` when that bridge is unavailable.

The macOS 26 `cloud` choice is the earlier PCC model generation, not the macOS 27 Cloud / Cloud Pro setup.

Please include the output of `hollis doctor` when reporting macOS 26 results.

Compatibility notes: [`results/macos-26-compat.md`](results/macos-26-compat.md)

## Shortcuts quirks Hollis handles

A few behaviors were discovered while testing the transport:

* raw `shortcuts run` attached directly to a terminal can appear to print nothing
* shortcut output can arrive as RTF rather than plain text
* empty input can leave `shortcuts run` waiting indefinitely
* Shortcuts returns complete responses instead of streaming tokens

Hollis therefore:

* captures shortcut output through a pipe
* requests plain text
* refuses empty prompts
* always uses a deadline
* does not fake streaming

Default timeout: **30 seconds**. Maximum: **120 seconds**.

Transport and persistence notes: [`results/transport-and-persistence-2026-09-01.md`](results/transport-and-persistence-2026-09-01.md)

## ChatGPT quirk

Measured on macOS 27 during development:

A **signed-in** ChatGPT account caused the Shortcuts extension to fail with `login could not be verified`.

Logging out allowed the shortcut bridge to work.

The macOS ChatGPT extension does not require a ChatGPT account for basic use.

## Model notes

Apple's third-generation Foundation Models: [Introducing Apple's third-generation Foundation Models](https://machinelearning.apple.com/research/introducing-third-generation-of-apple-foundation-models)

The Shortcuts choices roughly correspond to:

| Hollis | Shortcuts | Model family |
| --- | --- | --- |
| `cloud` | Cloud | AFM 3 Cloud via Private Cloud Compute |
| `cloud-pro` | Cloud Pro | AFM 3 Cloud Pro |
| `on-device` | On-Device | AFM 3 Core family |
| `chatgpt` | ChatGPT | OpenAI through Apple's extension |

Apple does not expose stable backend IDs through this interface, so these mappings intentionally stay at the family level.

A separate ADM 3 Cloud model family powers image-generation features such as Genmoji and is not exposed through the **Use Model** text action used by Hollis.

PCC is documented with a 32K context window. On-device models have a smaller working context and are more sensitive to prompt length.

## Testing

Unit tests:

```bash
go test ./...
```

Transport suite without using Apple model quota:

```bash
python3 scripts/live-suite/live_suite.py
```

Live model tests:

```bash
python3 scripts/live-suite/live_suite.py --live
```

Suite documentation: [`scripts/live-suite/README.md`](scripts/live-suite/README.md)

## More

* [`results/transport-and-persistence-2026-09-01.md`](results/transport-and-persistence-2026-09-01.md)
* [`results/macos-26-compat.md`](results/macos-26-compat.md)
* [Apple: Prompting an on-device foundation model](https://developer.apple.com/documentation/foundationmodels/prompting-an-on-device-foundation-model)
* [Apple: Adding server-side intelligence with Private Cloud Compute](https://developer.apple.com/documentation/foundationmodels/adding-server-side-intelligence-with-private-cloud-compute)

## License

Apache-2.0 — [LICENSE](LICENSE)
