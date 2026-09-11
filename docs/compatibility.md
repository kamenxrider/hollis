# Runtime compatibility and troubleshooting

This guide covers the Hollis CLI and Shortcuts transport. The [plugin compatibility guide](../plugins/hollis/docs/compatibility.md) adds its own installation and host requirements; its supported setup is narrower than the runtime's experimental paths.

## What you need

A Mac with **Apple Intelligence** enabled, macOS 27 for the measured setup, and `/usr/bin/shortcuts` (included with macOS). Cloud, Cloud Pro and ChatGPT need network access; On-Device works offline. For the ChatGPT bridge, enable the extension in *System Settings → Apple Intelligence & Siri*.

## Compatibility

**macOS 27 — measured.** On `26A5421a` and `26A5425a` the Shortcuts selector exposes Cloud, Cloud Pro, On-Device and ChatGPT, and all four bridges work.

**macOS 26 — experimental, untested.** macOS 26 Shortcuts exposed three model locations: Cloud, On-Device and ChatGPT. There was no Cloud Pro, and hollis refuses `cloud-pro` when that bridge is unavailable. The macOS 26 `cloud` choice is the earlier PCC model generation, not the 27 Cloud / Cloud Pro pair.

```bash
python3 scripts/make-bridge.py --os 26 bridges/
hollis doctor
hollis respond --model cloud "Reply with OK"
```

Please include `hollis doctor` output when reporting macOS 26 results — that is the fastest way for this to stop being untested.

## Native local

The explicit `local` route in 0.4.0 requires Apple Silicon and macOS 27. It uses
the matched precompiled helper beside Hollis and does not require Shortcuts.
A missing helper or unavailable Apple model is reported separately from bridge
readiness. Native local on macOS 26 and Intel is unsupported. A source-only Go
install does not install the Swift helper; use the [native-local guide](native-local.md).

## Doctor

The example below uses version 0.3.0. Its “Quickstart” instruction refers to [verified install step 2](../README.md#install-verified).

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
    [MISSING]   cloud-pro  DBB6E472-CBC6-4421-8D32-9D4543D5CDE6 (compiled-uuid)
    [OK]        on-device  AFM Bridge - On-Device.signed (shortcuts-list)
    [OK]        chatgpt    AFM Bridge - ChatGPT.signed (shortcuts-list)

  MISSING: install that bridge (README “Quickstart” step 2), or point config at one you already have:
  hollis config set bridge <tier> <name-or-uuid>
```

`doctor` exits 0 only when every tier supported by the detected macOS version is verified through `shortcuts list`. A missing supported bridge exits 3, discovery or transport failure exits 5, and unknown or unverified state exits 10. Cloud Pro is informationally unsupported on macOS 26. An explicit configured reference may still be attempted, but remains labelled unverified until its name is visible in discovery.

JSON output adds the macOS version and build, each bridge's `resolved_ref`, its resolution `source`, verification state, and `status` (`ok`, `missing`, `unsupported`, or `unverified`).

## If something breaks

- `doctor` says `MISSING`: install the bridges from [verified install step 2](../README.md#install-verified). If you renamed one in Shortcuts.app, run `hollis config set bridge <tier> "new name"`.
- A command seems missing after `git pull`: rebuild it with `go build -o "$(go env GOPATH)/bin/hollis" ./cmd/hollis`; an older binary may still be first on `PATH`.
- An OpenAI client fails with `stream: true`: select native `local` for streaming, or set `stream: false` for a Shortcuts route. Hollis does not read a streaming preference from custom headers.

## Other install routes

```bash
go install github.com/kamenxrider/hollis/cmd/hollis@latest   # needs Go 1.27+
go build -o "$(go env GOPATH)/bin/hollis" ./cmd/hollis        # from a clone
```

From a clone, `python3 scripts/package-bridges.py dist` creates the complete five-bridge archive. `python3 scripts/make-bridge.py bridges/` generates just the four model bridges.

Release binaries and Shortcut files are **unsigned by a Developer ID and not notarized**. Verify the downloaded checksum using the [verified install](../README.md#install-verified); releases also carry a GitHub build-provenance attestation and an SPDX SBOM. For the smallest trust chain, inspect the source and build it yourself with the second command above. Hollis does not claim Gatekeeper approval. If a command seems to be missing after a `git pull`, rebuild: an older binary on `PATH` is usually the cause.

[Back to Hollis](../README.md)
