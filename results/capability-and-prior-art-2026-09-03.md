# Capability and prior-art check — 2026-09-03

This note separates what Apple documents for its developer framework from what
Hollis has measured through the user-facing Shortcuts transport. Those are
different product surfaces and must not be presented as interchangeable.

## Product claim

The defensible claim is:

> On tested macOS 27 builds, Hollis exposes the distinct **Cloud** and **Cloud
> Pro** choices from Shortcuts to ordinary CLI and local-API users. To our
> knowledge, after the searches below, it is the first public CLI to expose both
> choices separately.

This is not a claim that Hollis invented calling Apple Intelligence from a
Shortcut. Joseph Humfrey publicly demonstrated that technique in June 2025.
It is also not a claim that Hollis is the first Apple Foundation Models CLI;
many on-device and generic-PCC tools predate or overlap it.

## Evidence matrix

| Surface | Evidence | Cloud choices | Streaming | Tools / structured output |
| --- | --- | --- | --- | --- |
| Hollis Shortcuts transport, macOS 27 build `26A5421a` | Picker capture, exported payloads, and live four-tier tests | Separate Cloud and Cloud Pro | Complete text only | No native tool channel observed |
| Hollis Shortcuts transport, macOS 27 build `26A5425a` | Live model and prompt-protocol probes | Separate Cloud and Cloud Pro | Complete text only | Prompt-defined calls and supplied tool results worked in some probes; call emission was inconsistent |
| Apple `fm`, build `26A5421a` | Local `fm --help` and `fm available` capture | `system` only; `pcc` rejected | Native `fm` behavior, not a Hollis capability | Different transport; not inferred for Hollis |
| Foundation Models framework, macOS 27 | Apple documentation and WWDC26 sessions | One entitlement-gated `PrivateCloudComputeLanguageModel` API | Snapshot streaming | Native Generable output and tool calling; 32K PCC context documented |
| Shortcuts on macOS 26 | Apple support plus historical research | One PCC choice, On-Device, ChatGPT; no Cloud Pro | Unknown here | Unknown here; profile remains untested on a 26 Mac |

Apple's AFM3 announcement names **AFM 3 Cloud** and **AFM 3 Cloud Pro** as
separate server models. It describes Cloud as the server workhorse and Cloud Pro
as its most capable server model for demanding reasoning and agentic tool use.
The Foundation Models developer API is not the same selector: Apple documents a
`PrivateCloudComputeLanguageModel`, a 32K context, reasoning levels, native
Generable output, and native tool calling. Access requires the PCC entitlement,
App Store Small Business Program membership, and fewer than two million first
downloads.

Hollis does not use that entitled API. It invokes imported Shortcuts whose **Use
Model** action selects `Apple Intelligence` or `Apple Intelligence Pro`. That
surface currently returns completed text, so Hollis must not advertise the
framework's streaming or native tool channel as its own.

## Prior-art search

Searches run on 2026-09-03:

- General web: `"Apple Intelligence Pro" CLI Shortcuts "Cloud Pro" GitHub`,
  `"WFLLMModel" "Apple Intelligence Pro"`, and `"Cloud Pro" "shortcuts run"`.
- GitHub repository search: `"Apple Intelligence Pro" CLI`, `"Cloud Pro"
  "Apple Intelligence"`, `"Apple Intelligence" Shortcuts CLI`, and `apple
  foundation models CLI macOS`.
- GitHub code search: exact `Apple Intelligence Pro`, exact `WFLLMModel` plus
  `Apple Intelligence Pro`, `Cloud Pro` plus `shortcuts run`, and language-
  specific Swift and Go searches.

The exact Shortcut payload search found Hollis and Apple's localized/private-
framework strings, but no second CLI implementation. The broad searches found
several important counterexamples to any larger “first Apple CLI” claim:

| Project | Transport / scope | Why it does not match the two-tier claim |
| --- | --- | --- |
| Joseph Humfrey's PCC Shortcut article | Shortcut plus `/usr/bin/shortcuts` | Establishes the bridge technique; one generic PCC choice in 2025 |
| TwoMillionKit | Apple `fm --model pcc` | Generic `pcc`, not separate Cloud and Cloud Pro |
| `fm-proxy` and `fm-server` | Apple `fm` / `fm serve` | Expose `system` and one `pcc` model |
| Foundation Models Framework CLI (`afm`) | Foundation Models framework and an entitled signed host | Exposes on-device and generic `pcc`, with a different entitlement boundary |
| `apple-intelligence-cli`, `askai`, `fmx`, and similar tools | Foundation Models framework or `fm` | Primarily on-device, or one generic PCC target |
| Shortcut authoring/decompiling CLIs | Create or inspect arbitrary Shortcuts | General Shortcut tooling, not a model-serving CLI with both tiers |

Search engines and GitHub indexes are not proofs of nonexistence. The “to our
knowledge” qualifier is mandatory. The claim must be removed or narrowed if a
prior public CLI that separately selects both Shortcuts tiers is found.

## Capability implications

1. `v0.2.0` should remain honest completed-text transport: no fake streaming,
   invented token usage, or native-tool claim.
2. Capability output should report evidence per transport and OS build, rather
   than copying features from Apple's developer framework.
3. Structured output over Shortcuts can be locally validated, but is
   prompt-guided rather than native guided generation.
4. A client-executed tool loop is technically plausible: models have emitted a
   JSON call and consumed unique supplied tool results. Because call emission
   was inconsistent and PCC rate limits interrupted longer probes, it belongs
   behind an experimental opt-in and Hollis must never execute the tools.
5. Native Foundation Models streaming/tools and Shortcuts Cloud/Cloud Pro should
   remain separate future transports even if they share higher-level schemas.

## Sources

- [Apple: Introducing the Third Generation of Apple’s Foundation Models](https://machinelearning.apple.com/research/introducing-third-generation-of-apple-foundation-models)
- [Apple: Foundation Models framework](https://developer.apple.com/documentation/foundationmodels)
- [Apple WWDC26: What’s new in the Foundation Models framework](https://developer.apple.com/videos/play/wwdc2026/241/)
- [Apple WWDC26: Build with the new Apple Foundation Model on Private Cloud Compute](https://developer.apple.com/videos/play/wwdc2026/319/)
- [Apple: Accessing Private Cloud Compute](https://developer.apple.com/private-cloud-compute/)
- [Apple Support: Run shortcuts from the command line](https://support.apple.com/guide/shortcuts-mac/run-shortcuts-from-the-command-line-apd455c82f02/mac)
- [Joseph Humfrey: The Shortcut to integrating Private Cloud Compute into my app](https://joethephish.me/blog/the-shortcut-to-integrating-PCC/)
- [TwoMillionKit](https://github.com/insidegui/TwoMillionKit)
- [`fm-proxy`](https://github.com/gregbarbosa/fm-proxy)
- [`fm-server`](https://github.com/tariqwest/fm-server)
- [Foundation Models Framework CLI (`afm`)](https://github.com/rudrankriyam/Foundation-Models-Framework-CLI)
- Hollis local evidence: [`two-cloud-tiers-26A5421a.md`](two-cloud-tiers-26A5421a.md), [`transport-and-persistence-2026-09-01.md`](transport-and-persistence-2026-09-01.md), and the local-only `tool-protocol-26A5425a.md` probe log.
