# Post-release image research

A repeatable, bounded image suite for Hollis. All images and prompts are synthetic;
no personal documents or photos are used. The default only creates the plan and
fixtures. It does not invoke Hollis, Shortcuts, or a model.

Requires Python 3.11+ and Pillow (tested with Pillow 12.3.0; no dependency
installation is performed) for the original pixel-semantic suite. The
conversation acceptance runner uses only the Python standard library. Live
execution and process-cleanup tests require
permission to inspect numeric process IDs and sessions with `ps`.
Run from the repository root:

```sh
python3 -m unittest discover -s scripts/image-suite -p 'test_*.py'
python3 scripts/image-suite/image_suite.py
```

The live suite is explicitly opt-in. Build the binary immediately before running
and supply its absolute path, or let the suite build a private fresh binary:

```sh
go build -o /private/tmp/hollis-image-test ./cmd/hollis
HOLLIS_BIN=/private/tmp/hollis-image-test python3 scripts/image-suite/image_suite.py --live
```

Do not run another live Hollis test at the same time. This suite locks against
other copies of itself, but cannot coordinate unrelated processes. `--max-calls N`
limits the ordered plan to its first N cases (1–12); the default is 12. There are
no retries. Any nonzero exit, timeout, invalid JSON/model identity, or recognized
service/rate-limit signal stops every remaining live call. A doctor preflight
checks installed bridge inventory in the same isolated state before any model
call. Model answer mismatches
are recorded separately and do not trigger retries or prove a CLI defect.

| Cases | Models | What is checked |
| --- | --- | --- |
| Prompt plus pixels | Cloud, Cloud Pro, ChatGPT | Fresh prompt marker and randomly chosen color, shape, position |
| Blank negative control | Cloud, Cloud Pro, ChatGPT | Fresh prompt marker and recognition of a blank white image |
| Document OCR | Cloud, Cloud Pro, ChatGPT | Fresh random document code and unit count, not supplied in the prompt |
| Misleading context | Cloud | Actual pixels despite deliberately incorrect prompt note and filename |
| Attachment ordering | Cloud Pro | Different colors in two attachments, in supplied order |
| High-resolution JPEG | ChatGPT | 3840×2160 simple image, fresh marker and visual answer; dimensions, not photo-sized bytes |

Cases are serialized with 15 seconds after a completed Cloud/ChatGPT call and
45 seconds whenever either adjacent call uses Cloud Pro. Individual calls have a
30-second CLI timeout (the product default) and a 40-second harness deadline. Budget approximately
6–8 minutes, depending on provider response times; the timeout worst case is
longer. There are at most four calls per model and 12 calls in total.

Each run prints a private temporary report path. Its directory and fixtures use
0700/0600 permissions; JSON reports use 0600. Reports contain the exact synthetic
prompts, expected facts, image hashes/dimensions, raw model output, timings, binary
version/hash/Go build identity, Git revision, and OS version. They do not copy
credentials or the process environment. Keep reports private unless deliberately
reviewed for publication. A separate temporary `HOLLIS_STATE_DIR` is cleaned up
at the end; existing user conversations and configuration are not edited.

`transport=PASS` means the process returned valid response JSON identifying the
requested model. It does not prove that model's internal implementation.
`semantic=MATCH` means the answer matched the predefined marker and fact tokens.
`MISMATCH` is a research observation: extra prose can fail a strict check even
when the visual answer is correct. In particular, the misleading-context case is
one observation, not a general prompt-injection or vision robustness assessment.
One randomized sample per case establishes only that sample's result.

A zero live exit code means all attempted transports completed; it does **not**
mean all semantic checks matched. Read the separate report fields. Offline unit
tests validate plan bounds, answer independence, permission modes, fail-stop
behavior, strict matching, spacing, and isolated state using fake invocations.

## Conversation acceptance runner

`acceptance_suite.py` is a separate, smaller gate for image conversations. Its
default `plan` command is offline. The opt-in runner can use a real PTY for
interactive `/image`, resume a CLI image conversation with a fresh output path,
send the actual first PNG as a final-user reference through both
`/v1/chat/completions` (`image_url`) and `/v1/responses` (`input_image`), and
exercise the standalone `/v1/images/generations` `reference_image` field.
Both conversation cases also replay the latest assistant image on a third turn.
The full acceptance plan contains 16 generations; select only the cases needed. It
shares the live lock name with this suite and enforces a minimum **three-second
gap after each generation completes**. Reports and output directories must be
private and fresh. The root test coordinator owns live invocations; do not run
this from a sub-agent while another live image check is active.

```sh
python3 scripts/image-suite/acceptance_suite.py plan --json

python3 scripts/image-suite/acceptance_suite.py run \
  --binary /absolute/path/to/hollis \
  --state-dir /private/tmp/hollis-acceptance-state \
  --output-dir /private/tmp/hollis-acceptance-output \
  --settle-seconds 3 \
  --case interactive-first \
  --case interactive-sequence \
  --case cli-followup
```

The API reference cases are selected explicitly and require a running local
server. They decode every returned PNG, compare its SHA-256 with response
metadata, and require `reference_image_sent` plus the echoed reference checksum:

```sh
python3 scripts/image-suite/acceptance_suite.py run \
  --binary /absolute/path/to/hollis \
  --state-dir /private/tmp/hollis-acceptance-api-state \
  --output-dir /private/tmp/hollis-acceptance-api-output \
  --api-base-url http://127.0.0.1:1978 \
  --settle-seconds 3 \
  --case api-chat-completions \
  --case api-responses \
  --case api-reference-generations
```

The API cases additionally require `--api-base-url`; an optional
`HOLLIS_API_TOKEN` supplies a bearer token without placing it in the report.
`dynamic-reference` is intentionally gated on the landed chat
`--image-reference path|auto` contract (the standalone image command uses
`--reference-image`) and a supplied `--reference-image` path to the runner. A
successful transport and valid PNG still require human visual review for
continuity.

The process helper also has real local subprocess tests. Timeout, Ctrl-C, and
SIGTERM cleanup covers children in separate process groups within the invocation's
owned session. Each invocation uses a private temporary directory for prompt
files, which is removed even when Hollis is forcibly stopped. If process
inspection is denied, execution fails before launching Hollis. These tests
invoke synthetic sleeping processes, never a model.
