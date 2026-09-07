# Hollis review closure — 7 September 2026

All three consolidated findings and all five smaller items are addressed in
source. Claude and Codex still return useful Apple answers and images inside
the conversation. The original runtime corrections shipped in
[0.3.1](https://github.com/kamenxrider/hollis/releases/tag/v0.3.1).
Plugin `0.1.0` now pins released 0.3.2; its
[current package checks](../plugins/hollis/docs/release-0.3.2-package-evidence.json)
are separate from the historical 0.3.1 results below. Earlier archives remain unchanged.

Review baseline: `23f0b80`. [Sanitized verification results](../plugins/hollis/docs/review-evidence.json)
record the pre-release fix verification. [Released package evidence](../plugins/hollis/docs/release-package-evidence.json)
records the later 0.3.1 repin and installation checks. Original reproductions,
worker reports and raw host receipts
are preserved locally under `docs/dev/plugin-review-2026-09-06/` and
`docs/dev/plugin-fixes-2026-09-07/`; those private folders are not distributed.

## Three findings

| Finding and calibration | Correction and disposition | Regression evidence |
|---|---|---|
| **F1: low — status before installation was opaque.** An empty home returned exit 10 and a missing `current` error. | **Fixed.** `status <route>` returns structured `setup_required`, exit 0, without writes, discovery or inference. Access restrictions, damaged state and newer external runtimes remain distinct. `path` still returns an executable path. | [Eight readiness tests](../scripts/plugin/test_readiness.py) |
| **F2: low — rollback availability was not reported during upgrade.** Earlier reviewers suggested medium, but the old runtime was already corrupt and rollback already refused it; no integrity bypass was demonstrated. | **Fixed.** Managed install/check results add `rollback.status`, `version` and `message`. Availability requires a verified receipt and executable. Repair upgrades retain old files; rollback verifies again and never switches to an unavailable candidate. | [Ten recovery tests](../scripts/plugin/test_recovery.py), including nonexecutable previous binaries and damaged current pointers |
| **F3: low — reference errors repeated caller-supplied paths.** This was runtime diagnostic exposure, not demonstrated image transmission. | **Fixed for 0.3.1.** Missing, denied and other read errors are path-free. Validation, exit codes and file protections remain intact. Direct, explicit chat and stored-artifact references are covered. | [Reference tests](../internal/imagegen/reference_test.go), [zero-dispatch CLI tests](../internal/cli/reference_error_test.go), twelve actual process checks in the verification results |

## Five smaller items

| Item | Disposition and evidence |
|---|---|
| **Absolute image links** | Earlier fix retained. Both new host flows returned an absolute Markdown preview and file link without a corrective turn; the [shared skill](../plugins/hollis/skills/hollis/SKILL.md) and [host checklist](../plugins/hollis/docs/validation.md) require it. |
| **ZIP executable permissions** | No shipping defect was established: the old Python-extracted fixture lost permissions. Six [archive tests](../scripts/plugin/test_package_archive.py) now check modes, bytes, five bridges and native `ditto` extraction. Provenance is mocked only in synthetic fixtures. |
| **Old scratch package** | `dist/plugin-test/hollis-plugin-0.1.0.zip` is explicitly **superseded** in the local artifact index; contents retained. Its hash and the separate earlier reviewed archive hash are recorded in the verification results. Neither is the new 0.3.1 bundle. |
| **Stale source version** | Default changed from `0.2.0` to `dev`. [Process tests](../internal/integration/cli_process_test.go) prove source identity and exact linker-injected `0.3.1`, including agent output. Released asset identity is unchanged. |
| **Stored auto preference** | Explicitly documented as no concrete preference: the plugin passes Cloud unless a concrete route is chosen. Runtime configuration compatibility remains unchanged. The new Claude text call observed stored `auto` and requested/used Cloud. |

## Validation completed

- 48 plugin tests, 30 image-harness tests, 22 review-boundary tests and three
  image-bridge tests passed. Manifest/skill validation, shell syntax, both bridge
  generator profiles and archive integrity checks passed.
- Full `go test ./...`, `go test -race ./...` and `go vet ./...` passed from an
  exact source snapshot. The working checkout contains ignored historical Go
  backups that are not build inputs; those were preserved. All 92 current Go
  files match the tested snapshot.
- Actual 0.3.1 process checks covered three filesystem failure classes through
  direct/chat and human/agent output: twelve passes, exit 2, no output images.
- Four Apple calls passed with the local 0.3.1 binary: Claude Cloud, a resumed
  Illustration turn, Codex Cloud and a Sketch referencing the Claude image.
  Both image links worked and the reference hash matched. Calls were serialized,
  with more than five seconds between generations and no retries. Existing
  approvals sufficed; no visible editor was required. Cloud Pro's existing
  45-second pacing is retained; this run did not call Cloud Pro.
- Restricted Shortcuts discovery correctly returned `unknown`; authorized host
  checks discovered both selected bridges. The process-cleanup harness initially
  failed because the sandbox blocks `ps`; the same 30 tests passed on the host.

These prove transport, setup contracts and presentation, not perfect model
quality or exact image identity. Visual inspection confirmed the bicycle,
toolbox and fern in both images, with changed composition. Claude's planning
answer contained ambiguous capacity advice; successful transport does not make
that advice authoritative.

## Remaining release and acceptance boundaries

Runtime 0.3.1 was published with authorization. All five release assets passed
strict GitHub provenance verification, and the new plugin bundle uses the
verified version-and-hash lock. Native package installation, source download,
Rosetta, actual 0.3.0 upgrade/rollback and corrupt-previous recovery passed.
The repinned package also passed a Claude Cloud answer and a Codex Sketch
with an explicit reference and absolute preview/file link, using the released
managed binary. Two Apple calls, no retries or visible editor.
The preserved 0.3.0 archive does **not** contain the runtime privacy fix.
Plugin publication and its outer archive attestation remain separate steps.

A genuinely fresh Mac account and fresh Apple consent remain unproven. So do a
real interrupted first import, fresh Apple permission rejection, a deliberately
locked live image session and a deliberately provoked rate limit. Fixtures and
this already-approved account do not close those [acceptance gaps](../plugins/hollis/docs/validation.md).
