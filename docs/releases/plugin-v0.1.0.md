# Hollis plugin 0.1.0 — Apple inside the conversation

Bring Apple Cloud and Cloud Pro into the agent conversation you already use.
Ask for a perspective on a plan, compare documents, understand an image or
generate an illustration, then follow up naturally.

## Install and ask

The [plugin guide](../../plugins/hollis/README.md#install) covers Claude Code and
Codex installation from GitHub or the downloadable archive. In Claude, start
with `/hollis:hollis-setup`; in Codex, use `$hollis:hollis-setup`.
Setup explains the required **Add Shortcut** and first-use **Allow** prompts.

The archive bundles the provenance-verified **Hollis 0.3.3 ARM64 runtime** and
all five bridges. Source installations fetch those same locked assets. Normal
setup uses macOS utilities, with no Homebrew, GitHub CLI, Go, Python, Node or
administrator installation required. GitHub build attestations, the archive
checksum and runtime verification receipts accompany the release.

## Included

- Shared `hollis` and `hollis-setup` skills, a portable Agent Plugins manifest,
  and native Claude Code and Codex manifests.
- Text and document tasks through Cloud, Cloud Pro, On-Device and ChatGPT;
  image understanding through the three supported online routes.
- Animation, Illustration, Sketch, Genmoji and Any Style generation, reference
  submission and local crop/pad/resize controls.
- Contextual follow-ups, explicit model selection, useful answers inline and
  absolute image links. No silent model substitution.
- Paced folder processing with a preview, explicit call budget and resumption.
- A gstack companion workflow and document/image examples, with no
  upstream modification or endorsement implied.
- Versioned private installation, preserved settings and custom bridges,
  verified rollback, and clear status for the runtime, state path and discovery.
- Runtime 0.3.2's clearer `request_declined` and `shortcut_failed` errors, plus
  the earlier path-free image-reference diagnostics.

## Tested scope and limits

Initial support is a local, physical Apple-silicon Mac on the tested macOS 27
route, with Apple Intelligence enabled. Cloud and Cloud Pro use PCC through
Shortcuts; the calling agent still receives the submitted context and result.
See [privacy](../../plugins/hollis/docs/privacy.md).

The existing-account validation covers both hosts, all four text routes,
three vision routes, all five generation styles and paced batches. Package
checks cover extraction, executable modes, installation, upgrades, rollback,
corrupt previous runtimes and locked source downloads.

Fresh-account setup and first Apple consent remain untested. Any Style does
not guarantee photography. Reference bytes can be submitted, but reliable use
of their pixels remains unproven. ChatGPT image generation is unavailable
through the tested Shortcut route. Images require an unlocked session; no
visible Image Playground automation is used. Host-visible inline rendering
remains separate from the confirmed absolute file-link behavior.

Historical recordings and package-review reports are retained privately and
are not included in the upcoming source archives or plugin packages.
