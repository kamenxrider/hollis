# Image generation live suite

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
