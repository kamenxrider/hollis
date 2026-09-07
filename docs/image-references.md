# Image references

Hollis can send one local image to an image-generation Shortcut as a reference.
Delivery of reference bytes has been demonstrated with the corrected bridge.
Reliable use of those pixels by the generator remains unproven: do not rely on
the reference to preserve a subject, composition or identity.

Use the current generated bridge from [image setup](image-generation.md#setup).
If you imported an earlier Reference Input v2 bridge, replace it: its Get Images
action was missing the explicit connection to decoded reference bytes.

The corrected connection was isolated with an echo test that returned the actual
input image. That establishes byte delivery through the tested bridge, not that
the generator used those pixels. `reference_image_sent` and matching checksums
describe Hollis's submitted input; they do not establish model conditioning.
Earlier generations had recognizable composition, but the prompts also named
the distinctive subjects. That result alone cannot distinguish reference use
from following the text. The [evidence record](../EVIDENCE.md#reference-correction)
preserves the original observations and bridge correction.

In the 0.3.2 comparisons, fully described scenes also appeared without a
reference, three times per historical case. A further control held prompt,
style, host and runtime constant and asked for the reference's main subject
without naming it:

| Attachment | Observed output (one call per condition) |
| --- | --- |
| Repair-café scene | Portrait of a man in a cap |
| Turtle with greenhouse | Sunflowers in a blue jug |
| None | Portrait of a man in a hat |

The café and no-reference conditions both produced unrelated male portraits;
neither attached subject was recovered. These results argue against relying
on reference editing in this flow. They do not prove that references can never
influence generation or identify the failing layer. The released bridge's
connections were inspected before these calls, but the current installed
bridge's action graph was not readable. See the
[0.3.2 call records](releases/v0.3.2-validation.json), including
`diagnostic-reference-cafe`, `diagnostic-reference-turtle` and
`diagnostic-reference-none`.

Hollis puts the newest revision first and retains earlier subject/text context.
The result is a newly generated image; details and proportions can change.
First-run permissions on a fresh Mac and exact identity preservation remain
untested. ChatGPT generation through Shortcuts is blocked on the measured build.

## Limits and accepted inputs

Historical API references share a 24-million-decoded-pixel request budget, in
addition to the 16-million-pixel and 4 MiB per-image limits. API requests remain
bounded to 8 MiB total, including base64 and text.

- References must be complete PNG or JPEG images.
- One reference may be at most 4 MiB after base64 decoding and at most 16
  megapixels.
- The complete HTTP request body is limited to 8 MiB.
- Remote URLs, server file paths, symlinks, and arbitrary Shortcut paths are
  rejected. The API accepts client-supplied inline bytes only.
- Hollis validates the actual image bytes, dimensions, format, and checksum
  before invoking the Shortcut.

These are Hollis safety limits. They are not Apple quotas, native resolution
controls, or proof of a particular Apple backend.

## CLI

The standalone image command accepts one explicit local reference:

```sh
hollis image generate "Put the bicycle beside a blue wall" \
  --style illustration \
  --reference-image ./bicycle.png \
  --output bicycle-wall.png
```

The public flags for this command are:

```text
--style <any|animation|genmoji|illustration|sketch|chatgpt>
--bridge <shortcut-name>
--reference-image <local PNG or JPEG path>
--output <PNG path>
--aspect-ratio <W:H>
--size <WIDTHxHEIGHT>
--fit <crop|pad>
--timeout <duration>
```

`--reference-image` requires the upgraded parameterized image Shortcut and
cannot be used with a legacy fixed-style bridge. `--style` and `--bridge` are
mutually exclusive. Output processing is local; `--aspect-ratio` and `--size`
are not native model controls.

Chat image turns expose the reference selector:

```sh
hollis chat --continue <conversation-id> --generate-image \
  --image-style illustration \
  --image-reference auto \
  --output revision.png \
  "Move the bicycle into a park"
```

The public chat flags are:

```text
--generate-image
--image-reference <auto|none|local PNG or JPEG path>
--image-style <any|animation|genmoji|illustration|sketch|chatgpt>
--image-bridge <shortcut-name>
--output <PNG path>
--aspect-ratio <W:H>
--size <WIDTHxHEIGHT>
--fit <crop|pad>
```

`--image-bridge` is an explicit Shortcut reference and cannot be combined with
`--image-style`. `--image-reference` defaults to `auto`.

### `auto`, `none`, and explicit paths

With `--image-reference auto`, Hollis searches the current local conversation
from newest to oldest for the latest assistant message that Hollis itself
marked as a generated image artifact. The artifact contains the generated
file path and SHA-256 checksum. Model-authored text that merely resembles an
artifact is not trusted.

Before sending an automatic reference, Hollis requires the recorded path to
still be a regular, non-symlink file, verifies its checksum, and revalidates
its PNG/JPEG format, dimensions, and limits. A missing, changed, corrupt, or
oversized file stops the image turn before the provider is called. Hollis does
not silently use a different file. Use `--image-reference none` to make a
text-only image request without reading a prior artifact, or pass an explicit
local path to select a different reference.

The first image in a new conversation has no prior artifact, so the default
`auto` selection is a no-op. Normal text turns do not attach image bytes. A
later image turn can use the latest trusted artifact from an earlier image
turn.

When a reference is attached, Hollis still validates and stores the full CLI
conversation. It places the newest revision first, followed by earlier subject
and text context after removing Hollis artifact metadata. Keeping the original
subject matters: a vague latest-only prompt lost the subject in a live check
even with an attachment. The ordering prioritizes the new change without
throwing away earlier text. With `--image-reference none`, the provider receives
chronological text replay. Neither mode guarantees exact object preservation.

In an interactive chat, configure `--output` and a style or bridge, then use
the `/image` command:

```text
> /image Draw the bicycle beside a blue wall
< Saved PNG to scene.png
> /image Move it into a park
< Saved PNG to scene-2.png
> /image Add a sunset
< Saved PNG to scene-3.png
```

The same `--image-reference` selection applies to each `/image` turn. Hollis
uses the base output path for the first image and appends `-2`, `-3`, and so on
for later images. It never overwrites an existing destination.

## API image generation

The HTTP server must have an image Shortcut configured. API callers select an
allowlisted style; they cannot select a Shortcut name or a local server path.
The image route is `hollis-image` when `model` is supplied.

Omitting the style selects `animation`. If only a specific style mapping is
configured, request that configured style explicitly. Image capability visibility
does not imply every style is configured or that ChatGPT has passed live
qualification.

### Standalone generation

`POST /v1/images/generations` accepts an inline PNG or JPEG data URL in
`reference_image`:

```json
{
  "model": "hollis-image",
  "prompt": "Put the bicycle beside a blue wall",
  "style": "illustration",
  "reference_image": "data:image/png;base64,<base64 PNG bytes>",
  "n": 1,
  "response_format": "b64_json"
}
```

The response contains the generated PNG in `data[0].b64_json`, plus style,
dimensions, checksum, and `reference_image_sent` metadata. The response does
not establish a server-side conversation.

### Chat Completions input and replay

`POST /v1/chat/completions` uses a final user message with text and one
`image_url` data URL:

```json
{
  "model": "hollis-image",
  "messages": [
    {
      "role": "user",
      "content": [
        {"type": "text", "text": "Turn this into a poster"},
        {"type": "image_url", "image_url": {
          "url": "data:image/jpeg;base64,<base64 JPEG bytes>"
        }}
      ]
    }
  ],
  "image_generation": {"style": "illustration"}
}
```

The assistant response contains a text generation record and an
`image_url` part whose URL is a PNG data URL. To make a follow-up, send the
complete assistant message from that response back in `messages`, followed by
the new user message:

```json
{
  "role": "assistant",
  "content": [
    {"type": "text", "text": "[Hollis generated an image ...]"},
    {"type": "image_url", "image_url": {
      "url": "data:image/png;base64,<previous response PNG bytes>"
    }, "style": "illustration", "width": 1024, "height": 1024,
      "sha256": "<response checksum>"}
  ]
}
```

The metadata is useful for preserving the response shape, but Hollis
revalidates the image bytes and does not trust a client-supplied label or
checksum as proof of the content.

### Responses input and replay

`POST /v1/responses` uses `input_image` in the final user content. Its data URL
is a string rather than the nested Chat Completions object:

```json
{
  "model": "hollis-image",
  "input": [
    {
      "role": "user",
      "content": [
        {"type": "input_text", "text": "Turn this into a poster"},
        {"type": "input_image",
         "image_url": "data:image/png;base64,<base64 PNG bytes>"}
      ]
    }
  ],
  "image_generation": {"style": "illustration"}
}
```

The assistant response contains an output message with an `output_text` record
and an `output_image` part:

```json
{
  "type": "output_image",
  "b64_json": "<previous response PNG bytes as base64>",
  "mime_type": "image/png",
  "style": "illustration",
  "width": 1024,
  "height": 1024,
  "sha256": "<response checksum>"
}
```

For a follow-up, append the complete assistant output message to `input`, then
append a new user message. Hollis accepts that `output_image` as a replayed
reference and validates the decoded bytes before generation. `previous_response_id`
and server-side response history are not supported.

### Reference selection in a conversation

For either conversation endpoint, Hollis preserves and validates the full text
transcript but sends at most one reference image to the Shortcut:

1. An image in the final user message wins.
2. If the final user message has no image, Hollis uses the latest prior
   assistant image replay.
3. If neither exists, the request is text-only.

Images must be in the final user message for new input. The final user content
must also contain nonempty text. Prior user images are validated when present
but are not selected as the provider reference. More than one reference part in
the same content item is rejected; separate older user images are validated but
do not override the final-user or latest-assistant selection rule.

The server is stateless. A client must resend the text history and the complete
generated assistant image part for a visual follow-up. On a reference turn,
the latest revision is placed first, followed by earlier subject and text
context. Without a reference, Hollis preserves chronological text replay. Hollis does not retain or
fetch an image from a response ID, URL, or filesystem path.

## Shortcut requirement

References require the upgraded parameterized Shortcut. Its input is JSON with
the following shape:

```json
{"prompt":"...","style":"Illustration","reference_base64":"<raw base64>"}
```

When there is no reference, `reference_base64` is omitted. The bridge receives
raw base64, without a `data:` URL prefix. Hollis maps its style IDs to the
native Image Playground labels.

The reproducible source and import setup is generated by
`scripts/make-image-bridge.py`; the exact signing and import commands are in
the [image-generation setup guide](image-generation.md#setup).
The generated source writes the production bridge and a separate ChatGPT
diagnostic bridge. The production bridge's imported name is exactly
`Hollis Image - Reference Input v2`.

A legacy fixed-style Shortcut receives a plain text prompt and cannot receive a
reference. Hollis rejects a reference before generation rather than silently
discarding it. Use the parameterized bridge for `--reference-image`, automatic
CLI reuse, and API references. `--image-reference none` is the explicit CLI
escape hatch when continuing through a legacy text-only bridge.

Configured bridges describe intent; they do not prove that Shortcuts is
installed, unlocked, permitted, or able to generate the selected style. Keep
the live Shortcut checks separate from these local contract and validation
tests. On the tested macOS 27.0 build, the installed Image Playground
ChatGPT route is currently blocked before inference: fixed, parameterized,
native-Shortcuts, and fresh-extension checks all reproduced Apple's
`GenerativePlaygroundAppIntents` Shortcuts ToolKit database sandbox denial
(SQLite error 23), while the native Image Playground app succeeded. This is a
scoped result for that host/build, not proof of universal impossibility; do not
silently retry or substitute another provider. Requalify after a supported
Apple route or OS fix.
