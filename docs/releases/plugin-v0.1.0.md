# Hollis plugin 0.1.0

Bring Apple Cloud and Cloud Pro into the agent conversation you already use.
Ask for a perspective on a plan, compare documents, understand an image or
generate an illustration, then follow up naturally.

**Status: prepared locally, not published.** This package pins the released
Hollis 0.3.1 runtime. Its release assets passed checksum and GitHub build
provenance verification before the lock was updated.

- Shared `hollis` and `hollis-setup` skills with Claude Code and Codex manifests.
- Guided Mac setup with individually selected bridges and explained Apple prompts.
- Bundled ARM64 Hollis 0.3.1 and all five bridges; exact locked downloads for source installs.
- Verified runtime provenance, private versioned installation, preservation of
  existing configuration and newer runtimes, and explicit rollback.
- Existing text, document, vision, generation, reference and paced-batch capabilities.
- An independent gstack companion workflow, plus a broader document/image example.

Initial support is the tested local Apple-silicon/macOS 27 route. Runtime assets
are verified before packaging and before local execution/extraction. A
separately authorized GitHub release build is still required to attest the
outer plugin archive.

Five image styles are exposed; Any Style is not a photorealism guarantee.
Reference submission is supported, but reliable use of reference pixels remains
unproven, including subject preservation. ChatGPT generation remains unavailable
through the tested Shortcut route. No visible Image Playground automation is used.

See [setup and limits](../../plugins/hollis/README.md),
[privacy](../../plugins/hollis/docs/privacy.md) and
[validation](../plugin-validation.md). See the [runtime 0.3.1 release notes](v0.3.1.md)
for the image-reference privacy correction and source-build identity. Public publication is
pending separate authorization; this source contains the review package and
release workflow.

## Review corrections

- Before installation, bridge status returns structured `setup_required`, with
  exit 0 and no installation writes or bridge discovery. Restricted access and
  damaged state retain distinct outcomes.
- Managed installation/check results report verified rollback availability.
  Repair upgrades preserve corrupt old files without advertising them as usable;
  a refused rollback leaves the current runtime selected.
- Stored `auto` is explicitly treated as no concrete preference, so the skill
  selects Cloud. Both hosts retain absolute image previews and file links.
- Archive tests verify executable permissions, exact contents and native macOS
  `ditto` extraction. The earlier review ZIP is preserved, not overwritten.

[All review dispositions and validation](../plugin-review-closure.md) are
recorded. The new package contains the released 0.3.1 runtime and its privacy
correction. Native extraction, installation, real 0.3.0-to-0.3.1 upgrade and
rollback, corrupt-previous recovery and source downloads passed. Earlier 0.3.0
archives and receipts are preserved as historical evidence.
