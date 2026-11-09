# Hollis plugin

Ask Apple's models for help inside Claude Code or Codex, then continue in the
same conversation. It runs on an Apple-silicon Mac with macOS 27 and Apple
Intelligence, alongside gstack or on its own.

Start with the [step-by-step quick start](docs/quickstart.md): install the
plugin, run setup and make your first request.

<a id="install"></a>
<a id="claude-code"></a>
<a id="codex"></a>
<a id="what-setup-asks-you-to-do"></a>

[Installation and approvals](docs/quickstart.md) · [Everyday requests](docs/usage.md) ·
[Compatibility](docs/compatibility.md) · [Folder map](docs/package.md)

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

- Writing, planning, research synthesis and text-document comparison on all five
  text routes. Hollis does not add a web search engine; provide the relevant sources.
- Image understanding with Cloud, Cloud Pro or ChatGPT; Native Local accepts one PNG/JPEG when its image capability is reported.
- Image generation in **Animation, Illustration, Sketch, Genmoji and Any Style**.
  Attach a previous image when requesting a variation; choose local crop, pad or
  resize processing when you need a particular output shape.
- Folder processing with a preview, a specific call budget and resumable progress.
- On a supporting runtime, host context as one-shot messages, Local token-fit
  checks before large inputs, and consent-based context shortening. Local-only
  retries are opt-in; permissive guardrails are for supplied-text transformations.
- Hollis as the model behind your own scripts, or behind another tool through a
  local, token-protected OpenAI-compatible endpoint on this Mac. See
  [Hollis as the model behind another tool](docs/usage.md#hollis-as-the-model-behind-another-tool).

Any Style does not guarantee a photograph. References can be submitted, but
reliable use of their pixels remains unproven, including subject preservation.
ChatGPT image generation is unavailable through the tested Shortcut route.
These limits differ from the native Image Playground app. Copyable prompts,
document formats and model choices are in [everyday use](docs/usage.md).

## Alongside gstack

After a gstack plan review, ask Hollis to have Apple assess the agreed plan.
For example, in Claude Code:

```text
/hollis:hollis Ask Apple Cloud Pro to review the agreed plan above. Find a concrete missing constraint or explain why the plan covers the stated requirements. Use only the supplied context.
```

Then: “Ask Apple whether that finding still matters if we launch for one team
first.” Read the useful contribution in the same conversation; the host remains
responsible for judging and applying suggestions. See the
[reproduction steps](examples/demo.md), [sample plan](examples/plan.md)
and [gstack usage](skills/hollis/references/gstack.md).

Hollis is an independent companion, with no upstream gstack modification or
endorsement implied. Use the same skills for a letter, an event brief, an image
or a folder of documents.

## Other agents

The portable core follows [Agent Plugins 1.0.0](https://agent-plugins.org/specification),
so compatible skills-capable clients can discover and load its shared skills.
The skills are tested with Claude Code and Codex. See the
[compatible client list](https://agent-plugins.org/clients) for format support;
other clients have not been qualified with this package. Execution still needs
the eligible local Mac and the host's permission to run scripts.

## One package, multiple hosts

The plugin lives in Hollis's repository and has its own version and release
archive. Shared skills and scripts sit beside a portable Agent Plugins manifest
and small native Claude/Codex manifests. There are no separate host-specific
copies of the workflow. The portable format is already included; it is not a
third plugin to install. Claude Code and Codex are the supported hosts for this
release. Support for additional harnesses is future work: each needs a tested
installation and conversation flow on the eligible Mac before we claim support.
See the [folder map, format boundaries and build instructions](docs/package.md).

[Setup](docs/setup.md) · [Compatibility](docs/compatibility.md) ·
[Privacy and integrity](docs/privacy.md) · [Troubleshooting](docs/troubleshooting.md) ·
[Test evidence](docs/validation.md)
