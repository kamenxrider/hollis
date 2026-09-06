# Hollis workflows

All examples below are arguments to `bash "$KIT/scripts/run.sh"`; replace paths with actual absolute paths. Discover the complete flags using `agent-context`. Do not copy illustrative filenames into a live request without checking them.

## Documents and pictures

- Text documents: `respond --agent --model cloud --prompt-file instructions.txt --file brief.md --file notes.txt`. Only regular UTF-8 `.txt`/`.md` documents; no PDF. Instruction plus documents must fit 128 KiB. Documents cannot be mixed with images in one call.
- Image understanding: `respond --agent --model cloud-pro --image screenshot.png --prompt-file question.txt --timeout 120s`. Cloud and Cloud Pro accept repeated PNG/JPEG images; ChatGPT accepts one. On-Device and auto are not supported for this input.
- Current CLI image bounds are 64 MiB / 64 million pixels per input. Use real regular files; the runtime rejects arbitrary symlinks. Do not bypass its checks. HTTP limits are different and do not apply to these CLI examples.

## Generate and revise

`image generate 'A tiny greenhouse on a brass turtle, detailed illustration' --style illustration --output /absolute/new-image.png --agent --timeout 120s`

The five supported selections are `animation`, `illustration`, `sketch`, `genmoji`, and `any`. Default to `animation` when the user has no style preference, matching Hollis. `any` does not guarantee photographs. ChatGPT **text and image understanding** work; ChatGPT **image generation** is unavailable through the tested Shortcut route.

For a follow-up: keep the previous returned local file path, then call `image generate` with the requested change and `--reference-image /absolute/previous.png`. This guides a new image; it does not promise identical subjects or pixel-level editing. Use a new output filename each time. An explicit fresh-scene request omits the reference.

Ratios and sizes are local processing after generation: `--aspect-ratio 16:9 --fit crop`, or `--size 1200x800 --fit pad`. Preserve native output when neither is requested. If the user requests a ratio without specifying a fit, explain crop versus padding and ask which preserves their intent. These flags do not control Apple's native sampling. No seed control is exposed.

For an explicitly persistent conversation, use `chat --agent --model cloud` initially and retain its returned ID. Image turns use `chat --continue <id> --generate-image --image-style illustration --output <new.png> <prompt>`, with `--image-reference auto|none|<path>` as appropriate. Image-understanding history is not supported by Hollis chat; use explicit `respond` calls with selected context instead.

## Folders

Use `batch plan --input-dir <folder> --prompt-file <instructions> --model <concrete-tier> --output-dir <results> --job <manifest>`. Planning makes no model calls. Explain the resulting scope and apply an explicit user-authorized `--max-calls` budget to `batch run` or `batch resume`. A request to process N known inputs can authorize that bounded run; do not silently invent an unlimited budget.

Keep the manifest and output directory for resume. Existing successful results are checked and skipped. A failed or uncertain item needs investigation; do not add `--retry-failed` or `--retry-uncertain` automatically. This is a finite folder job, not a watcher.
