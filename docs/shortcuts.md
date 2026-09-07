# Shortcuts transport and model notes

These observations describe the tested macOS builds. For installation and supported systems, see [compatibility and troubleshooting](compatibility.md).

## How it works

```text
hollis → /usr/bin/shortcuts → Use Model → Cloud / Cloud Pro / On-Device / ChatGPT
```

Each model bridge configures incoming text, runs **Use Model**, then **Stop and Output**. Receive is input configuration, not a separate action. Text-only requests feed the prompt on stdin. Image requests pass a private temporary prompt file and the image files together as repeated inputs. Hollis captures plain text back. It does not patch or modify `fm`, and needs no Apple model API key: the transport is the local Shortcuts app, running as you. The optional HTTP server has its own local bearer token.

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

The measured evidence behind these rules is summarized in [EVIDENCE.md](../EVIDENCE.md).

### The ChatGPT quirk

Measured on macOS 27 during development: a **signed-in** ChatGPT account made the Shortcuts extension fail with `login could not be verified`, and logging out let the bridge work. The macOS ChatGPT extension does not require an account for basic use.

Image Playground's separate **ChatGPT** style has a different scoped result:
on macOS 27.0 build `26A5425a`, the fixed, parameterized, native-Shortcuts,
and fresh-extension routes failed before inference with Apple's Shortcuts
ToolKit database sandbox denial, while the native Image Playground app worked.
This does not prove universal impossibility; Hollis does not silently retry or
substitute that route. See the [image-generation qualification note](image-generation.md#setup).

## Model notes

The Shortcuts choices correspond, at family level, to Apple's [third-generation Foundation Models](https://machinelearning.apple.com/research/introducing-third-generation-of-apple-foundation-models):

| Hollis | Shortcuts | Model family |
| --- | --- | --- |
| `cloud` | Cloud | AFM 3 Cloud via Private Cloud Compute |
| `cloud-pro` | Cloud Pro | AFM 3 Cloud Pro |
| `on-device` | On-Device | AFM 3 Core family |
| `chatgpt` | ChatGPT | OpenAI through Apple's extension |

Apple exposes no stable backend IDs through this interface, so these mappings stay at the family level deliberately. A separate ADM 3 Cloud family powers image features such as Genmoji and is not reachable through the **Use Model** text action. [Private Cloud Compute](https://developer.apple.com/private-cloud-compute/) is documented with a 32K context window; on-device models have a smaller working context and are more sensitive to prompt length.

[Back to Hollis](../README.md)
