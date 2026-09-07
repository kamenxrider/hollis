# Image generation

Hollis generates PNG images through a single Image Playground Shortcut. Use it
from the terminal, within a Hollis chat, or through the local API. The same
bridge accepts a style and an optional reference image.

## Setup

The normal **hollis-bridges.zip** release download includes all four model
bridges plus **Hollis Image - Reference Input v2**. Follow the
[Quickstart](../README.md#quickstart) to verify the package, sign the shortcuts
on your Mac and add them. No source checkout or separate image download is needed.

Then configure the exact image Shortcut name:

```bash
hollis config set image-bridge "Hollis Image - Reference Input v2"
hollis image generate "A small red sailboat on a calm blue lake" \
  --style illustration --output sailboat.png
```

Choose a fresh PNG filename in an existing directory. Restart a running Hollis
API server after configuring the bridge. `hollis doctor` checks the four model
bridges; the generation above is the actual image-route check.

**Upgrading an earlier image bridge:** replace the old Reference Input v2
Shortcut when importing the release copy. The corrected bridge explicitly
connects Base64 Decode to Get Images. Rebuilding the binary alone cannot update
an installed Shortcut. Keep the exact name above, or configure your chosen name.

First-use Add/Allow prompts are part of setup. Once authorized, the tested
Shortcut returns files directly without a per-image click. This works in an
unlocked macOS 27 session; it is not a logged-out service. Hollis does not open
the native Image Playground editor or automate its Done button. First-ever
permissions on another Mac remain untested.

### Generate the bridge from source

Developers can inspect or regenerate the same bundled source:

```bash
HOLLIS_BRIDGE_DIR="$(mktemp -d)"
python3 scripts/make-image-bridge.py "$HOLLIS_BRIDGE_DIR"
mkdir "$HOLLIS_BRIDGE_DIR/signed"
shortcuts sign --mode anyone \
  --input "$HOLLIS_BRIDGE_DIR/Hollis Image - Reference Input v2.shortcut" \
  --output "$HOLLIS_BRIDGE_DIR/signed/Hollis Image - Reference Input v2.shortcut"
open "$HOLLIS_BRIDGE_DIR/signed/Hollis Image - Reference Input v2.shortcut"
```

The script also produces a ChatGPT diagnostic; that diagnostic is excluded from
the release bundle. The production bridge configures incoming Text/Rich Text
and contains these actions:

1. Get Dictionary Value `prompt` from Shortcut Input; set variable `Prompt`.
2. Get Dictionary Value `style` from Shortcut Input.
3. Get Dictionary Value `reference_base64` from Shortcut Input.
4. Base64 Decode the reference value.
5. Get Images from the **explicit Decode output**.
6. Create Image with Description = Prompt, Style = the style value,
   Photo = Images, Save to Playground = Never.
7. Stop and Output the generated Image, with fallback Do Nothing.

Receive is input configuration, not an extra action. The optional reference key
is omitted for a text-only generation. The model bridges are independent of this
image-generation bridge.

## Tested behavior and limits

Animation, Illustration, Sketch, Genmoji and Any Style returned valid PNGs in
paced tests. API generation and CLI/API conversation follow-ups also returned
images. Earlier reference tests checked bytes at the Hollis boundary but missed
a connection inside the Shortcut. The corrected connection passed an echo
control and a live reference generation. See the [corrected evidence](../EVIDENCE.md#reference-correction).

In the 0.3.2 comparisons, both historical prompts generated three times with
and three times without their references. Controls that omitted the subject
from the prompt did not reproduce the attached subject. Successful attachment
delivery is therefore not enough to promise reliable reference editing. See
the [repeated test results](releases/v0.3.2.md#validation).

**Any Style does not guarantee photographs.** Native Image Playground can
produce photographs, but we have not found a repeatable unattended photographic
setting for its Shortcut action. The image bridge remains experimental, with
provider refusals and build-specific behavior recorded rather than hidden.

**ChatGPT image generation is blocked on the tested build** (`26A5425a`). Fixed
and parameterized Shortcuts failed before inference with a Shortcuts ToolKit
database sandbox denial (`SQLite error 23`), while native Image Playground
worked. This does not affect the separate ChatGPT text/image-understanding
bridge. No supported repair was established; Hollis does not silently substitute
a provider. Recheck after a supported Apple update.

A native “This shortcut requires your Mac to be unlocked” error becomes
`image_session_locked` (HTTP409). Hollis does not manipulate the session or retry.
The public native image sheet required a visible window and Done in a controlled
test, so that helper is not part of Hollis.

### Existing fixed-style shortcuts

Existing single-style shortcuts still work. Configure one per style if needed:
Description = Shortcut Input, a fixed Style, Photo empty, Save to Playground =
Never, then Stop and Output = Image with fallback Do Nothing. Fixed-style bridges
accept plain text and cannot accept reference images. Use the bundled bridge
for new installations and conversation references.

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

Configure the permitted style bridges locally before starting
`hollis serve --token-file <private-file>`.
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

## Evidence

See [failure codes](errors.md) for runtime 0.3.2's `request_declined` and
`shortcut_failed` diagnostics. A request for a different description leaves
the underlying cause unknown; it is not confirmed safety-filter evidence.

The [evidence record](../EVIDENCE.md#v030-validation) separates live model
results from local contract tests and records the reference-wiring correction.
The release bridge has a regression test for the exact Decode → Get Images →
Photo path; the packaging test checks the bridge actually shipped in the ZIP.

Apple documents the Create Image action in its
[Shortcuts release notes](https://support.apple.com/en-us/125148). Its
[model research](https://machinelearning.apple.com/research/introducing-third-generation-of-apple-foundation-models)
describes wider image capabilities, which are not automatically Shortcut controls.
The requested style is not proof of a private backend model or recipe.
