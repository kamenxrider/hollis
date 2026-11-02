# Hollis workflows

All examples below are arguments to `bash "$KIT/scripts/run.sh"`; replace paths with actual absolute paths. Discover the complete flags using `agent-context`. Do not copy illustrative filenames into a live request without checking them.

## Documents and pictures

- Text documents: `respond --agent --model cloud --prompt-file instructions.txt --file brief.md --file notes.txt`. Only regular UTF-8 `.txt`/`.md` documents; no PDF. Instruction plus documents must fit 128 KiB. Documents cannot be mixed with images in one call.
- Image understanding: `respond --agent --model cloud-pro --image screenshot.png --prompt-file question.txt --timeout 120s`. Cloud and Cloud Pro accept repeated PNG/JPEG images; ChatGPT accepts one. On-Device and auto are not supported for this input.
- Current CLI image bounds are 64 MiB / 64 million pixels per input. Use real regular files; the runtime rejects arbitrary symlinks. Do not bypass its checks. HTTP limits are different and do not apply to these CLI examples.

## Generate and revise

`image generate 'A tiny greenhouse on a brass turtle, detailed illustration' --style illustration --output /absolute/new-image.png --agent --timeout 120s`

The five supported selections are `animation`, `illustration`, `sketch`, `genmoji`, and `any`. Default to `animation` when the user has no style preference, matching Hollis. `any` does not guarantee photographs. ChatGPT **text and image understanding** work; ChatGPT **image generation** is unavailable through the tested Shortcut route.

For a follow-up: keep the previous returned local file path, then call `image generate` with the requested change and `--reference-image /absolute/previous.png`. This submits the previous image; reliable use of its pixels remains unproven, including subject preservation. Keep relevant subject and scene context in the prompt, and assess the returned result rather than treating successful attachment delivery as proof of an edit. Use a new output filename each time. An explicit fresh-scene request omits the reference.

Ratios and sizes are local processing after generation: `--aspect-ratio 16:9 --fit crop`, or `--size 1200x800 --fit pad`. Preserve native output when neither is requested. If the user requests a ratio without specifying a fit, explain crop versus padding and ask which preserves their intent. These flags do not control Apple's native sampling. No seed control is exposed.

For an explicitly persistent conversation, use `chat --agent --model cloud` initially and retain its returned ID. Image turns use `chat --continue <id> --generate-image --image-style illustration --output <new.png> <prompt>`, with `--image-reference auto|none|<path>` as appropriate. Image-understanding history is not supported by Hollis chat; use explicit `respond` calls with selected context instead.

## Guided output (native local only)

Offer this only for the on-device model. Check support first: `models --model local --json` must report `available: true`, `helper_protocol: 2` and `capabilities.guided_output: true`. A protocol-1 helper cannot produce guided output; say so instead of degrading to plain text. Never use `--schema` to stand in for a requested Cloud/Cloud Pro tier: if the user asked for that tier, answer with it or explain the failure of that request.

Write the schema to a private file, keep it for the call, and delete it afterwards. Then:

```sh
bash "$KIT/scripts/run.sh" respond --agent --model local --schema /private/person.json "Invent one person"
bash "$KIT/scripts/run.sh" respond --json --model local --schema /private/person.json "Invent one person"
```

With `--json`, `response` is the decoded object; without it the object prints indented. Guided output is complete-only: `--stream` and `--image` are usage errors, and every non-local route rejects `--schema` before generation.

The accepted subset, matching the runtime's own checks: an object root with `"additionalProperties": false` on every object; `string`, `integer`, `number` and `boolean` properties; `required` naming declared properties; string-only enums (non-empty, unique); arrays with `items` and `minItems`/`maxItems` between 0 and 100; integral integer bounds and finite number bounds; printable-ASCII property names, `required` names and enum choices; at most three object levels (arrays add none), sixteen schema nodes on any path, 32 properties across the schema and 64 KiB normalized. Rejected: references and recursion, `anyOf`/`oneOf`/`allOf`/`not`, patterns, formats, string bounds, defaults, descriptions, integer enums, duplicate keys, non-strict JSON and contradictions such as `maxItems < minItems`.

Read failures by class. Exit 2 is a usage error naming the offending JSON pointer before any process spawns, so nothing was inferred. `native_protocol` (exit 5) means the helper is missing, older than the CLI, or returned a payload that failed validation: install the matching bundle rather than retrying or repairing. Refusal, timeout and context-capacity keep their existing errors and never produce a partial object. A conforming object is structurally valid, not semantically true.

Diagnostics are measured, not estimated: `models --model local --json` and `doctor --model local --json` expose `helper_protocol`, `helper_version`, `context_size` when available and the route's `capabilities`; complete responses add Apple's `usage` counts. Streaming usage stays absent, and no remaining Apple allowance or reset time is exposed. Use the window and usage to explain headroom; on a context-capacity error, agree a smaller scope with the user.

## Folders

Use `batch plan --input-dir <folder> --prompt-file <instructions> --model <concrete-tier> --output-dir <results> --job <manifest>`. Planning makes no model calls. Explain the resulting scope and apply an explicit user-authorized `--max-calls` budget to `batch run` or `batch resume`. A request to process N known inputs can authorize that bounded run; do not silently invent an unlimited budget.

Keep the manifest and output directory for resume. Existing successful results are checked and skipped. A failed or uncertain item needs investigation; do not add `--retry-failed` or `--retry-uncertain` automatically. This is a finite folder job, not a watcher.
