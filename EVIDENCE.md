# Evidence and prior art

This note separates what Apple documents for its developer framework from what
Hollis has measured through the user-facing Shortcuts transport. Those are
different product surfaces and must not be presented as interchangeable.

## Agent plugin evidence

The retained plugin 0.1.0 package pins verified Hollis 0.3.1. Its original
[recorded gstack/Claude and Codex demonstration](plugins/hollis/examples/recorded-demo.md)
contains actual Apple answers and generated images. The
[validation report](docs/plugin-validation.md) follows the original 26 successful
live Apple invocations and subsequent regression/package checks, leaving fresh
Mac-account installation and the separate release attestation unproven/pending.
This evidence does not expand the model or privacy claims below.

The subsequent [local 0.3.2 validation](docs/releases/v0.3.2.md#validation)
records 36 planned calls plus eight diagnostics, with 40 completed outputs and
four declined requests. It preserves the existing package and adds narrower
failure codes and more informative plugin status. Reference-free controls
also matched the fully described scenes, while subject-omission controls did
not recover their attached subjects. Reliable pixel-guided editing remains
unproven; generation completion alone does not establish reference use.

## Evidence storage and sharing

Git contains the [test harnesses](scripts/README.md), selected measurements in
`results/`, and sanitized reports such as the
[0.3.2 per-call record](docs/releases/v0.3.2-validation.json). The ignored
`docs/dev/` tree holds local research, generated originals, receipts and host
captures. Ignoring a file keeps it out of a clone; it does not back it up.
Local evidence paths are not public downloads, and these reports do not
establish that an independent backup or public raw-evidence archive exists.

Preserve raw originals, including failures and superseded runs, in a private
off-device backup. Record file checksums and verify a restore before claiming
recoverability. Keep redacted reports and compressed display images as separate
derivatives so the original receipts and image hashes remain meaningful.

A public release evidence bundle is a separate, reviewed export for one test
round, not a copy of the entire private tree. Include the shareable prompts,
fixtures, case manifest, outcomes (including failures), timing, runtime/source
identities, environment versions and checksums needed to examine the claims.
Document redactions and unavailable inputs. Exclude credentials, configuration
and conversation databases, unrelated files and unreviewed host transcripts.

After separate publication authorization, that bundle can accompany the runtime
release as an asset, with its checksum and a direct link from the release notes.
Until an upload is verified, describe it as planned. A public export complements
the private backup; it does not replace it. Re-running the harness reproduces the
protocol, not necessarily the provider's answers or generated pixels.

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

## Measured product evidence

![The Use Model action on macOS 27.0 (26A5421a), showing Cloud, Cloud Pro, On-Device and ChatGPT](results/img/use-model-picker-26A5421a.png)

On macOS 27.0 build `26A5421a`, the Shortcuts **Use Model** picker exposed
Cloud, Cloud Pro, On-Device and ChatGPT. On the same Mac, `fm --help` listed
only `system`, while `fm available --model pcc` rejected `pcc` during argument
validation. The imported Hollis bridges successfully invoked all four
Shortcuts choices.

Direct transport tests on that build established the rules Hollis now enforces:

- prompts travel through standard input and output must be requested as
  `public.plain-text`;
- attaching `shortcuts run` directly to a terminal can produce empty output;
- an empty prompt can wait indefinitely, so Hollis rejects it before launch;
- separate model calls are stateless, while replaying a stored transcript
  preserves a conversation;
- exit 0 with empty output is not treated as a successful response.
- repeated `--input-path` co-delivers a prompt file plus multiple images to Cloud/Cloud Pro and one image to ChatGPT; the tested On-Device Shortcut did not consume the pixels.

The `v0.2.0` gate ran on macOS 27.0 build `26A5425a`. It covered the complete
CLI, configuration and chat lifecycle, authentication, both HTTP model
endpoints, method and input errors, model identity, and clean shutdown. All six
planned Cloud Pro calls completed serially without retry: CLI response, new
chat, continued chat, agent output, Chat Completions and Responses. Its test
conversation, temporary state and server process were removed afterward.

The repository keeps the provider-free regression suite as the living proof of
these contracts. The detailed live record is retained outside the source tree
for attachment to the `v0.2.0` release.

## v0.3.0 validation

All live observations below are from an already authorized Mac on macOS 27.0
build `26A5425a`. They establish this configuration, not first-install success
on another Mac or a particular private backend identity.

| Capability | Observed result | Boundary |
| --- | --- | --- |
| Instruction/document files | Passed on On-Device, Cloud, Cloud Pro and ChatGPT | UTF-8 text and Markdown; no PDF |
| Text batches and resume | Passed on all four tiers | Completed jobs resumed without extra calls |
| API PNG/JPEG understanding | Passed on Cloud, Cloud Pro and ChatGPT, across both API formats | No On-Device image input |
| Image batches | Passed on the three online tiers | One fixture per tier; no additional call on resume |
| Image generation | Animation, Illustration, Sketch, Genmoji and Any Style returned PNGs | Generic refusals also occurred; style names are requested settings, not backend attestations |
| Image conversation delivery | Final nine-image sequence retained subjects and applied scene changes across interactive chat and both APIs | Text also described the subjects; this does not establish reference-pixel use |

Document, API-understanding and batch smoke tests made 21 live requests, all
matching the expected fixtures. Image qualification retained 52 valid PNGs
across diagnostic and acceptance runs; earlier images included visual failures.
Calls were serialized with at least three seconds between completion and the
next generation. These counts describe separate test phases, not a success-rate
benchmark.

### Reference correction

A later zero-generation diagnostic found a missing connection between Base64
Decode and Get Images in the generated reference Shortcut. Earlier checks of
`reference_image_sent` and checksums proved Hollis sent the bytes, but did not
prove the Shortcut delivered them to Photo. Those runs cannot establish that
the model used the reference pixels.

The generator now explicitly connects Decode → Get Images → Photo. The isolated
corrected bridge returned the supplied image in an echo control. That proves
byte delivery through that tested bridge. A subsequent generation had
recognizable composition, but its prompt also described the scene; this does
not establish that the generator used reference pixels. The regression test
checks the UUID connection; packaging tests inspect the actual bridge inside
the release archive. Neither check proves model conditioning.

The 0.3.2 controls strengthen this distinction: fully described scenes appeared
with and without references, while prompts that omitted the subject did not
recover either attached subject. A café reference produced a man in a cap;
no reference produced a man in a hat; the turtle reference produced sunflowers
in a blue jug. These were one call per subject-omission condition, not identical
images or proof of impossibility. Reliable reference editing remains unproven.
The installed bridge's current action graph could not be read in that round,
so the observations do not isolate the failing layer. See
[image references](docs/image-references.md) for the controls and call records.

The assembled 0.3.0 binary then passed four new live image calls using the
installed corrected diagnostic bridge. Its complete workflow matches the
packaged generator after normalizing UUIDs. A CLI chat created a red sailboat,
then changed the scene to snowy hills at sunset using the prior image. Both API
conversation formats also accepted a replayed image and returned the requested
variation. All four PNGs were decoded, checksum-checked and visually reviewed;
no UI clicks were supplied. Calls had at least five seconds between completions
and subsequent starts. One malformed test replay omitted required text; the API
rejected it with HTTP400 before inference and corrected requests passed. The
temporary authenticated API server was stopped.

A fifth live check extracted the image Shortcut from the actual five-bridge
release ZIP, signed it on the test Mac, and imported it under a separate
release-check name. It generated the requested reference variation in 30.24
seconds with no UI interactions during generation. The PNG decoded correctly,
its checksum matched, and visual review confirmed the red sailboat, cream sail,
jetty, snowy hills and orange sunset. Import required the normal Add Shortcut
step. The existing user's bridge was not replaced. These five successful calls
qualify the packaged workflow on this Mac, not first-ever setup on a fresh Mac.


### Routes excluded from the release promise

- Native Image Playground can produce photographic images. Shortcut Any Style
  has sometimes done so, but repeated photographic results are not established.
- Image Playground's ChatGPT Shortcut failed before inference on this build
  with a Shortcuts ToolKit database sandbox denial (`SQLite error 23`). Native
  Image Playground worked. This is separate from the working ChatGPT model bridge.
- The public Image Playground view controller displayed a completed preview
  and returned its file only after Done in a controlled test. A background run
  returned no file within 100 seconds. That helper is not shipped; it fails the
  invisible, unattended requirement.
- First-ever consent, another Mac, and locked/logged-out operation are untested.
  A Shortcuts request may require an unlocked session; Hollis reports that error.

### Security and local checks

The recorded Daybreak audit reported two medium and seven low findings, no high
or critical findings. All nine received targeted fixes and regression checks.
The combined candidate passed Go tests, the race suite, vet, Python harness and
review-boundary tests, and builds for Apple Silicon and Intel. Intel was
cross-built, not used for Apple Intelligence inference.

Fixes cover API authentication, bounded image processing, local image paths,
batch locks including macOS ACLs, terminal output, complete release-artifact
verification, live-test credentials and credentialed review automation. The
Poolside replacement uses text-only requests and no model command executor.
Its live API compatibility and first published GitHub run remain untested;
local fixture tests do not stand in for those results.

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

All observations are specific to the named beta builds. Apple exposes no stable
backend model IDs through Shortcuts, and a future macOS build may change the
picker or restore `pcc` to `fm`. Neither search results nor one machine can prove
universal availability.

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

1. Hollis remains completed-text transport: no fake streaming,
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
- Hollis implementation evidence: the [Use Model picker](results/img/use-model-picker-26A5421a.png), the [provider-free integration suite](internal/integration), and the separately gated [real-Mac suite](internal/integration/live_test.go).
