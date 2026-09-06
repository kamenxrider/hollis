# Hollis plugin 0.1.0

Bring Apple Cloud and Cloud Pro into the agent conversation you already use.
Ask for a perspective on a plan, compare documents, understand an image or
generate an illustration, then follow up naturally.

**Status: prepared locally, not published.** This package intentionally pins
the verified Hollis 0.3.0 runtime. A prepared 0.3.1 target is documented
separately, but the official lock does not move until its release and provenance
are separately authorized.

- Shared `hollis` and `hollis-setup` skills with Claude Code and Codex manifests.
- Guided Mac setup with individually selected bridges and explained Apple prompts.
- Bundled ARM64 Hollis 0.3.0 and all five bridges; exact locked downloads for source installs.
- Verified runtime provenance, private versioned installation, preservation of
  existing configuration and newer runtimes, and explicit rollback.
- Existing text, document, vision, generation, reference and paced-batch capabilities.
- An independent gstack companion workflow, plus a broader document/image example.

Initial support is the tested local Apple-silicon/macOS 27 route. Runtime assets
are verified before packaging and before local execution/extraction. A
separately authorized GitHub release build is still required to attest the
outer plugin archive.

Five image styles are exposed; Any Style is not a photorealism guarantee and
references guide variations. ChatGPT generation remains unavailable through the
tested Shortcut route. No visible Image Playground automation is used.

See [setup and limits](../../plugins/hollis/README.md),
[privacy](../../plugins/hollis/docs/privacy.md) and
[validation](../plugin-validation.md). See the [prepared runtime 0.3.1 notes](v0.3.1.md)
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
recorded. The corrected source was tested with a local 0.3.1 build. The existing
archive still bundles 0.3.0; a new complete bundle follows authorized runtime
publication, provenance verification and a lock update.
