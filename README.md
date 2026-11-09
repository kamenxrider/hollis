<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/assets/hollis-logo-dark.svg">
  <img src="docs/assets/hollis-logo.svg" alt="hollis: Your Mac has more to say." width="680">
</picture>

Hollis brings Apple Intelligence to your terminal, scripts and agents, with a
local OpenAI-compatible server for other apps. Use Apple's on-device model or
choose Cloud, Cloud Pro and other routes through Apple Shortcuts. Hollis needs
no model API key or new account and adds no per-request charge.

## Requirements

- An Apple-silicon Mac running macOS 27 or later.
- Apple Intelligence enabled in System Settings, with model downloads complete.
- Network access for Cloud, Cloud Pro and ChatGPT. Local works offline.
  [Full requirements and limits](docs/getting-started.md#install).

## Quick start

Open Terminal. Start with Local, which needs no Shortcut import.

1. Install with Homebrew:
   ```bash
   brew install kamenxrider/tap/hollis
   ```
   Or [download, inspect and run the installer](docs/getting-started.md#install).
2. Check your Mac:
   ```bash
   hollis doctor --model local
   ```
3. Ask:
   ```bash
   hollis respond --model local "Reply with OK"
   ```

## Models

| Model | Where it runs |
| --- | --- |
| `local` | On your Mac, through Apple's native SDK |
| `cloud` | Apple's Private Cloud Compute, through Shortcuts |
| `cloud-pro` | Private Cloud Compute with increased reasoning, through Shortcuts |
| `on-device` | On your Mac, through Shortcuts |
| `chatgpt` | Apple's ChatGPT extension, through Shortcuts |

The default `auto` tries Cloud, with one On-Device fallback only for a confirmed
missing bridge. Use `--model local` to keep requests on your Mac.

## Guides

- [Getting started](docs/getting-started.md): installation, everyday use and fixes.
- [Claude Code and Codex plugin quick start](plugins/hollis/docs/quickstart.md).
- [Reference docs](docs/overview.md#reference) and [feature details](docs/overview.md).
- [Privacy: what leaves your machine](docs/privacy.md).
- [Verification and trust](docs/verification.md).
- [Release notes](docs/releases/README.md).

<!-- Keep older deep links working. Details now live in the linked guides. -->
<a id="add-shortcuts"></a><a id="bridge-discovery"></a><a id="clients-listing-is-not-the-same-as-working"></a><a id="cloud-availability-and-limits"></a><a id="compatibility"></a>
<a id="doctor"></a><a id="everyday-use"></a><a id="generate-an-image-through-the-api"></a><a id="how-it-works"></a><a id="if-something-breaks"></a>
<a id="included-from-030"></a><a id="inline-image-input"></a><a id="install"></a><a id="install-verified"></a><a id="limits-and-caveats"></a>
<a id="local-openai-shaped-api"></a><a id="model-notes"></a><a id="openai-compatible-api"></a><a id="other-install-routes"></a><a id="quick-reference"></a>
<a id="quickstart"></a><a id="reference"></a><a id="scripts-and-agents"></a><a id="shortcuts-quirks-hollis-handles"></a><a id="testing"></a>
<a id="the-chatgpt-quirk"></a><a id="use-hollis-in-claude-code-and-codex"></a><a id="verification-and-trust"></a><a id="verified-install-step-by-step"></a><a id="what-leaves-your-machine"></a>
<a id="what-you-need"></a><a id="whats-in-031"></a><a id="whats-new-in-070"></a><a id="why-not-fm"></a><a id="your-data"></a>
<a id="your-data-and-uninstalling"></a>

[Install and use Hollis](docs/getting-started.md) · [Limits](docs/overview.md#limits-and-caveats) ·
[Verify a download](docs/verification.md) · [Your data and removal](docs/getting-started.md#updating-and-uninstalling) ·
[Evidence](EVIDENCE.md) · [Contributing](CONTRIBUTING.md) · [Apache-2.0 license](LICENSE)
