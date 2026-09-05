# Image generation

Hollis can save one generated PNG using an explicit Image Playground Shortcut.
This is an experimental, optional CLI and HTTP feature. It is separate from the four
text/image-understanding model tiers and requires its own Shortcut.

## Setup: generated parameterized Shortcut

The reference-capable setup uses one parameterized Shortcut for every style.
From a Hollis source checkout, generate and sign the source with the repository
script:

```sh
BRIDGE_DIR="$(mktemp -d)"
python3 scripts/make-image-bridge.py "$BRIDGE_DIR"
shortcuts sign --mode anyone \
  --input "$BRIDGE_DIR/Hollis Image - Reference Input v2.shortcut" \
  --output "$BRIDGE_DIR/Hollis Image - Reference Input v2.signed.shortcut"
open "$BRIDGE_DIR/Hollis Image - Reference Input v2.signed.shortcut"
```

In Shortcuts, choose **Add Shortcut** when prompted. The imported Shortcut is
named **Hollis Image - Reference Input v2**. Configure that exact name:

```sh
hollis config set image-bridge "Hollis Image - Reference Input v2"
hollis image generate "A blue square on white" --style sketch --output square.png
```

The Receive section configures Text and Rich Text input with no-input behavior
Continue; it is not a separate action. The generated actions are:

1. **Get Dictionary Value** for `prompt` from **Shortcut Input**.
2. **Set Variable** to **Prompt**.
3. **Get Dictionary Value** for `style` from **Shortcut Input**.
4. **Get Dictionary Value** for `reference_base64` from **Shortcut Input**.
5. **Base64 Encode** set to **Decode**, using that reference value.
6. **Get Images from Input**, consuming the decoded bytes.
7. **Create Image**: Description = **Prompt**, Style = the style value,
   Photo = **Images**, Save to Playground = **Never**.
8. **Stop and Output**: output **Image**, fallback **Do Nothing**.

Hollis sends `prompt`, the native style label, and an optional raw
`reference_base64` value. It maps IDs such as `sketch` to labels such as
`Sketch`. When no reference is supplied, the optional field is omitted. The
source generator also writes a separate ChatGPT diagnostic Shortcut; it is for
the scoped investigation below, not the production bridge.

The signed reference bridge was imported and exercised on the already
authorized macOS 27.0 build `26A5425a`. The complete qualification produced 52
valid images across diagnostic and acceptance runs, with at least three
seconds from completion to the next generation. All five working styles
(Any Style, Animation, Genmoji, Illustration, Sketch) were exercised with and
without references, including PNG/JPEG and local crop/pad processing.

Earlier conversation prompt formats sometimes ignored a new scene or lost a
subject. The final renderer puts the newest revision first while retaining
earlier subject and text context. All nine final images passed visual review:
three-turn interactive chat, Chat Completions, and Responses each retained
the intended subjects and applied the requested scene changes. Reference
metadata and checksums also matched. This is bounded visual and transport
evidence, not proof of exact identity retention or backend pixel conditioning.
Any Style has not reliably honored photographic prompts. Hollis preserves
provider failures and does not retry or silently substitute a style.

The current ChatGPT result has a narrower, definite scope. A dated investigation
on macOS 27.0 build `26A5425a` reproduced the failure through a fixed bridge, the
parameterized bridge, native Shortcuts, and a fresh extension process. Apple's
`GenerativePlaygroundAppIntents` extension selected ChatGPT but hit a sandbox
denial reading its Shortcuts ToolKit database (`SQLite error 23`) before
inference. Native Image Playground generated the same kind of request
successfully. Do not advertise ChatGPT through this installed Shortcuts route
on this host, retry prompts, or silently substitute another provider. This does
not prove that every future macOS or Shortcuts build is impossible; requalify
after a supported Apple route or OS fix.

Earlier API attempts also exposed Apple's “This shortcut requires your Mac to
be unlocked” diagnostic. The later paced acceptance pass completed all listed
API generations through the imported reference bridge. The API and CLI still
return actionable `image_session_locked` errors for that exact native
diagnostic, without session manipulation or automatic retry. The final
conversation rerun passed transport, checksum, and bounded visual checks as
described above.

## Legacy fixed-style setup

On a Mac with Image Playground and Apple Intelligence available, create a
Shortcut named **Hollis Image Generation Probe** (or use another explicit name).
The actual setup tested on macOS 27.0 build `26A5425a` is:

1. Enable **Use as Quick Action** in Shortcut Details to expose the Receive
   input section. Receive **Text and Rich Text**; if there is no input,
   **Continue**. Hollis rejects empty prompts before running the Shortcut.
2. Add Image Playground's **Create Image** action. Set **Description** to the
   **Shortcut Input** variable. In the tested editor, Control-clicking the
   Description field offered Shortcut Input directly. Set **Style** to
   **Animation**, leave **Photo** empty, and set **Save to Playground** to
   **Never**.
3. Add **Stop and Output**, using the actual **Image** variable from Create
   Image. Set “If there's nowhere to output” to **Do Nothing**.

The Receive section is input configuration, not an action to drag into place.
No Get Text action is needed for this tested flow. Style and Photo must not
contain Shortcut Input for the text-only setup. If replacing another action,
remove its stale Response variable and select the Image output instead.

First-run permissions may require your attention. Hollis does not grant
permissions, sign into an account, or answer dialogs. `--no-input` prevents
Hollis terminal prompts; it cannot prevent a chosen Shortcut or macOS from
showing UI. The generated reference bridge was imported on the already
authorized test Mac; first-ever permissions and a new Mac remain untested.
The standard release bridge bundle and `doctor` cover the four model bridges;
they do not install or validate this optional image Shortcut.

## Styles

`hollis image styles` lists **any, animation, genmoji, illustration, sketch,
chatgpt**, corresponding to the choices observed in the installed action.
The single configured bridge receives the selected style as JSON. You can
still override individual styles with separately configured fixed-style
Shortcuts for compatibility or diagnosis:

```sh
hollis config set image-bridge animation "Hollis Image Generation Probe"
hollis config set image-bridge illustration "Hollis Image - Illustration"
hollis image styles --json
hollis image generate "A lighthouse on a cliff" --style illustration --output lighthouse.png
```

For each legacy variant, duplicate the tested fixed-style bridge, change Style
to the matching menu option, and give it a distinct name. Configuration is an explicit mapping,
not runtime validation of the action. An unconfigured style fails before a
model runs. Without `--style` or `--bridge`, Animation is selected. `--bridge` accepts an explicit custom Shortcut and cannot be combined
with `--style`; its actual style is determined entirely by that Shortcut.
An explicit per-style mapping takes precedence over the single bridge. An
empty name clears the relevant single bridge or per-style mapping. Restart a running
API server after changing mappings.

## Run

```sh
hollis image generate "A red circle on a white background. No text." \
  --bridge "Hollis Image Generation Probe" --output circle.png
```

Use a new destination in an existing directory. One positional prompt, one
selected bridge, one output file, one generation attempt. `--timeout` accepts
positive durations up to 120s (the default). There are no automatic retries.
An existing output is rejected before generation, and a collision that arises
during generation is also refused. Output files are created with mode 0600.
Staging files are private and cleaned up after execution; a failed generation
does not publish a partial image. Cancellation terminates the owned command
process group, not the shared Shortcuts application or a remote inference job.

`--json` returns `path`, `format`, `bytes`, `width`, `height`, and `checksum`.
`--agent` wraps these in the normal agent envelope; `--select` works as on
other data commands. The CLI prints metadata, never binary image bytes.

Generated output is currently accepted as PNG up to 16 MiB and 16 million
pixels. These are Hollis validation limits. Reference inputs are separate:
they may be PNG or JPEG up to 4 MiB and 16 megapixels; see
[image references](image-references.md) for the CLI, chat, and API contracts.
No native resolution/aspect-ratio/seed parameter or exact Apple model
identifier is exposed by the tested Shortcut. A reference can guide a new
generation, but Hollis does not promise pixel-perfect editing.

## Output dimensions and aspect ratio

The installed action has no observed native size or ratio control. Hollis
therefore offers explicit local post-processing after generation:

```sh
hollis image generate "A lighthouse on a cliff" --style animation \
  --aspect-ratio 16:9 --fit crop --output banner.png
hollis image generate "A lighthouse on a cliff" --style animation \
  --size 1200x800 --fit pad --output padded.png
```

`--fit crop` centers and crops the image to the requested ratio; `--fit pad`
adds white space to retain the full composition. An exact `--size` also resizes
the fitted output. `--aspect-ratio` and `--size` are mutually exclusive, and
both require an explicit fit choice. These operations cannot invent content
outside the generated image or improve native resolution. Metadata retains
native dimensions separately from final dimensions and identifies the output
processing. With no output options, the native PNG is saved. Genmoji may return
a transparent background even when a prompt requests white. Cropping can cut
into the subject; choose padding when preserving the whole composition matters.

Apple's native framework does document size/aspect-ratio selection through
[SizeSpecification](https://developer.apple.com/documentation/imageplayground/imageplaygroundoptions/sizespecification-swift.struct).
However, its programmatic [ImageCreator initializer](https://developer.apple.com/documentation/imageplayground/imagecreator/init())
is documented to throw `notSupported` on macOS 27 and later. Apple directs
applications to `ImagePlaygroundViewController` or `imagePlaygroundSheet`, which
are interactive UI integrations. The installed SDK also marks ImageCreator
deprecated at 27.0. Hollis has not implemented an interactive native app adapter;
these native options are not controls supported by its Shortcuts transport.

## Images during CLI chat

An explicit image request can use a chat's accumulated text context and, by
default, the latest trusted generated image as a reference:

```sh
hollis chat "We are designing a lighthouse poster with a blue sky"
hollis chat --continue <id> --generate-image --image-style animation \
  --output poster.png "Generate the poster we discussed"
hollis chat --continue <id> --generate-image --image-style animation \
  --output sunset.png "Now use a sunset sky"
hollis chat --continue <id> --generate-image --image-style animation \
  --image-reference none --output text-only.png "Start a fresh scene"
```

In an interactive chat, configure `--image-style` (or `--image-bridge`) and
`--output` when starting, then enter `/image Draw the scene we discussed`.
With the default `--image-reference auto`, later `/image` turns use the latest
trusted artifact. `--image-reference none` disables that reuse, and an explicit
PNG/JPEG path selects a different reference. The output is one fixed new path
for that invocation; Hollis appends `-2`, `-3`, and so on for later images and
never overwrites an earlier one. Output dimension options also work on chat
image turns.

The conversation stores the generation request and a typed artifact record
with path, style, dimensions, and checksum. With a reference attached, Hollis
puts the newest revision first, then includes earlier subject descriptions and
text context with its own artifact metadata removed. Both the original history
and the rendered prompt are checked against the input limits. This ordering
avoids burying the newest change while retaining context for requests such as
“keep the same subjects”. Without a reference, chronological text replay remains
available. Reference input does not guarantee that the backend preserves every
object or edits existing pixels. Normal text turns can continue in the same
conversation. See [image references](image-references.md) for integrity and
precedence rules.

## HTTP and API conversations

Configure the permitted style bridges locally before starting `hollis serve`.
Remote callers choose a style from that allowlist; they cannot provide an
arbitrary Shortcut name or a local filesystem path. Authentication and shared
model concurrency apply to generation too.

```json
POST /v1/images/generations
{"prompt":"A lighthouse on a cliff","style":"animation","n":1,"response_format":"b64_json"}
```

This returns base64 PNG data with style, native/final dimensions, checksum and
explicit output-processing metadata. `n` is restricted to one. Optional
`aspect_ratio` or `size` plus `fit` have the same post-processing semantics as
the CLI.

Both conversation endpoints accept the Hollis-specific `image_generation`
extension to request an image explicitly. It is not an autonomous model tool
call or a promise of the complete OpenAI image-generation API. `hollis-image`
is Hollis's routing identifier, not an Apple backend name.

```json
POST /v1/chat/completions
{
  "model":"hollis-image",
  "messages":[
    {"role":"user","content":"We are making a blue lighthouse poster."},
    {"role":"assistant","content":"A lighthouse with a blue sky and sea."},
    {"role":"user","content":"Generate that poster."}
  ],
  "image_generation":{"style":"animation","aspect_ratio":"16:9","fit":"crop"}
}
```

```json
POST /v1/responses
{
  "model":"hollis-image",
  "input":[{"role":"user","content":"Generate a lighthouse poster."}],
  "image_generation":{"style":"illustration"}
}
```

The response includes an image and a textual generation record. Include the
conversation text and that record in a follow-up request to retain context.
The server does not retain a conversation or previous image behind response
IDs. Native pixel editing and `previous_response_id` are unsupported. Request
bodies remain bounded to 8 MiB. A final-user image input wins; otherwise the
latest replayed assistant image can be used as the one reference. On a
reference turn, the full text history is validated and the newest revision is
placed before earlier subject and text context. The stateless server does not
store that conversation. With no reference, chronological text replay remains
available. The complete generated assistant message is
accepted for image follow-up when its image bytes pass validation. For normal
text turns, carry forward the text generation record without the image block.
See [image references](image-references.md) for exact inline and replay
schemas. Streaming remains unsupported.

## Capabilities and evidence

| Capability | Evidence | Hollis status |
| --- | --- | --- |
| Text to image | Two distinct prompts returned valid, visually matched PNGs without interaction on the tested Mac | Implemented |
| Animation | Repeated direct generations and two fixed-photo reference probes succeeded | Tested configuration |
| Any Style, Genmoji, Illustration, Sketch | Two realistic-scene generations per style succeeded | Tested configurations; Any Style did not honor photographic prompts in two later samples |
| ChatGPT | Native app generated a realistic landscape; the fixed, parameterized, native-Shortcuts, and fresh-extension routes all hit Apple's pre-inference ToolKit database sandbox failure on the tested macOS 27 build | Scoped blocked on the tested host/build; not a universal impossibility claim |
| Photo reference | Fixed-photo probes and the imported parameterized bridge delivered validated references through CLI, interactive, Chat Completions, Responses, and standalone API calls | Transport and bounded three-turn visual checks passed; exact pixel editing unproven |
| Image editing, photorealism, varying aspect ratios/resolutions | Apple describes these capabilities for ADM 3 Cloud | Not proof they are controllable through this Shortcut |
| Native Image Playground size options | Public native API documents them; ImageCreator initialization is unsupported on macOS 27+ | Not exposed through this transport |
| Exact backend, model version, quota | Not reported by the tested path | Unknown; no invented selector or usage count |

The two live tests used a red circle and a blue square on white, with no text.
Both returned 1024×1024 PNGs in approximately 7.3s and 5.5s respectively. Visual
review found matching subjects, with the shading expected from Animation.
No user or agent interaction was supplied during execution. Sampled UI checks
showed no prompt; this was not a continuous recording. These tests exercised
the same command implementation and transport before root registration;
full-root integration is also covered by provider-free tests. This is a
bounded smoke test, not a quality benchmark or general reliability guarantee.

Apple documents the Create Image action in [Shortcuts release notes](https://support.apple.com/en-us/125148).
Its [third-generation model research](https://machinelearning.apple.com/research/introducing-third-generation-of-apple-foundation-models)
describes ADM 3 Cloud's image capabilities. Hollis does not infer that a
particular style uses ADM 3, that every model feature has a Shortcut parameter,
or that ChatGPT in the style menu behaves like the text model tier.

Current-build qualification is complete for the working styles and the tested
conversation paths. First-ever permission/new-Mac setup remains untested.
The ChatGPT Image Playground Shortcut route is a confirmed platform blocker
before inference on the tested macOS 27 build; do not silently substitute it.
Reference attachment and bounded visual continuity checks passed, but exact
identity, pixel editing, photorealism, and unseen prompts remain model-dependent.
This remains an experimental, unreleased feature.
