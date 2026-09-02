# hollis

**Apple Intelligence from your terminal — including both cloud tiers.**

Apple's own `fm` CLI, on the macOS 27 build tested here, offers exactly one model: the on-device one. Its Private Cloud Compute option is gone.

The Shortcuts **Use Model** action, on the same machine at the same moment, offers four choices — and two of them are separate cloud tiers:

![The Use Model action on macOS 27.0 (26A5421a): Cloud, Cloud Pro, On-Device, ChatGPT](results/img/use-model-picker-26A5421a.png)

`fm --model pcc`, back when it worked, was one generic Private Cloud Compute target with no way to choose between cloud models. Shortcuts draws the distinction, and `/usr/bin/shortcuts` makes it scriptable. Hollis is the CLI and local API over that surface.

```bash
hollis respond "Summarize this repo in one sentence"
hollis respond --model cloud-pro "Analyze this bug"
printf 'long prompt' | hollis respond
hollis chat                       # interactive, remembers the conversation
hollis serve                      # local OpenAI-shaped API
```

Chats and configuration stay on your Mac. Model requests go to whichever provider you select through Shortcuts.

Measured on **macOS 27.0 (26A5421a)**. macOS 26 is untested — see [Compatibility](#compatibility).

## Quickstart

**1. Install the binary.**

```bash
# Apple Silicon
curl -fsSL -o hollis https://github.com/kamenxrider/hollis/releases/latest/download/hollis-darwin-arm64
# Intel
curl -fsSL -o hollis https://github.com/kamenxrider/hollis/releases/latest/download/hollis-darwin-amd64

chmod +x hollis && sudo mv hollis /usr/local/bin/
```

**2. Install the bridge shortcuts.** Hollis reaches Apple Intelligence through four small Shortcuts, one per model tier. They ship unsigned, because a signed shortcut is an artifact of the Mac that signed it:

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

Release binaries are not codesigned by a developer ID, but they carry the ad-hoc signature Go's linker produces, and files downloaded with `curl` get no quarantine flag — so macOS runs them without a Gatekeeper prompt. If a command seems to be missing after a `git pull`, rebuild: an older binary on `PATH` is usually the cause.

### What you need

A Mac with **Apple Intelligence** enabled, macOS 27 for the measured setup, and `/usr/bin/shortcuts` (included with macOS). Cloud, Cloud Pro and ChatGPT need network access; On-Device works offline. For the ChatGPT bridge, enable the extension in *System Settings → Apple Intelligence & Siri*.

## Models

| You type | Shortcuts model | Notes |
| --- | --- | --- |
| `auto` | Cloud → On-Device | Default; falls back locally if Cloud fails |
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

The prompt comes from the argument or from stdin, so pipelines work. Each `respond` call is stateless. Default timeout is 30 seconds, ceiling 120.

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

## Scripts and agents

```bash
hollis respond --agent "Name three Go testing tips"   # JSON + non-interactive
hollis models --json
hollis doctor --json --select bridges
hollis agent-context                                  # machine-readable CLI description
```

`--agent` is shorthand for `--json --no-input`. In `--no-input` mode hollis never waits on a terminal, and destructive commands require explicit confirmation flags (`hollis chats delete <id> --yes`).

Exit codes are stable and parseable:

| Code | Meaning |
| --- | --- |
| 0 | Success |
| 2 | Usage error — bad flag, unknown model, empty prompt, no matching command |
| 3 | Missing resource — unknown conversation id, no search hits, bridge not installed |
| 5 | Transport failure — the shortcut ran but produced nothing usable |
| 7 | Timeout — the run exceeded its deadline and was killed |
| 10 | Config or database error |

## Local OpenAI-shaped API

```bash
hollis serve                                    # http://127.0.0.1:1978
curl -s localhost:1978/health
curl -s localhost:1978/v1/models
curl -s localhost:1978/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -d '{"model":"cloud-pro","messages":[{"role":"user","content":"Explain closures in Go."}]}'
```

`POST /v1/responses` is also available, with `input` as a string or a message array plus optional `instructions`. `/v1/models` lists `auto` plus only the tiers whose bridges resolve here, so `cloud-pro` disappears when its bridge is not installed.

Port **1978**, not 1976 — `fm serve` uses 1976 throughout Apple's own examples, and two servers cannot share a port.

### What it deliberately does not do

Shortcuts returns a complete response rather than a token stream, so `stream: true` returns **400** instead of a faked stream. Apple exposes no token counts through this path, so no `usage` field is invented. `system` and `instructions` are advisory prompt content, not hard isolation boundaries.

Binding outside loopback requires `--token`, after which `/v1/*` expects `Authorization: Bearer <token>`. `/health` stays unauthenticated. Note that a token passed on the command line is visible to other local users via `ps`.

## Your data

Everything hollis stores lives in one directory:

```
~/Library/Application Support/hollis/
├── hollis.db       conversations, messages, and per-run diagnostics
└── config.json     default model and any bridge overrides
```

The run diagnostics record timing, exit code and an error class for each call — never prompt or response bodies beyond the messages you asked to store. To delete everything hollis knows, remove that directory. To delete one conversation, `hollis chats delete <id>`.

## How it works

```text
hollis → /usr/bin/shortcuts → Use Model → Cloud / Cloud Pro / On-Device / ChatGPT
```

Each bridge shortcut is three actions: **Receive** input, **Use Model**, **Stop and Output**. Hollis feeds the prompt in on stdin and captures plain text back. It does not patch or modify `fm`, and it holds no credentials — the transport is the local Shortcuts app, running as you.

### Bridge discovery

Shortcuts UUIDs differ on every Mac, so hollis resolves each tier at runtime, in order:

1. An explicit configured bridge
2. A stable bridge name found via `shortcuts list`
3. A compiled development UUID — kept as a reference for overrides, but never treated as proof the bridge exists

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
| Responses are complete, not streamed | Does not fake streaming |

Details: [`results/transport-and-persistence-2026-09-01.md`](results/transport-and-persistence-2026-09-01.md).

### The ChatGPT quirk

Measured on macOS 27 during development: a **signed-in** ChatGPT account made the Shortcuts extension fail with `login could not be verified`, and logging out let the bridge work. The macOS ChatGPT extension does not require an account for basic use.

## Why not `fm`?

Apple ships its own Foundation Models CLI, `fm`, and hollis neither patches nor replaces it. Two things make Shortcuts the more capable path today.

**Granularity.** Even when `fm` supported Private Cloud Compute, its selector was a single `pcc` target — one generic cloud model, with no way to ask for a specific tier. Shortcuts exposes Cloud and Cloud Pro as separate choices, so hollis can offer a distinction the CLI never had.

**Availability.** On macOS 27.0 build `26A5421a`, `fm` lists only `system`, and `fm available --model pcc` is rejected at argument validation. Whether that is deliberate or a beta regression, Apple has not said.

There is also a reason not to link the framework directly. Apple gates third-party PCC access behind an entitlement, App Store Small Business Program enrollment, and a two-million-download ceiling; a non-entitled binary calling `PrivateCloudComputeLanguageModel` fails with `ModelManagerError 1046`. That entitlement gates the **developer framework, not the user-facing automation surface** — Shortcuts is a shipped consumer feature, `shortcuts run` is a documented Apple CLI, and the bridges are shortcuts you could build by hand in a minute. Hollis automates a supported surface rather than working around a restriction.

That surface is one Apple can change in any build, exactly as it changed `fm` in this one, which is why every claim here names the build it was measured on.

Prior art: bridging to Apple Intelligence through a Shortcut was shown by **Joseph Humfrey** in [*The Shortcut to integrating Private Cloud Compute into my app*](https://joethephish.me/blog/the-shortcut-to-integrating-PCC/) (June 2025). Hollis is the hardened version of that idea plus the tier selection. Full evidence and citations: [`results/two-cloud-tiers-26A5421a.md`](results/two-cloud-tiers-26A5421a.md).

## Doctor

```bash
hollis doctor
hollis doctor --json
```

```text
$ hollis doctor
hollis doctor (version 0.1.0)
  transport: ok
  macos: 27.0 (26A5421a)
  support: macOS 27 measured; macOS 26 untested
  timeout default: 30s (ceiling 120s)
  bridges (resolved at runtime):
    [OK]        cloud      AFM Bridge - Cloud.signed (shortcuts-list)
    [MISSING]   cloud-pro  DBB6E472-CBC6-4421-8D32-9D4543D5CDE6 (compiled-uuid)
    [OK]        on-device  AFM Bridge - On-Device.signed (shortcuts-list)
    [OK]        chatgpt    AFM Bridge - ChatGPT.signed (shortcuts-list)

  MISSING: install that bridge (README “Quickstart” step 2), or point config at one you already have:
  hollis config set bridge <tier> <name-or-uuid>
```

`MISSING` means no bridge for that tier resolved here. Hollis refuses explicit tiers whose bridge did not resolve, rather than failing at the first prompt with a bare error.

JSON output adds the macOS version and build, each bridge's `resolved_ref`, its resolution `source` (`config`, `shortcuts-list`, `compiled-uuid`) and its `status` (`ok`, `missing`, `unsupported`).

## Compatibility

**macOS 27 — measured.** The tested Shortcuts selector exposes Cloud, Cloud Pro, On-Device and ChatGPT, and all four bridges work.

**macOS 26 — experimental, untested.** macOS 26 Shortcuts exposed three model locations: Cloud, On-Device and ChatGPT. There was no Cloud Pro, and hollis refuses `cloud-pro` when that bridge is unavailable. The macOS 26 `cloud` choice is the earlier PCC model generation, not the 27 Cloud / Cloud Pro pair.

```bash
python3 scripts/make-bridge.py --os 26 bridges/
hollis doctor
hollis respond --model cloud "Reply with OK"
```

Please include `hollis doctor` output when reporting macOS 26 results. Notes: [`results/macos-26-compat.md`](results/macos-26-compat.md).

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
go test ./...                                   # unit tests
python3 scripts/live-suite/live_suite.py        # transport suite, no model quota
python3 scripts/live-suite/live_suite.py --live # live model tests
```

Suite documentation: [`scripts/live-suite/README.md`](scripts/live-suite/README.md).

## More

* [`results/two-cloud-tiers-26A5421a.md`](results/two-cloud-tiers-26A5421a.md) — the two-tier finding, with evidence and prior art
* [`results/transport-and-persistence-2026-09-01.md`](results/transport-and-persistence-2026-09-01.md) — measured transport behaviour
* [`results/macos-26-compat.md`](results/macos-26-compat.md) — macOS 26 notes
* [Apple: Prompting an on-device foundation model](https://developer.apple.com/documentation/foundationmodels/prompting-an-on-device-foundation-model)
* [Apple: Adding server-side intelligence with Private Cloud Compute](https://developer.apple.com/documentation/foundationmodels/adding-server-side-intelligence-with-private-cloud-compute)

## License

Apache-2.0 — [LICENSE](LICENSE)
