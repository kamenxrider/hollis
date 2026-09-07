# Live Apple test harnesses

These are developer tools, not installation dependencies. See the
[scripts index](../README.md) for the other harnesses and their prerequisites.

## Controlled host matrix

`host_matrix.py` runs one preselected case through the plugin's `run.sh`.
It uses Python 3's standard library and was used for the
[0.3.2 validation](../../docs/releases/v0.3.2.md#validation). It does not launch
Claude or Codex: the calling host invokes it, and host-visible image rendering
still needs a separate observation.

Before a live run, review a manifest with these fields:

- `kit`, `binary`, `binary_sha256`: absolute plugin/runtime paths and the
  reviewed runtime's SHA-256. The helper verifies both the bytes and the runtime
  selected by `setup.sh path` before dispatch.
- `output`: a private experiment directory, shared by both hosts for locking,
  pacing and receipts. Different directories do not share those protections.
- `env`: explicit test overrides, including `HOLLIS_PLUGIN_HOME` and
  `HOLLIS_STATE_DIR`. Verify the binary's resolved config path before starting;
  the harness inherits other environment variables and does not create an
  isolated installation for you. Do not put credentials in the manifest.
- `cases`: unique filename-safe `id`, `sequence`, explicit `model` for text and
  vision, and CLI `arguments` as an array. Include `--agent` for structured
  output and an explicit model/style; do not use `auto`. Optional `inputs`
  lists files to checksum, and `image_output` names an expected PNG.

Only after authorizing that experiment, dispatch one case:

```sh
python3 scripts/live-suite/host_matrix.py \
  --manifest /absolute/private/experiment.json --case cloud-01 --live
```

The shared lock serializes calls. The helper waits at least 10 seconds after
the previous completion, or 45 seconds before Cloud Pro. It journals before
dispatch, records timestamps, arguments, results, input hashes and PNG header
dimensions, and refuses to reuse a receipt. Rate limits, uncertain/unknown
outcomes and changed inputs block the affected sequence. Investigate abandoned
locks and blocked sequences instead of clearing them to force another attempt.
Read receipt outcomes: the harness can finish recording a failed Apple call
without itself exiting nonzero.

Full image decoding, reference similarity, photographic appearance and
instruction-following require separate checks. The sanitized release report
does not include the private local manifest and host transcripts; it is not a
ready-to-run experiment package. Preserve originals and prepare any public
reproduction bundle using the [evidence guide](../../EVIDENCE.md#evidence-storage-and-sharing).

Offline regressions require no Apple access or Pillow:

```sh
python3 -m unittest discover -s scripts/live-suite -p 'test_host_matrix.py'
```

## Earlier image-generation suite

This diagnostic suite includes a ChatGPT generation probe. Its six tested
settings are not six supported product styles: the released generation route
supports five styles, and ChatGPT generation remains unavailable through the
tested Shortcut route.

`image_generation_live.py` is the bounded post-release probe for the image
transport. It is intentionally opt-in and serial. A fresh run makes at most
28 model calls, waits at least 30 seconds after each completed call, stops at
the first failed case, and preserves every PNG plus a 0600 JSON report for
visual review.

The preferred mode tests one parameterized Shortcut across all six styles:

```sh
python3 scripts/live-suite/image_generation_live.py \
  --binary /absolute/path/to/hollis \
  --output /private/tmp/hollis-image-live-20260905 \
  --unified-bridge 'Hollis Image - Unified Probe' \
  --live
```

For comparison, the six style mappings can point to fixed, separately
configured Shortcuts. Supply them as JSON text or a private JSON file:

```sh
python3 scripts/live-suite/image_generation_live.py \
  --binary /absolute/path/to/hollis \
  --output /private/tmp/hollis-image-live-20260905 \
  --bridges-json /private/path/image-bridges.json \
  --live
```

The JSON object must contain exactly `any`, `animation`, `genmoji`,
`illustration`, `sketch`, and `chatgpt`. The report directory must not already
exist. The suite writes `HOLLIS_STATE_DIR` inside that directory, so it does
not modify the user's normal Hollis configuration or conversation database.
`--unified-bridge` and `--bridges-json` are mutually exclusive.

Use `--plan` to print the complete offline matrix. Use one or more exact
`--case` IDs, or `--start INDEX`, to run a smaller follow-up probe after a
route has been blocked; the report still records the full matrix and marks
unselected cases as skipped. There are no automatic retries or bridge
fallbacks.

The independent API probes use the explicit `Animation` style because the
live probe observed an initial `Any Style` API failure and repeated prompt
rejections for `Any Style` and `ChatGPT`. The six direct CLI style cases still
cover all six configured styles, including `Any Style` and `ChatGPT`.

The suite requires Python 3 and Pillow (`python3 -m pip install Pillow` in an
isolated environment if it is not already installed). Offline contract tests
are provider-free:

```sh
python3 -m unittest discover -s scripts/live-suite -p 'test_*.py'
```

The harness checks PNG framing and CRCs, full Pillow decoding, private output
permissions, response dimensions, checksums, canonical base64, conversation
replay shape, and the requested local crop/pad or exact-size metadata. It
cannot decide whether a generated picture is aesthetically correct; the
retained PNGs are the visual review evidence for that part.

Use `--prompt-set scenes` for realistic scene descriptions adapted from the
user-supplied prompting guide: a mechanical sea turtle, coffee station, robotic
arm, croissant character, woodland fox, and alpine cabin. The four previously
working styles run first. Both repetitions use the same scene, with the existing
output transforms. Geometry remains available for transport regressions.
The guide supplies creative examples; its model internals and sample APIs are
not treated as verified Apple specifications. Reports retain the exact prompts.
