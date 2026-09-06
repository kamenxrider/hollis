# Plugin 0.1.0 validation — 6 September 2026

The local review package works in Claude Code and Codex on the tested existing
Mac account. **Full first-time installation acceptance remains open:** this run
did not have a clean macOS account with fresh Apple approvals. Public publication
and the GitHub build that attests the outer plugin archive are also pending.

The [recorded demonstration](../examples/recorded-demo.md) shows the useful
Apple contribution first. [Sanitized results](../examples/recorded/results.json)
retain actual answers, timestamps and image checksums. Private raw receipts
remain in the maintainer workspace; no credentials or host settings are packaged.

## Observed live behavior

Hardware: physical Apple-silicon Mac16,8; macOS 27.0 build 26A5425a.
Hollis 0.3.0, Claude Code 2.1.263, Codex CLI 0.153.4.

| Flow | Result |
|---|---|
| Text: Cloud, Cloud Pro, On-Device, ChatGPT | 4 successful calls; requested/used tier matched; correct arithmetic |
| Image understanding: Cloud, Cloud Pro, ChatGPT | 3 successful calls; each identified the red-left/blue-right fixture |
| Animation, Illustration, Sketch, Genmoji, Any Style | Each generated twice: 10 successful, distinct output files |
| Image-reference follow-up | Successful explicit attachment; receipt identifies the source hash; local 1200×800 padding |
| Folder processing | Two inputs; budget-one run stopped with one pending; resume completed it; one attempt per input |
| Claude + gstack | Real plan review, then Cloud assessment and Cloud Pro contextual challenge in one host conversation |
| Claude images | Illustration plus an explicit reference follow-up; final preview and absolute file link |
| Codex | Document-derived Cloud answer inline, then Sketch output with 16:9 crop and absolute file link |

Total: **26 successful Apple invocations**, including **14 image generations**.
No visible Image Playground editor or per-generation user click was required
in these tests. Existing Apple approvals were already present. No rate limit
occurred during this suite. An initial image command failed on missing bridge
configuration before provider dispatch; that record is retained separately.

Live execution was serialized. The launcher uses a five-second minimum quiet
interval, or 45 seconds for Cloud Pro, and now adds one second to cover integer
timestamp rounding. Boundary arithmetic is covered by regression tests; the
earlier live recording used the five-/45-second settings before that extra
rounding guard. The recorded Cloud batch also used an overly conservative
45-second wrapper pause, subsequently corrected using its stored tier. Hollis
retains its own internal batch pacing. No failed generation was automatically retried.

These are integration successes, not perfect model-quality scores. Visual
inspection found unwanted lettering in some images and substantial scene changes
in a reference follow-up. Codex's invitation had 35 words instead of 45. Any Style
is not a photography guarantee; ChatGPT generation was not attempted because its
tested Shortcut route remains unavailable.

## Packaging, installation and regression checks

- Both native host installation paths accepted the same local source package.
  Claude's `/hollis:hollis` and `/hollis:hollis-setup` are documented; Codex exposed
  `hollis:hollis` and `hollis:hollis-setup`, including a tested
  `$hollis:hollis-setup` request.
- Bundled and source-download runtime installation passed with only macOS
  utilities on the installer PATH. The actual source download matched the lock.
  Real `arch -x86_64 /bin/bash ... setup.sh check` passed under Rosetta.
- Canonical Agent Plugins 1.0 JSON Schema, native manifests, shared skills,
  source identity checks and shell syntax were validated.
- Runtime binary and five-bridge ZIP were authenticated against GitHub build
  provenance and their locked v0.3.0 release commit. Local hash checks occur
  before managed execution or extraction. The outer archive has a checksum;
  only the separately authorized GitHub build can supply its GitHub attestation.
- **24 provider-free regression tests** pass: corruption, archive traversal,
  unsafe paths, interrupted locks, preservation of state/custom bridges,
  newer installs, upgrade/rollback fixtures, piped input and exit codes,
  cancellation cleanup, pacing, optional bridge independence, pending imports,
  Rosetta/Intel/virtual/restricted classification and rejected/empty provenance.
- Readiness is now read-only and safe to query concurrently. The installed Codex
  sandbox test correctly returned unknown Shortcuts access, exit 5, for both
  routes; it no longer invented a stale setup lock. Host-access checks in the
  preceding live run discovered both routes and permitted the requested calls.
  No inference occurred during the diagnostic retest.

## Fixes made from observed failures

1. A discovered image shortcut had no runtime mapping. Setup now distinguishes
   discovery from configuration and connects the existing bridge without an import.
2. Claude initially printed a relative image filename. The shared skill now
   requires an absolute Markdown file link; subsequent Claude and Codex replies did so.
3. Concurrent readiness checks contended for an unnecessary setup lock, and
   denied lock creation looked like an active operation. Status no longer takes
   that lock; actual write denial reports filesystem access required.

## Remaining acceptance work

| Scenario | What remains |
|---|---|
| Clean supported Mac account | Download and install the package with no development tools, select bridges, approve first use, then repeat text and images without recurring Apple clicks |
| Interrupted first-time import | Interrupt an actual Add Shortcut flow and resume it; current pending-state tests use fixtures |
| Fresh permission rejection / locked session | Observe these through the installed plugin on an appropriate account; helper failures and cancellation are tested, but not fresh Apple consent rejection or a deliberately locked live image call |
| Rate-limit behavior | Stopping/error propagation is implemented; no live rate limit was deliberately provoked in this suite |
| Upgrade / rollback | Fixture installations prove preservation and pointer changes; a second released plugin/runtime upgrade is not available yet |
| Final release provenance | Run the prepared GitHub packaging workflow after separate authorization and verify the resulting archive attestation |

Temporary directories and an already-approved user account are not substitutes
for the clean-account row. Other macOS versions, remote Linux execution and
visible-editor automation are outside this release's supported route. These
open checks prevent claiming that every first-time installation condition has
been proven, while preserving the observed unattended successes above.
