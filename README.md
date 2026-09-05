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
hollis serve                      # local OpenAI-shaped API
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

**1. Install the binary.**

```bash
# Apple Silicon. On Intel, use: HOLLIS_ASSET=hollis-darwin-amd64
HOLLIS_ASSET=hollis-darwin-arm64
curl -fsSL -o "$HOLLIS_ASSET" "https://github.com/kamenxrider/hollis/releases/latest/download/$HOLLIS_ASSET"

curl -fsSL -o SHA256SUMS https://github.com/kamenxrider/hollis/releases/latest/download/SHA256SUMS
shasum -a 256 -c SHA256SUMS --ignore-missing

chmod +x "$HOLLIS_ASSET" && sudo mv "$HOLLIS_ASSET" /usr/local/bin/hollis
```

**2. Install the bridge shortcuts.** Hollis reaches Apple Intelligence through four small Shortcuts, one per model tier. They ship unsigned, because a signed shortcut is an artifact of the Mac that signed it. This step is **blocking for model calls**: `doctor` still runs without the bridges, but reports `MISSING` and exits 3.

```bash
curl -fsSL -o hollis-bridges.zip https://github.com/kamenxrider/hollis/releases/latest/download/hollis-bridges.zip
unzip hollis-bridges.zip -d bridges

for f in bridges/*.shortcut; do
  shortcuts sign --mode anyone --input "$f" --output "${f%.shortcut}.signed.shortcut"
  open "${f%.shortcut}.signed.shortcut"
done
```

Shortcuts.app asks you to **Add Shortcut** for each one. The first time a bridge runs, macOS may also ask you to **Allow** model access.

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

The prompt comes from the argument or from stdin, so pipelines work. Each `respond` call is stateless. Default timeout is 30 seconds, ceiling 120. Hollis rejects a rendered prompt over 128 KiB before invoking Apple.

### Images

`respond` accepts PNG and JPEG files through the existing bridges:

```bash
hollis respond --image photo.jpg "What is this?"
hollis respond --model cloud-pro --image a.png --image b.png "Compare them"
```

An image request with no selected or configured model defaults directly to Cloud. Cloud and Cloud Pro accept repeated `--image`; ChatGPT accepts one image. `auto` and On-Device are rejected for images because the tested On-Device Shortcut ignored the pixels, making automatic fallback unsafe.

When images are present, give the prompt as an argument. Hollis writes it to a private temporary UTF-8 text file and passes that file plus the images as repeated Shortcuts inputs; the temporary prompt is deleted after the run. Do not pipe a second prompt through stdin with `--image`. Image chat history and HTTP image uploads are not part of `v0.2.0`.

## Persistent chats

Shortcuts model calls are stateless. Hollis stores conversations locally and replays the transcript each turn:

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
hollis serve                                    # http://127.0.0.1:1978
curl -s localhost:1978/health
curl -s localhost:1978/v1/models
curl -s localhost:1978/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -d '{"model":"cloud-pro","stream":false,"messages":[{"role":"user","content":"Explain closures in Go."}]}'

curl -s localhost:1978/v1/responses \
  -H 'Content-Type: application/json' \
  -d '{"model":"cloud-pro","stream":false,"input":"Explain closures in Go."}'
```

The Responses reply text is at `output[0].content[0].text`. Its `input` may be a string or a message array, with optional `instructions`. `/v1/models` lists `auto` plus only the tiers whose bridges resolve here, so `cloud-pro` disappears when its bridge is not installed.

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

Authentication is configured with `--token-file <private-file>` or `HOLLIS_API_TOKEN`; the token must contain at least 32 bytes and is never printed. `/v1/*` then expects `Authorization: Bearer <token>`, while `/health` stays unauthenticated. Hollis deliberately has no command-line `--token`, because process arguments can be visible through `ps`.

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

Pull requests from branches in this repository also receive an automated Poolside review. Fork pull requests are skipped because GitHub does not provide repository secrets to them.

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
hollis serve                                # local API on 127.0.0.1:1978
hollis doctor                               # check transport and bridges
hollis models                               # show available tiers
hollis config set model cloud-pro           # save a default tier
```

## License

Apache-2.0 — [LICENSE](LICENSE)
