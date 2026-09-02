# hollis

Ask Apple Intelligence from the terminal.

Apple’s own `fm` tool, on current macOS, only talks to the **on-device** model.
The stronger **cloud** models (and ChatGPT) still work — they’re just sitting
inside the Shortcuts app. Hollis is a small command that sends your prompt
through those shortcuts and prints the answer as plain text.

```bash
hollis respond "What's a closure in Go, in one sentence?"
hollis chat "Remember the project is called hollis"
hollis serve          # optional: OpenAI-shaped HTTP for local apps
```

After a one-time Shortcuts import, that’s the whole product. Chats are saved
on your Mac; Apple forgets every call. Nothing leaves the machine except the
model request Apple already makes.

Tested on **macOS 27**. macOS 26 is experimental — see [Compatibility](#compatibility).

## What you need

- A Mac with **Apple Intelligence** turned on (macOS 27 measured)
- `/usr/bin/shortcuts` (ships with macOS)
- Network for cloud / ChatGPT; on-device works offline
- ChatGPT: enable the extension in *System Settings → Apple Intelligence & Siri*

## Install

```bash
go build -o "$(go env GOPATH)/bin/hollis" ./cmd/hollis
```

If a command looks missing after a `git pull`, rebuild — an old binary on
`PATH` is the usual cause.

Then import four tiny Shortcuts (once per Mac). Hollis cannot call Apple
Intelligence without them.

```bash
python3 scripts/make-bridge.py bridges/

shortcuts sign --mode anyone \
  --input "bridges/AFM Bridge - Cloud.shortcut" \
  --output "bridges/AFM Bridge - Cloud.signed.shortcut"
open "bridges/AFM Bridge - Cloud.signed.shortcut"   # click Add Shortcut
```

Repeat sign + `open` for Cloud Pro, On-Device, and ChatGPT. Shortcuts.app
will ask you to **Add Shortcut**, then **Allow** model access the first time
you use each tier.

`hollis doctor` tells you what’s still missing.

Only unsigned `.shortcut` files live in git. You sign on your own Mac
(the signed files embed your identity).

## Everyday use

```bash
# One question. Default is auto: cloud first, on-device if cloud fails.
hollis respond "Summarize this repo in one sentence"
printf 'long prompt' | hollis respond

# Pick a tier (or write `model cloud-pro` before the prompt)
hollis respond --model cloud-pro "Draft a reply"
hollis respond --model on-device "Reply with OK"
hollis respond --model chatgpt "Reply with OK"

# A conversation that survives across commands
hollis chat "Remember the codeword VANTA-ORBIT-7319"
hollis chats list
hollis chat --continue <id> "What was the codeword?"
hollis chats search VANTA-ORBIT-7319

# JSON for scripts and agents
hollis respond --agent "Name three Go testing tips"
hollis agent-context
```

`hollis config set model cloud-pro` persists a default so you stop typing
the tier. `hollis config show` prints the file path.

## Models

| You type | What it is |
| --- | --- |
| `auto` | Cloud, then on-device if that fails (the default) |
| `cloud` | Apple’s server model (Private Cloud Compute) |
| `cloud-pro` | The larger server model — **macOS 27+** |
| `on-device` | Runs on the Mac, no network, no daily quota |
| `chatgpt` | Apple’s ChatGPT extension, not Apple’s own model |

On-device is smaller and fussier: keep prompts short. It sometimes refuses
to repeat things from earlier in a chat. Cloud and cloud-pro follow
instructions more reliably and count against a **daily iCloud quota**.

Apple publishes no backend model IDs. Hollis never invents them.
`hollis models` shows what this Mac can actually run.

More background (AFM names, RAM, context size): [Model notes](#model-notes).

## Talk to it like an OpenAI API

```bash
hollis serve                          # 127.0.0.1:1976
curl -s localhost:1976/health         # no auth, even with --token
curl -s localhost:1976/v1/models
curl -s localhost:1976/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -d '{"model":"auto","messages":[{"role":"user","content":"hi"}]}'
```

- Binding off loopback requires `--token`; then `/v1/*` needs
  `Authorization: Bearer <token>`. `/health` stays open.
- `stream: true` is a **400**. Shortcuts returns the whole answer at once;
  hollis will not fake streaming.
- No token counts — Apple doesn’t give us any.
- `system` / `instructions` are **advisory**. Cloud has ignored hard
  constraints there (asked for `PONG`, replied `Understood.`).
- `/v1/responses` is the Responses-shaped sibling: `input` as a string or
  message array, optional `instructions` as the system prompt.

`/v1/models` lists `auto` plus whatever bridges this Mac actually has.
`cloud-pro` disappears if that shortcut isn’t installed.

## Compatibility

- **macOS 27 — measured.** All four Shortcuts locations work.
- **macOS 26 — untested.** Shortcuts there had three locations (on-device,
  one cloud, ChatGPT) and no Cloud Pro. Hollis refuses `cloud-pro` when
  that bridge is missing. 26 `cloud` is last year’s PCC model, not AFM 3.
- **`fm` is not used.** On 26 the Foundation Models API had no cloud
  backend; Shortcuts is the path.

On 26: `python3 scripts/make-bridge.py --os 26`, sign, import,
`hollis doctor`, then `hollis respond --model cloud "Reply with OK"`.
Please file what doctor printed. Notes: [`results/macos-26-compat.md`](results/macos-26-compat.md).

## How it finds the shortcuts

Hollis does not hard-require this Mac’s UUIDs. For each tier it tries, in
order:

1. `hollis config set bridge <tier> <name-or-uuid>`
2. A stable name from `shortcuts list` (`AFM Bridge - Cloud.signed`, …)
3. Compiled-in UUIDs — **only as a last resort on the original 27
   development machine**. They mean nothing on yours.

```bash
hollis config set bridge cloud "AFM Bridge - Cloud.signed"
hollis config set bridge cloud ""          # clear
```

ChatGPT quirk (measured 2026-09-01): a **signed-in** ChatGPT account
failed with “login could not be verified.” Logging out fixed it. The
macOS extension does not need an account.

## Search, doctor, quirks

```bash
hollis chats search VANTA-ORBIT-7319
hollis chats search --model cloud-pro --json --limit 5 heating
```

The whole query is one phrase (hyphens like `VANTA-ORBIT` work). Exit
**2** if the query is empty, **3** if nothing matches.

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

`hollis doctor --json` adds `macos` plus each bridge’s `resolved_ref`,
`source` (config / shortcuts-list / compiled-uuid), and `status`
(ok / missing / unsupported).

Things Apple doesn’t document, measured here:

- Raw `shortcuts run` in a terminal often prints **nothing** (TTY). Hollis
  always captures through a pipe.
- Default shortcut output is **RTF**; hollis asks for plain text.
- Empty input hangs `shortcuts run` forever. Hollis refuses empty prompts
  and always uses a deadline (30s default, 120s ceiling).

## Testing

```bash
go test ./...
python3 scripts/live-suite/live_suite.py           # no Apple quota
python3 scripts/live-suite/live_suite.py --live    # hits the models
```

How that suite thinks: [`scripts/live-suite/README.md`](scripts/live-suite/README.md).

## Model notes

Apple’s third-generation Foundation Models ([announcement](https://machinelearning.apple.com/research/introducing-third-generation-of-apple-foundation-models)):

| hollis | Shortcuts | Roughly |
| --- | --- | --- |
| `cloud` | Cloud | AFM 3 Cloud on Private Cloud Compute |
| `cloud-pro` | Cloud Pro | AFM 3 Cloud Pro |
| `on-device` | On-Device | AFM 3 Core, or Core Advanced on “most capable” Apple silicon |
| `chatgpt` | ChatGPT | OpenAI, via Apple’s extension |

A fifth model (ADM 3 Cloud, images/Genmoji) is not reachable through the
Use Model **text** action.

PCC is documented at a 32K context; on-device is much smaller. Third-party
reporting puts Core Advanced around M3+ / 12GB RAM; Apple only says “most
capable Apple silicon.” Apple: PCC “ensures that user data is never stored
or shared with anyone, including Apple.”

## More

Working notes (not the user manual): [`docs/dev/`](docs/dev/).

- Plan: [`docs/dev/APPLE_SHORTCUTS_CLOUD_GATEWAY_PLAN.md`](docs/dev/APPLE_SHORTCUTS_CLOUD_GATEWAY_PLAN.md)
- Transport evidence: [`results/transport-and-persistence-2026-09-01.md`](results/transport-and-persistence-2026-09-01.md)
- Apple: [on-device prompting](https://developer.apple.com/documentation/foundationmodels/prompting-an-on-device-foundation-model) · [PCC](https://developer.apple.com/documentation/foundationmodels/adding-server-side-intelligence-with-private-cloud-compute)

## License

Apache-2.0 — [LICENSE](LICENSE).
