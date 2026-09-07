# Plugin 0.1.0 validation — updated 7 September 2026

Plugin 0.1.0 bundles released Hollis 0.3.2 and works in Claude Code and Codex on
the tested existing Mac account. **Full first-time installation acceptance
remains open:** these checks did not use a clean macOS account with fresh Apple
approvals. Historical checks below retain their original runtime versions.

The [recorded demonstration](../examples/recorded-demo.md) shows the useful
Apple contribution first. [Sanitized results](../examples/recorded/results.json)
retain actual answers, timestamps and image checksums. Private raw receipts
remain in the maintainer workspace; no credentials or host settings are packaged.

## Released-runtime 0.3.2 package verification — 7 September

All five 0.3.2 release assets passed checksum and strict GitHub provenance
verification against commit `7c2b384`. The plugin packager independently verified
the bundled ARM64 executable and five-bridge ZIP before building its archive.
[Package evidence](release-0.3.2-package-evidence.json) records the runtime,
tested package contents and preserved older archives separately.

The actual archive passed native macOS `ditto` extraction and byte/mode checks.
Fourteen installer operations passed with only macOS utilities on the installer
PATH: empty status, bundled/repeated install, version and hash, Rosetta,
0.3.1-to-0.3.2 upgrade and rollback, recovery over a corrupt old runtime, refused
rollback preserving the healthy runtime, state preservation and source downloads.
All 51 plugin regressions, manifest validation and shell syntax checks passed.

| Packaged host flow | Observed result | Measured time |
|---|---|---|
| Claude Code | Seed-swap checklist inline; stored `auto` selected Cloud explicitly | 145.211 s for the whole host turn, including setup checks |
| Codex desktop | 1024×1024 Sketch of a lighthouse, mossy island and yellow sailboat; image inspected and absolute Open image link emitted | 7.742 s for generation |

Exactly two Apple calls ran serially with at least ten seconds of separation,
no retries and no tier substitution. Both used the verified managed 0.3.2 binary
and isolated `HOLLIS_STATE_DIR`. Existing bridges and Apple approvals were reused;
no visible Playground automation was used. These are representative release
checks, not a repeat of the full 44-call matrix or fresh-account proof.

The local acceptance ZIP has its own recorded hash. The GitHub release workflow
rebuilds and attests the distributed ZIP; its scripts, skills, manifests, lock
and runtime bytes must match the tested core. Documentation and freshly verified
provenance receipts can differ. Earlier archives and receipts remain unchanged.

## Historical 0.3.1 release acceptance checklist

For the later local 0.3.2 source/runtime results, see the separate section
below. The retained archive and the historical checks here remain 0.3.1-backed.

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
- [x] Runtime 0.3.1 is published. All five release assets passed checksum and
  strict GitHub provenance checks before repinning and packaging. The plugin
  remains unpublished; its outer archive attestation is still pending.

## Local 0.3.2 source verification — 7 September

The existing Mac account ran 36 planned calls and eight registered diagnostics:
40 completed outputs and four `request_declined` failures. All calls were
serialized, with at least 10 seconds after completion and 45 seconds before
Cloud Pro. No automatic retry, rate limit, timeout or uncertain completion occurred.

| Condition | Claude Code | Codex |
|---|---|---|
| Text: Cloud, Cloud Pro, On-Device, ChatGPT | 4/4 correct budget answers | 4/4 correct budget answers |
| Understanding: Cloud, Cloud Pro, ChatGPT | 3/3 correct red-left/blue-right answers | 3/3 correct red-left/blue-right answers |
| Animation, Illustration, Sketch, Genmoji | 4/4 PNGs | 4/4 PNGs |
| Any Style coffee photo prompt | Declined | Declined |
| Two historical cases × reference present/absent × three repetitions | Not assigned | 12/12 PNGs; similarity attributable to the prompt remains a confound |

Two Any Style lighthouse controls also returned stylized PNGs. Replacing only
the coffee prompt's opening “A photograph of” with “An image of” was declined
twice. Thus Any Style remains usable, while these photo conditions failed for
an unknown reason. Three source-omission controls returned unrelated subjects:
the presence of reference bytes does not establish reliable visual editing.
One final Cloud smoke check passed against the final build.

Claude Code loaded the source plugin through `--plugin-dir` and invoked the
same helper used by the Codex host. A scoped test launcher
identified the local binary and isolated state explicitly. This proves the
source/helper integration; it is not a new installed-archive or fresh-account
test. The plugin lock and retained archive remain on verified 0.3.1.

The updated `status` reports `runtime_path`, binary-resolved `config_path` and
structured discovery status. An actual sandboxed invocation returned unknown
with discovery exit 1 and the correct isolated path; an invocation with host
access completed discovery. An exit code cannot establish whether sandbox, helper or
session access was the cause.

All 50 plugin regressions passed, including config-read failure and successful
empty versus failed discovery. Full Go tests/race/vet, existing package integrity
and macOS extraction tests, four journal/pacing tests, manifest validation and
shell syntax checks passed. New subprocess and API tests cover specific error
codes, path-free public diagnostics and bounded model-call counts.

Both hosts emitted absolute previews and `Open image` links. Claude inspected
its four PNGs through its file tool; Codex inspected its results and sent a
preview in the conversation. Agent inspection and emitted Markdown are not
proof of what the user's host rendered; user-visible inline rendering remains
unconfirmed. No Image Playground automation was used, and no generation click
was requested by either host. Desktop visibility was not independently recorded.
Fresh-account setup and first Apple consent remain untested.

## Released 0.3.1 package verification — 7 September

The plugin now pins the published runtime at commit `19a7970`, after all five
release assets passed strict GitHub provenance and checksum verification.
The packager independently reverified its bundled binary and bridge ZIP.
[Sanitized package evidence](release-package-evidence.json) preserves these
checks separately from the earlier local-build and 0.3.0 recordings.

The actual ZIP was extracted with macOS `ditto`. Fourteen installer operations
passed using only macOS utilities on the installer PATH: empty/read-only status,
bundled and repeated installation, binary version/hash and executable modes,
Rosetta, real released 0.3.0-to-0.3.1 upgrade and rollback, recovery over a corrupt
old binary, refused rollback preserving 0.3.1, state preservation and actual
locked source downloads. These used isolated storage on the existing account,
not a genuinely fresh account. The 48 plugin regressions also passed after repinning.

| Packaged host flow | Observed result | Elapsed host turn |
|---|---|---|
| Claude Cloud | Seed-swap checklist returned inline; stored `auto` selected Cloud explicitly | 47.664 s |
| Codex Sketch | Prior image explicitly attached; 1024×1024 PNG, absolute preview and file link | 75.463 s |

Both used the verified managed release binary, with external runtime discovery
excluded. Two Apple calls ran serially, with one image and no automatic retries.
Codex first reported restricted Shortcuts discovery as unknown, then successfully
repeated that read-only check with authorized host access before generating.
Existing Apple approvals sufficed; no visible Image Playground editor or new
Apple click was required. Visual review found the blue bicycle, red toolbox and
green fern in the Sketch. The prompt also named these subjects, so this is proof
of generation with an attachment, not of the generator using its pixels. Later
0.3.2 controls left reliable reference editing unproven; see
[reference evidence](compatibility.md#image-reference-evidence). Cloud Pro was not called
in this recheck; its pacing is unchanged.

The final archive includes this report. Its scripts, skills, manifests, lock
and runtime bytes must match the tested package; documentation and freshly
verified provenance receipts may differ. Earlier ZIPs and raw receipts are retained.
The plugin remains unpublished and its outer GitHub attestation remains pending.

## Local 0.3.1 verification — 7 September

The corrected plugin source used a local 0.3.1 executable through the supported
newer-external-runtime path. At that stage the official lock and earlier archive
remained on 0.3.0. These historical receipts prove the corrected local
source/runtime combination. The subsequent released-asset package verification
is recorded separately above.

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
  provenance and their locked release commit (originally v0.3.0, now v0.3.1). Local hash checks occur
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
| Upgrade / rollback | Actual released 0.3.0 and 0.3.1 assets now pass upgrade, rollback and corrupt-previous recovery in isolated storage; an existing end-user installation migration remains unobserved |
| Final release provenance | Run the prepared GitHub packaging workflow after separate authorization and verify the resulting archive attestation |

Temporary directories and an already-approved user account are not substitutes
for the clean-account row. Other macOS versions, remote Linux execution and
visible-editor automation are outside this release's supported route. These
open checks prevent claiming that every first-time installation condition has
been proven, while preserving the observed unattended successes above.
