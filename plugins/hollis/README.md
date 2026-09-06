# Hollis: Apple inside the conversation

Ask Apple's Cloud or Cloud Pro for a useful second perspective, turn documents
into clear writing, understand images, or generate an illustration—then continue
in the same agent conversation.

Plugin **0.1.0** uses the verified Hollis **0.3.0** runtime. It shares two skills
between Claude Code and Codex. It works alongside gstack and also without it.
There is no additional server, subscription, model API key or background job to set up.
Your existing host-agent service is separate.

**Requires a local Apple-silicon Mac running macOS 27 with Apple Intelligence.**
See [compatibility](docs/compatibility.md) and [privacy](docs/privacy.md).

## Install, ask, continue

The downloadable `hollis-plugin-0.1.0.zip` includes the ARM64 runtime, all five
bridges and both host installation entries. Extract it with Archive Utility.
Use the extracted **hollis-plugin-0.1.0 directory** as the marketplace path below.
For a source checkout, use the repository root instead; setup downloads the same
locked release bytes. No Homebrew, GitHub CLI, Go, Python, Node, shell edits or
administrator installation is required on the user's Mac.

Claude Code:

```text
/plugin marketplace add /absolute/path/to/hollis-plugin-0.1.0
/plugin install hollis@hollis-plugins
/hollis:hollis-setup
/hollis:hollis Ask Apple Cloud Pro to review the plan above. Focus on what we missed.
```

Codex installation from its terminal:

```sh
codex plugin marketplace add /absolute/path/to/hollis-plugin-0.1.0
codex plugin add hollis@hollis-plugins
```

Start a new conversation so the host loads the installed skills. Use
`$hollis:hollis-setup` to set up, then `$hollis:hollis Ask Apple Cloud to summarize
this document`, or select Hollis in the skills picker. Details are in
[setup](docs/setup.md) and [validation](docs/validation.md).

Setup offers Cloud, Cloud Pro, On-Device, ChatGPT and image generation. Select
what you want. It explains **Add Shortcut** and first-use **Allow** prompts before
opening them. Once that route is set up, ordinary requests run through Shortcuts
without a visible Image Playground editor.

Then continue naturally: “Ask it whether that still holds if we double the
capacity.” Relevant context goes with the follow-up; Hollis does not automatically
copy the whole host transcript into a second saved conversation.

## What you can ask for

- Writing, planning, research synthesis and text-document comparison on all four
  text routes. Hollis does not add a web search engine; provide the relevant sources.
- Image understanding with Cloud, Cloud Pro or ChatGPT.
- Image generation in **Animation, Illustration, Sketch, Genmoji and Any Style**.
  Use a previous image as a reference for a variation; choose local crop, pad or
  resize processing when you need a particular output shape.
- Folder processing with a preview, a specific call budget and resumable progress.

Any Style does not guarantee a photograph. References guide variations rather
than preserving exact identity. ChatGPT image generation is unavailable through
the tested Shortcut route. These limits differ from the native Image Playground app.

## Alongside gstack

After a gstack plan review, ask Hollis for Apple's assessment of the agreed plan.
Read the useful contribution in the same conversation and ask a follow-up. The
host remains responsible for judging and applying suggestions. See the
[recorded demonstration](examples/recorded-demo.md),
[reproduction steps](examples/demo.md), [sample plan](examples/plan.md)
and [gstack usage](skills/hollis/references/gstack.md).

Hollis is an independent companion, with no upstream gstack modification or
endorsement implied. Use the same skills for a letter, an event brief, an image
or a folder of documents.

[Setup](docs/setup.md) · [Compatibility](docs/compatibility.md) ·
[Privacy and integrity](docs/privacy.md) · [Troubleshooting](docs/troubleshooting.md)
