# Plugin skill update: release gating and pending slots

Maintainer note for the plugin release. It is not packaged with the plugin and
is not skill text.

The shared skills in `plugins/hollis/skills` describe the next runtime. The
plugin still pins runtime 0.4.0 (native protocol 1) in
`plugins/hollis/runtime.lock.json`, and its version stays 0.2.0, until the
runtime is released. Re-pin both together, refresh
`scripts/plugin/testdata/agent-context-<version>.json` from the newly pinned
runtime's `agent-context`, and empty `NEEDS_NEW_RUNTIME` in
`scripts/plugin/test_skill_commands.py`; that test fails until the fixture
matches the lock.

## Instructions that need the new runtime

`test_skill_commands.py` checks the commands in `plugins/hollis/skills` and
`plugins/hollis/docs` against the 0.4.0 `agent-context`. Each instruction the
pinned runtime cannot run must have its gate sentence in the same paragraph,
list item or table row:

| Instruction | Required gate | 0.4.0 behavior |
| --- | --- | --- |
| `respond --schema` (guided output) | `agent-context` lists `--schema` | unknown flag, exit 2; no guided output |
| `respond --code` | `agent-context` lists `--code` | unknown flag, exit 2 |
| `respond --prompt-file -` | `agent-context` describes `--prompt-file -` | `-` is read as a file name |
| `doctor --model <route>` | `agent-context` lists `--model` on `doctor` | unknown flag, exit 2; plain `doctor --json` works |

Also runtime-dependent and described as a check, not run as a command:

- Bare prompts and near-miss refusal: the skills tell agents to always name the
  command, which works on both runtimes.
- The runtime's own selected-route setup (`runtime_unverified` for Homebrew):
  `setup.sh status <route>` falls back to the legacy view on 0.4.0.
- `context_size` and `capabilities.guided_output` in `models` output, and the
  matched-bundle helper check (0.4.0 reports protocol 1 and no `guided_output`).
  The skills check `available: true` with the capability, not a protocol number.
- HTTP 429 `rate_limited`: the 0.4.0 server sends `Retry-After: 1`, newer
  servers send none; the recipe says to stop either way.
- `unsupported_capability` (exit 5) and protocol-3 helper matching.
- HTTP guided JSON (`response_format`, `text.format`) in the "model behind a
  tool" recipe.

Each of these is written conditionally in the skills ("when `agent-context`
lists ...", "when `models` reports ..."), so an agent on the pinned runtime
checks before using it. Everything else in the skills, including the
`run.sh respond --agent/--json` script recipe and the `hollis serve` endpoint
recipe without a schema, works on 0.4.0.

`setup.sh status <route>` previously reported `runtime_unverified` for a verified
0.4.0 install because 0.4.0 rejects the root `--model` flag before printing JSON.
The adapter now treats that exact refusal as a runtime without a `setup` command
and uses the labelled legacy view, as it already did for an `unknown command`
error.

## Pending skill slots

Add each when the feature is merged, checked against the `agent-context` of the
runtime that ships it. Until then the skills must not mention them.

- TODO(pdf): PDF documents and OCR for `respond --file`, `--pages` limits and
  what leaves the Mac on Cloud routes with extracted text.
- TODO(local-images): image input on `local`; the skills currently say On-Device
  and `local` do not accept images.
- TODO(tool-calling): native tool calling on `local` and the HTTP `tools` field;
  the endpoint recipe currently says `tools` is refused.
- TODO(mcp): MCP servers, the approval contract and what tools receive; the
  plugin must never attach MCP servers to `hollis serve`.

Release check: install the plugin from the source checkout and from the plugin
archive, and confirm both resolve the same runtime version and native protocol
as `runtime.lock.json`.
