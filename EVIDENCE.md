# Evidence and prior art

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

## Earlier image-generation checkpoints (historical, pre-reference bridge, 2026-09-05)

The image-generation paragraphs in this historical section describe the
diagnostic and pre-reference-bridge checkpoints. Statements that dynamic
attachments, API conversations, interactive turns, or ChatGPT were
unresolved describe that earlier state. The current reference-bridge evidence
and the remaining qualification boundary are recorded in the section below.

Latest image diagnostics add nine serial CLI attempts: six validated PNGs and
three Apple rejections. Two photographic Any Style requests generated animated
3D results, so they failed the visual photorealism objective. A native Image
Playground ChatGPT submission produced a realistic landscape from the same
prompt rejected through Shortcuts. That native app result was visually checked;
it is not proof of a working Hollis ChatGPT route.

A clean description-plus-revision prompt successfully reproduced the requested
turtle scene change after the full CLI chat transcript had failed. Separately,
a diagnostic copy of the unified Shortcut with the generated turtle PNG fixed
in Photo passed two revision-only requests (greenhouse sunset and snowy forest).
At that checkpoint, those tests proved reference-image feasibility, while
dynamic CLI/API attachments were not yet implemented. ChatGPT with the Photo
attachment still failed through Shortcuts. Calls were serial with at least 30
seconds between them. The later reference-bridge implementation supersedes
the attachment status; these checkpoints remain historical evidence.

The resulting shared image-context renderer omits Hollis artifact records and
general chat boilerplate while preserving user revisions, other text and input
limits. Five additional serial live requests passed: the exact previously failed
CLI continuation, and an initial image plus follow-up through each HTTP endpoint.
All PNGs were inspected. A Responses follow-up changed the setting but lost the
miniature greenhouse on the turtle shell; successful generation is not proof of
exact visual continuity. Total this diagnostic phase: 14 Shortcut attempts, 11
PNGs and three rejections, plus one successful native app submission. Dynamic
photo attachment and the Shortcuts ChatGPT route were unresolved at that
checkpoint.

Validation after the renderer change: `go test ./...`, focused race tests and
`go vet` for imagegen/cli/server passed. Both Darwin architectures built; the
ARM binary ran the five live requests. The Intel binary was built but not run.


An isolated candidate adds UTF-8 instruction/document files and inline PNG/JPEG
input for both HTTP model endpoints. The full Go race suite and `go vet ./...`
passed after integration. Tests use injected runners and synthetic images;
they cover exact document preparation, malformed input, image format and size
limits, endpoint history, model selection, authentication, capacity and staging
cleanup. These are local implementation checks, not new live Apple model tests.

The image research harness also has 13 provider-free plan/answer tests and six
synthetic subprocess tests. The subprocess checks need host `ps` access; they
passed with that access after the sandbox denied process inspection. No live
mode was used for these checks.

The same candidate also includes finite folder batches with private JSON
manifests and response envelopes. The combined race suite and vet passed after
integration. A public-command test uses the real local store and a fake model
to verify plan/run/resume, call budgets, lifetime counts, no repeat of verified
successes and refusal of corrupted results. Store tests cover collision
preservation, source/result validation, process-backed lock exclusion and
release after process death. Fault injection verifies recovery when a result
was saved before the success manifest. Pacing is tested with fake clocks.
No live batch model calls were made during that implementation checkpoint;
the subsequent authorized smoke run is recorded below.

Image generation is now registered as the optional experimental `image generate`
command in the combined candidate. Its actual Shortcuts transport completed two
unattended Animation-style generations on macOS 27.0 build `26A5425a`: red circle
and blue square, both visually matched 1024×1024 PNGs, in 7.3s and 5.5s. Those
live calls exercised the command before root registration. Full-root command,
agent output and no-call validation are separately checked with fake generators.
The combined candidate implements one parameterized JSON image Shortcut for all
six style choices, optional fixed-style overrides, local crop/pad/resize, CLI
conversation image turns, `/v1/images/generations`, and explicit generation on
both conversation endpoints. Full `go test -race ./...` and `go vet ./...`
passed with fake providers, and both Darwin architecture builds passed.

The September 5 repeated live probe produced nine valid CLI PNGs: Any Style once,
and Animation, Genmoji, Illustration, and Sketch twice each. All nine were fully
decoded, checked against dimensions/checksums, and visually reviewed. Two prior
single-bridge feasibility calls also passed, with distinct Animation and Sketch
images. Genmoji returned transparency; explicit cropping clipped the subject.
An identical Any Style repeat was rejected by Apple. ChatGPT failed through
both the parameterized bridge and a fixed-style comparison. These failures
remain part of the record and do not justify a reliability claim.

At this pre-reference-bridge checkpoint, API attempts returned 502; a temporary private diagnostic identified the native
error “This shortcut requires your Mac to be unlocked.” The console-lock flag
subsequently read false, so the exact session/context requirement still needs
verification. An initial CLI conversation request also received Apple's generic
prompt rejection. The full 28-case matrix is **not complete**; successful live
API generation, conversation follow-ups, interactive `/image`, and ChatGPT
image generation were unqualified at that checkpoint. Broad live testing then
stopped pending the session issue. No automatic retries or fallbacks were used;
manual diagnostic comparisons were separately recorded, with at least 30
seconds between attempts.

The observed unlocked-session message is now classified into an actionable
CLI error and API HTTP 409 `image_session_locked`. Provider-free transport, CLI,
and server tests cover this exact diagnostic and redaction; the updated error
classification has not made a new live call.

The opt-in paced harness retains every attempt and output, stops on the first
failure, checks full PNG decoding and metadata, and keeps provider-free contract
tests separate from live evidence. This is an experimental local candidate,
not a released image feature.
A later realistic-scene run made 15 attempts and produced 13 validated,
visually reviewed PNGs: two each for Animation, Illustration, Sketch, Genmoji,
and Any Style, plus two standalone HTTP outputs and an initial CLI conversation
image. ChatGPT rejected an alpine-cabin prompt; the CLI follow-up also received
an Apple rejection. The API session error did not recur in these two later
requests, but changed prompts and session/awake conditions prevent attributing
that improvement to a single cause. API conversations, successful CLI
follow-ups, and interactive image turns were unqualified at that checkpoint.
The realistic preset supplements the geometry cases; it does not erase earlier
failures or validate the prompting guide's architecture/API claims. The harness
had 23 provider-free tests at that point.

See [setup and capability boundaries](docs/image-generation.md).

The prior document, HTTP-image and batch candidate also passed 21 real requests
across On-Device, Cloud, Cloud Pro and ChatGPT on this build. Completed batches
resumed without additional attempts. These earlier smoke tests are not repeated
by the provider-free suite and do not imply a measured failure rate or quota.

## Final reference-bridge evidence (2026-09-05)

The source-generated and signed **Hollis Image - Reference Input v2** Shortcut
was imported on an already authorized macOS 27.0 build `26A5425a`. The source
generator is `scripts/make-image-bridge.py`; the reproducible signing and
import setup is documented in [the image-generation guide](docs/image-generation.md#setup-generated-parameterized-shortcut).

The complete qualification produced 52 valid images across diagnostic and
acceptance runs. Calls were serialized with at least three seconds from one
generation's completion to the next start. Coverage included CLI continuation,
interactive `/image`, Chat Completions, Responses, standalone image API,
PNG/JPEG references, all five working styles with and without a reference,
and local crop/pad geometry. Reference-delivery metadata and checksums matched.

Earlier prompt formats exposed visual failures: stale scenes and lost subjects.
The final renderer places the newest revision first and retains earlier
subject and text context. All nine final images passed visual review across
three-turn interactive, Chat Completions, and Responses sequences: subjects
remained recognizable and requested scene changes appeared. The 52 total is a
transport-success count, not a claim that every diagnostic image was visually
correct. Neither successful reference delivery nor these visual matches prove
exact pixel conditioning or identity preservation by Apple's backend.

The reference contract is implemented for standalone CLI input, automatic
trusted-artifact reuse, explicit `none`, API inline input, and API assistant
image replay. With a reference, Hollis puts the newest revision before earlier
subject descriptions and text context, while validating the complete original
history. CLI history is stored locally; API conversations remain stateless.
Without a reference, chronological text replay remains available. The reference is guidance
for a new generation, not guaranteed pixel editing.

A scoped investigation on macOS 27.0 build `26A5425a` found that the Image
Playground ChatGPT Shortcut route fails before inference: fixed, parameterized,
native-Shortcuts, and fresh-extension checks all reproduced Apple's
`GenerativePlaygroundAppIntents` Shortcuts ToolKit database sandbox denial
(SQLite error 23), while the native Image Playground app succeeded. This is a
host/build-scoped result, not proof of universal impossibility. Do not
advertise, silently retry, or substitute that route; requalify after a
supported Apple route or OS fix. First-ever permission prompts on a new Mac
remain untested.

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
- Hollis implementation evidence: the [Use Model picker](results/img/use-model-picker-26A5421a.png), the [provider-free integration suite](internal/integration), and the separately gated [real-Mac suite](internal/integration/live_test.go).
