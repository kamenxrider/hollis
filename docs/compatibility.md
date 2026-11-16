# Runtime compatibility and troubleshooting

This guide covers the Hollis CLI and Shortcuts transport. The [plugin compatibility guide](../plugins/hollis/docs/compatibility.md) adds its own installation and host requirements; its supported setup is narrower than the runtime's experimental paths.

## What you need

You need an **Apple-silicon Mac** with **Apple Intelligence** enabled, macOS 27 for the measured setup, and `/usr/bin/shortcuts` (included with macOS). Cloud, Cloud Pro and ChatGPT need network access; On-Device works offline. For the ChatGPT bridge, enable the extension in *System Settings → Apple Intelligence & Siri*.

## Compatibility

**macOS 27: historical Shortcuts checks.** On `26A5421a` and `26A5425a` the Shortcuts selector exposed Cloud, Cloud Pro, On-Device and ChatGPT, and all four bridges returned responses in those tests. The 0.4.0 candidate was also checked on `26A428`. On 2026-10-31, Cloud and Cloud Pro each succeeded once on the merged runtime; this does not requalify every bridge or guarantee continuing availability.

**macOS 27.2: native local checked 2026-11-01.** Apple Silicon, build `26B123`,
SDK 27.0, helper 0.5.0/protocol 2 at that time: one plain complete call and
one guided JSON call succeeded, with measured usage. Diagnostics reported an
8,192-token context window. See the [qualification scope](../EVIDENCE.md#dated-qualification-summary);
runtime 0.4.0 does not include these features.

**macOS 26: experimental, untested.** macOS 26 Shortcuts exposed three model locations: Cloud, On-Device and ChatGPT. There was no Cloud Pro, and hollis refuses `cloud-pro` when that bridge is unavailable. The macOS 26 `cloud` choice is the earlier PCC model generation, not the 27 Cloud / Cloud Pro pair.

```bash
python3 scripts/make-bridge.py --os 26 bridges/
hollis doctor
hollis respond --model cloud "Reply with OK"
```

Include `hollis doctor` output when reporting macOS 26 test results.

## Native local

The explicit `local` route requires Apple Silicon and macOS 27. It uses
the matched precompiled helper beside Hollis and does not require Shortcuts.
A missing helper or unavailable Apple model is reported separately from bridge
readiness. Native local on macOS 26 and Intel is unsupported. The published `hollis-darwin-amd64` download is a legacy CLI artifact; Intel Macs
do not support Apple Intelligence inference. The primary installer selects the
ARM64 bundle. A source-only Go install does not install the Swift helper; use the [native-local guide](native-local.md).

Hollis 0.7.1 uses helper 0.7.1 and protocol 4. CLI and helper always ship as a
matched bundle and must have exactly the same version. A mismatched pair
fails closed before generation, with a clear error asking you to install the
matching bundle (exit 5, HTTP 502 `native_protocol`). The helper accepts request
formats 1, 2, 3 and 4 but stamps every event with 0.7.1; this does not make older
CLIs compatible.

| CLI | Helper | Result |
| --- | --- | --- |
| 0.4.0 / protocol 1 | 0.7.1 | Old decoder rejects the version |
| 0.5.0 / protocol 2 | 0.7.1 | Old decoder rejects the version |
| 0.7.1 / protocol 4 | Matching 0.7.1 | Negotiates protocol 4; capabilities checked per request |
| 0.7.1 / protocol 4 | Older or mismatched helper | Fails closed; install the matching bundle |
| 0.7.1 native tools | 0.6.0 / protocol 3 | Status preflight rejects the old version before generation dispatch |

Native tools require both `tools.executable` and `tools.turn_handoff`.
Requests use `--native-tools`, which old helpers reject before generation.
Metadata-only advertisement does not qualify, even when the product version
matches. None disables callbacks without extracting text calls.

Protocol-format acceptance is tested separately from CLI compatibility: protocol
1 omits context size and structured output, protocol 2 adds them, and protocol
3 adds capability advertisement. Status checks and decoder fixtures do not
establish successful inference.

## Doctor

The example below is version 0.3.0 output with its `MISSING` line updated to current wording, which points at [adding the Shortcuts](../README.md#add-shortcuts). Current output also adds a `local:` line for native availability.

```bash
hollis doctor
hollis doctor --json
```

```text
$ hollis doctor
hollis doctor (version 0.3.0)
  transport: ok
  macos: 27.0 (26A5421a)
  support: macOS 27 measured; Cloud Pro unsupported on macOS 26
  timeout default: 30s (ceiling 120s)
  bridges (resolved at runtime):
    [OK]        cloud      AFM Bridge - Cloud.signed (shortcuts-list)
    [MISSING]   cloud-pro  00000000-0000-4000-8000-000000000002 (compiled-uuid)
    [OK]        on-device  AFM Bridge - On-Device.signed (shortcuts-list)
    [OK]        chatgpt    AFM Bridge - ChatGPT.signed (shortcuts-list)

  MISSING: add that bridge (README.md#add-shortcuts), or point config at one you already have:
  hollis config set bridge <tier> <name-or-uuid>
```

Unscoped `doctor` is an overview and exits 0 even with missing bridges or failed
discovery. This is not a readiness gate. Its JSON reports `overview: true`,
`readiness: "not_assessed"` and a `diagnostics` array, also under `--select`.
Use [selected-route diagnostics](selected-route-setup.md) with `--model <route>`
for a nonzero exit when that route is unready. Cloud Pro is informationally
unsupported on macOS 26. An explicit configured reference may still be
attempted, but remains labelled unverified until its name is visible in discovery.

JSON output adds the macOS version and build, each bridge's `resolved_ref`, its resolution `source`, verification state, and `status` (`ok`, `missing`, `unsupported`, or `unverified`). The `uuid` field is the compiled placeholder, not the installed shortcut's own identifier: `shortcuts list` reports names only, so Hollis never learns a workflow ID from your Mac. Do not copy that placeholder into config expecting it to run something.

With protocol 2 and above, native availability results in `doctor` and
`models` also report the measured `context_size` when available. The observed
8,192 tokens are not a fixed promise for every model/build or a replacement for
the separate 128 KiB rendered-prompt limit. Availability is not inference proof.

## If something breaks

- `doctor` says `MISSING`: add the bridges as described in [add the Shortcuts routes](../README.md#add-shortcuts). If you renamed one in Shortcuts.app, run `hollis config set bridge <tier> "new name"`.
- A command seems missing after `git pull`: rebuild it with `go build -o "$(go env GOPATH)/bin/hollis" ./cmd/hollis`; an older binary may still be first on `PATH`.
- An OpenAI client fails with `stream: true`: select native `local` for streaming, or set `stream: false` for a Shortcuts route. Hollis does not read a streaming preference from custom headers.

## Other install routes

```bash
git clone https://github.com/kamenxrider/hollis.git
cd hollis
go build -o "$(go env GOPATH)/bin/hollis" ./cmd/hollis        # needs Go 1.27+
```

From a clone, `python3 scripts/package-bridges.py dist` creates the complete nine-bridge archive (four text, four JSON and one image). `python3 scripts/make-bridge.py bridges/` generates just the four model bridges.

The module's local SDK replacement requires this clone-and-build route rather
than `go install …@latest`. This builds only the CLI, not the native helper.

Release binaries and Shortcut files are **unsigned by a Developer ID and not notarized**. Verify the downloaded checksum and attestation using the [manual verification steps](../README.md#verification-and-trust); releases also carry a GitHub build-provenance attestation and an SPDX SBOM. For the smallest trust chain, inspect the source and build it yourself with the commands above. Hollis does not claim Gatekeeper approval. If a command seems to be missing after a `git pull`, rebuild: an older binary on `PATH` is usually the cause.

[Back to Hollis](../README.md)
