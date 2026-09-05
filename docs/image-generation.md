# Image generation

Hollis can save one generated PNG using an explicit Image Playground Shortcut.
This is an experimental, optional CLI and HTTP feature. It is separate from the four
text/image-understanding model tiers and requires its own Shortcut.

## Setup: one parameterized Shortcut

The preferred setup uses one Shortcut for every style. Create **Hollis Image -
Unified Probe** with the following actions, in this order:

1. Receive **Text and Rich Text**, with no-input behavior **Continue**.
2. **Get Dictionary Value**: get `prompt` from **Shortcut Input**.
3. **Set Variable**: set **Prompt** to that Dictionary Value.
4. **Get Dictionary Value**: get `style` from **Shortcut Input** again.
5. **Create Image**: Description = **Prompt**, Style = the second **Dictionary
   Value**, Photo empty, Save to Playground = **Never**.
6. **Stop and Output**: output **Image**, fallback **Do Nothing**.

Configure this once:

```sh
hollis config set image-bridge "Hollis Image - Unified Probe"
hollis image generate "A blue square on white" --style sketch --output square.png
```

Hollis sends a JSON object containing `prompt` and `style`. It maps its style
IDs to the native display values (for example `sketch` becomes `Sketch`). The
first live feasibility checks passed Animation and Sketch through the same
unchanged Shortcut, and visual inspection found the expected distinct styles.
The repeated suite is the source of broader runtime evidence.

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
showing UI. Review and complete required setup in Shortcuts before relying on
unattended invocation. Fresh imports and other macOS builds remain unverified.
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

Only PNG is currently accepted (16 MiB, 16 million pixels maximum). These are
Hollis validation limits. No native resolution/aspect-ratio/seed parameter or
exact Apple model identifier is exposed by the tested Shortcut. Photo input
and pixel editing are not implemented yet.

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
processing. With no output options, the native PNG is saved.

Apple's native framework does document size/aspect-ratio selection through
[SizeSpecification](https://developer.apple.com/documentation/imageplayground/imageplaygroundoptions/sizespecification-swift.struct).
However, its programmatic [ImageCreator initializer](https://developer.apple.com/documentation/imageplayground/imagecreator/init())
is documented to throw `notSupported` on macOS 27 and later. Apple directs
applications to `ImagePlaygroundViewController` or `imagePlaygroundSheet`, which
are interactive UI integrations. The installed SDK also marks ImageCreator
deprecated at 27.0. Hollis has not implemented an interactive native app adapter;
these native options are not controls supported by its Shortcuts transport.

## Images during CLI chat

An explicit image request can use a chat's accumulated text context:

```sh
hollis chat "We are designing a lighthouse poster with a blue sky"
hollis chat --continue <id> --generate-image --image-style animation \
  --output poster.png "Generate the poster we discussed"
hollis chat --continue <id> --generate-image --image-style animation \
  --output sunset.png "Now use a sunset sky"
```

In an interactive chat, configure `--image-style` (or `--image-bridge`) and
`--output` when starting, then enter `/image Draw the scene we discussed`.
The output is one fixed new path for that invocation; use a new invocation and
path for another image. Output dimension options also work on chat image turns.

The conversation stores the generation request and a typed artifact record
with path, style, dimensions and checksum. It does not store or re-read image
pixels as context. Follow-up generation uses textual history; it is not a
pixel edit of the previous PNG. Normal text turns can continue in the same
conversation. Every image needs a new destination, so a repeated command cannot
silently overwrite an earlier image.

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
bodies remain bounded to 8 MiB; retain the text record instead of accumulating
base64 images in history. The complete generated assistant message is also accepted on an image-generation
follow-up, although its pixels are ignored. For normal text turns, carry forward
the text generation record without the image block. Streaming remains unsupported.

## Capabilities and evidence

| Capability | Evidence | Hollis status |
| --- | --- | --- |
| Text to image | Two distinct prompts returned valid, visually matched PNGs without interaction on the tested Mac | Implemented |
| Animation | Used for both real tests | Tested configuration |
| Any Style, Genmoji, Illustration, Sketch, ChatGPT | Visible in this Mac's Create Image Style menu on 2026-09-05 | Configurable routes implemented; live style behavior not yet exercised |
| Photo reference | A Photo template field is visible in the action | No file-input contract implemented or tested |
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

The remaining live checks are all style variants, fresh setup, API/chat invocation, and a synthetic photo-reference bridge. Configuration and provider-free tests do not replace those runtime checks.
