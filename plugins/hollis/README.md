# Hollis: Apple inside the conversation

Ask Apple's Cloud or Cloud Pro for a useful second perspective, turn documents
into clear writing, understand images, or generate an illustration—then continue
in the same agent conversation.

It works in **Claude Code and Codex**, alongside gstack or on its own. You ask
in the conversation; Hollis calls the selected Apple route on your Mac and
brings the result back. No separate server or model API key is needed.

**Plugin 0.1.0** bundles the released, provenance-verified Hollis **0.3.2**
runtime and all five bridges. Get the [plugin release](https://github.com/kamenxrider/hollis/releases/tag/plugin-v0.1.0)
or install from this repository below. Earlier review archives and their
receipts remain historical evidence; see [validation](docs/validation.md).

**Requires a local Apple-silicon Mac running macOS 27 with Apple Intelligence.**
Have Claude Code or Codex installed and signed in. The host must execute on this
Mac; a remote Linux agent cannot use Apple Intelligence through this package.
See [compatibility](docs/compatibility.md).

**Start here:** [Install](#install) · [Ask and continue](#ask-and-continue) ·
[Everyday examples](docs/usage.md) · [gstack](#alongside-gstack) ·
[Folder map and maintenance](docs/package.md)

## Install

Install from GitHub using the host commands below, or download and extract
`hollis-plugin-0.1.0.zip` from the [release page](https://github.com/kamenxrider/hollis/releases/tag/plugin-v0.1.0).
The archive includes the runtime and bridges; a source installation downloads
those same locked assets during setup.

| What you have | Folder to give the host | Runtime and bridges |
| --- | --- | --- |
| `hollis-plugin-0.1.0.zip`, extracted with Archive Utility | The outer `hollis-plugin-0.1.0` folder | ARM64 executable and all five bridges are bundled |
| A Hollis source checkout containing this guide | The repository root | Setup downloads the exact assets in the committed lock |

Replace `/absolute/path/to/hollis-package` below with that folder. It contains
the marketplace entries and `plugins/hollis/`. **Do not point marketplace
installation at the inner `plugins/hollis` folder.** Keep the source/extracted
folder available for later local marketplace updates.

Beyond the host app, normal setup uses macOS utilities: no Homebrew, GitHub CLI,
Go, Python, Node, shell configuration edits or administrator installation.

### Claude Code

Enter these in the **Claude conversation**, one at a time:

```text
/plugin marketplace add kamenxrider/hollis
/plugin install hollis@hollis-plugins
```

For an extracted archive or local checkout, replace `kamenxrider/hollis` in the
first command with `/absolute/path/to/hollis-package`.

Choose the installation scope in Claude. If the install summary asks for
`/reload-plugins`, run it; otherwise start a new session if the skills are not
listed. Then run:

```text
/hollis:hollis-setup
```

### Codex

Run these in a **terminal on the same Mac**:

```sh
codex plugin marketplace add kamenxrider/hollis --ref plugin-v0.1.0
codex plugin add hollis@hollis-plugins
```

For an extracted archive or local checkout, use
`codex plugin marketplace add "/absolute/path/to/hollis-package"` for the first
command; omit `--ref` for a local folder.

Start a new Codex conversation and type the following there, **not in the shell**:

```text
$hollis:hollis-setup
```

You can also select `hollis:hollis-setup` in the skills picker. If `codex plugin`
is unavailable in your installation, use a plugin-capable Codex version; this
guide does not assume that every older host understands the format.

### What setup asks you to do

Setup offers Cloud, Cloud Pro, On-Device, ChatGPT and image generation. Select
what you want. It explains **Add Shortcut** and first-use **Allow** prompts before
opening them. Existing bridges and custom settings are preserved. You can skip
capabilities and add them later by asking setup again. If interrupted, ask it to
continue; there is no need to start over.

Setup verifies installation and bridge discovery. **Your first successful Apple
answer or image confirms that capability works.** On the tested, already approved
account, requests ran without per-image clicks or a visible Playground editor.
Fresh-account and first-consent testing remain open. Image generation requires
an unlocked session. [Setup and recovery details](docs/setup.md).

Cloud and Cloud Pro use Apple's **Private Cloud Compute through Shortcuts**,
separate from the native Foundation Models developer API. The calling agent
receives the context it sends and Apple's result. Testing observed no separate
Apple model charge, with rate limits; your host service has its own costs and
privacy policy. [Privacy and integrity](docs/privacy.md).

## Ask and continue

In Claude Code:

```text
/hollis:hollis Ask Apple Cloud to summarize the plan above and identify one unresolved question.
```

In Codex:

```text
$hollis:hollis Ask Apple Cloud to summarize the plan above and identify one unresolved question.
```

Then continue naturally: “Ask it whether that still holds if we double the
capacity.” The host sends relevant context with the next request and returns
Apple's answer inline, labelled with its model. It does not automatically save
a second copy of the entire conversation.

Choose a model in your request, such as “Use Cloud Pro” or “Use On-Device.”
Otherwise the plugin uses your existing concrete preference, then Cloud. It
does not silently switch models after a failure. Generated images come with an
absolute **Open image** link and a preview where the host supports it.

## What you can ask for

- Writing, planning, research synthesis and text-document comparison on all four
  text routes. Hollis does not add a web search engine; provide the relevant sources.
- Image understanding with Cloud, Cloud Pro or ChatGPT.
- Image generation in **Animation, Illustration, Sketch, Genmoji and Any Style**.
  Attach a previous image when requesting a variation; choose local crop, pad or
  resize processing when you need a particular output shape.
- Folder processing with a preview, a specific call budget and resumable progress.

Any Style does not guarantee a photograph. References can be submitted, but
reliable use of their pixels remains unproven, including subject preservation.
ChatGPT image generation is unavailable through the tested Shortcut route.
These limits differ from the native Image Playground app. Copyable prompts,
document formats and model choices are in [everyday use](docs/usage.md).

## Alongside gstack

After a gstack plan review, ask Hollis for Apple's assessment of the agreed plan.
For example, in Claude Code:

```text
/hollis:hollis Ask Apple Cloud Pro to review the agreed plan above. Find a concrete missing constraint or explain why the plan covers the stated requirements. Use only the supplied context.
```

Then: “Ask Apple whether that finding still matters if we launch for one team
first.” Read the useful contribution in the same conversation; the host remains
responsible for judging and applying suggestions. See the
[recorded demonstration](examples/recorded-demo.md),
[reproduction steps](examples/demo.md), [sample plan](examples/plan.md)
and [gstack usage](skills/hollis/references/gstack.md).

Hollis is an independent companion, with no upstream gstack modification or
endorsement implied. Use the same skills for a letter, an event brief, an image
or a folder of documents.

## One package, multiple hosts

The plugin lives in Hollis's repository and has its own version and release
archive. Shared skills and scripts sit beside a portable Agent Plugins manifest
and small native Claude/Codex manifests. There are no separate host-specific
copies of the workflow. The portable format is already included; it is not a
third plugin to install. Claude Code and Codex are the supported hosts for this
release. Support for additional harnesses is future work: each needs a tested
installation and conversation flow on the eligible Mac before we advertise it.
See the [folder map, format boundaries and build instructions](docs/package.md).

[Setup](docs/setup.md) · [Compatibility](docs/compatibility.md) ·
[Privacy and integrity](docs/privacy.md) · [Troubleshooting](docs/troubleshooting.md) ·
[Test evidence](docs/validation.md)
