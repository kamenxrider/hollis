# Plugin 0.1.0 validation — updated 7 September 2026

The local review package works in Claude Code and Codex on the tested existing
Mac account. **Full first-time installation acceptance remains open:** this run
did not have a clean macOS account with fresh Apple approvals. Public publication
and the GitHub build that attests the outer plugin archive are also pending.

The [recorded demonstration](../examples/recorded-demo.md) shows the useful
Apple contribution first. [Sanitized results](../examples/recorded/results.json)
retain actual answers, timestamps and image checksums. Private raw receipts
remain in the maintainer workspace; no credentials or host settings are packaged.

## Release acceptance checklist

- [x] The retained v0.1.0 archive has a fixed checksum, regular entries, the
  ARM64 runtime and all five bridge names. A provider-free fixture also checks
  archive bytes and modes, and macOS `ditto` extraction checks executable bits.
- [x] Existing live receipts and the recorded example disclose model routes,
  image references, absolute image links and the limits of an already-approved
  account.
- [x] Post-fix integration passed all 48 plugin tests, full Go tests/race/vet,
  twelve actual reference-error process checks and four serialized Apple calls
  against a local 0.3.1 build. [Sanitized results](review-evidence.json).
- [x] Claude and Codex returned generated-image previews and working absolute
  Markdown file links; Codex attached the prior Claude image explicitly.
- [ ] A genuinely fresh supported Mac account installs the package, completes
  Add Shortcut/Allow decisions, and repeats one text and one image request.
- [ ] A separately authorized release publishes runtime 0.3.1 and verifies its
  provenance before any lock or package pin changes. Plugin 0.1.0 remains
  unpublished while the official lock stays on runtime 0.3.0.

## Local 0.3.1 verification — 7 September

The corrected plugin source used a local 0.3.1 executable through the supported
newer-external-runtime path. The official 0.3.0 lock and earlier archive were
preserved. This proves the corrected source/runtime combination; it is not a
GitHub-attested 0.3.1 package.

| Host flow | Observed outcome | Elapsed host turn |
|---|---|---|
| Claude Cloud | Stored `auto` resolved to requested/used Cloud; repair-cafe plan returned inline | 64.403 s |
| Claude resumed image turn | Illustration from conversation context; absolute preview/file link | 27.094 s |
| Codex Cloud + reference image | Invitation inline, then Sketch with matching reference SHA-256; absolute preview/file link | 104.716 s |

Four successful Apple calls, including two images, were serialized with no
retries. More than five seconds separated generations. The account already had
Apple approvals; no recurring click or visible Image Playground editor was
required. Both images contain the bicycle, toolbox and fern, with changed
composition. Model answers still require judgment: the Cloud plan left capacity
and volunteer allocation ambiguous. These checks do not prove exact image
identity or universal model quality.

The 0.3.1 error checks covered missing, denied and other filesystem failures in
human/agent output for direct generation and conversation references. All twelve
processes returned exit 2 with path-free errors and no image output. Injected
Go tests separately assert zero generator calls on invalid references.

Full Go tests, race tests and vet passed from an exact source snapshot excluding
preserved historical development backups; all 92 current Go files match it.
Thirty image-harness tests passed on the host after the sandbox blocked process
inventory (`ps`). This access limitation and the successful host rerun are both
retained. The 22 review-boundary and three image-bridge tests also passed.

## Original 0.3.0 live behavior — 6 September

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
- A focused package test builds a synthetic asset fixture through the real
  packager (the provenance verifier is mocked only inside that test), checks
  exact source/runtime membership and modes, and uses macOS `ditto` to compare
  extracted bytes and all five bridges. The retained ZIP/hash is never written.
- The 7 September integration run passed **48 provider-free plugin tests**:
  the original 24 plus eight readiness, ten recovery and six archive tests.
  Native manifest/skill validation, shell syntax and both bridge profiles pass.
- The original 24 tests cover: corruption, archive traversal,
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
