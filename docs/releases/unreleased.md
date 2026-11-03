# Hollis unreleased changes

These notes describe the next release. No version number or date is announced
yet. Runtime and plugin versions stay separate, and each ships with its own
archive and evidence.

## First-run diagnostics

Model discovery includes unavailable routes and marks the configured default.
Native diagnostics distinguish unsupported platforms, unknown availability,
helper identity mismatch and malformed protocol data without extra CLI probes
on `models --model local`. Doctor suggests a next action without diagnostic
loops or selecting another installer. Native repair guidance preserves the
original installation source and the matching CLI/helper pair.
Empty chat lists and no-match searches include human-only next-step hints.

## Embedded job recipes

`hollis examples` prints copyable, job-organized recipes without running them.
The same embedded guide supports human, JSON and agent output, and every
displayed shell block runs verbatim in offline tests with synthetic model
runners. See [the recipes](../../examples/recipes.md) and
[their test scope](../testing.md#copyable-job-recipes).

## Database schema v3

> **Changing back to an older Hollis binary does not undo this upgrade.**
> Runtime 0.4.0 supports schema v2 only and refuses a v3 database with a
> newer-schema error and exit 10. A plugin pointer, a reinstalled package or an
> older executable does not downgrade the data or restore a backup. Recover
> deliberately, with the [database upgrade and recovery steps](../cli.md#database-upgrades-and-recovery).

Schema v3 adds an indexed UTC nanosecond key for chronological chat listing.
Readable timestamps and stored transcripts are preserved. Malformed timestamps
or instants outside the signed 64-bit nanosecond range (approximately September
1677 to April 2262) stop the upgrade instead of inventing a timestamp.

The migration is atomic: schema changes, backfills, search-index recovery and
the schema-version update commit together, and a failed migration rolls back its
uncommitted changes. Before migrating or repairing an existing database, Hollis
retains a private `.hollis.db.backup-*` SQLite snapshot in the state directory.
The snapshot includes committed WAL data at that moment; it does not promise to
capture later writes from an already-running process. The migration lock
coordinates compatible Hollis openers, not simultaneous old and new runtime use.

## Strict field selection

`--select` now validates against the command's declared schema before any model
call or other command work. Unknown fields and malformed paths fail with a
usage error, exit 2, instead of being silently omitted. This is an intentional
correction to earlier lenient behavior; valid optional fields may still be
absent, and fields that occur only in later array rows stay valid.

## Selected-route setup and diagnostics

`hollis setup --model <route>` plans setup for one concrete route, and
`hollis doctor --model <route>` inspects only that route. Both are described in
[selected-route setup and diagnostics](../selected-route-setup.md).

Plans report a route-scoped status such as `ready`, `import_required`,
`approval_required`, `model_not_ready` or `unknown`, with a reason code, explicit
actions and `inference_tested: false`. Selected doctor reports
`artifact_verification: "unknown"`. An installation that cannot present a fresh
verified-runtime record, including standalone, source, `go install`, direct and
Homebrew invocations, receives an action-needed `runtime_unverified` result and
can still use selected doctor for read-only availability diagnostics.

These checks never request inference, import Shortcuts, download artifacts,
change permissions or change Apple settings. A ready prerequisite is not a
successful inference, and a failed listing does not prove a bridge is missing.

## Bounded Shortcuts output

Every Shortcuts run has an effective deadline and a bounded output capture. A
run whose stdout exceeds the 1 MiB capture cap is refused as a transport
failure, exit 5: the response cannot be delivered faithfully, so Hollis does not
return a partial one. `shortcuts list` uses the same 1 MiB bound and reports an
oversized listing as a transport failure, exit 5, rather than proving a bridge
missing or inventing bridge names. Stderr keeps a bounded head and tail so a
late diagnostic can still classify the failure. See
[Shortcuts transport](../shortcuts.md#how-it-works).

## Native local: protocol 3, helper 0.7.0 and guided output

`hollis respond --schema` asks the on-device model for a JSON object that
conforms to a supported schema subset. Guided output needs the `local` route, a
protocol-3 helper (`hollis-native` 0.7.0 matches this source) and
`capabilities.guided_output: true` from `hollis models --model local --json`.

Protocol 3 adds negotiation, capability advertisement and named refusal. This source
supports caller tools on local; it does not advertise image support statically.

`hollis respond --file` now accepts PDFs. The paired native helper reads
them on this Mac through the protocol-3 `extract` operation: the text layer per
page, with on-device OCR for pages that have none, and no model call. Content
decides the route, not the extension. `--pages` selects pages of one PDF.
New error codes: `invalid_document`, `extract_no_text`, `unsupported_media`,
`extract_media_mismatch` and `page_out_of_range`; extracted text over 128 KiB
is `context_capacity` with both numbers. See [text documents](../cli.md#text-documents).
The helper accepts request formats 1, 2 and 3 but stamps all events with 0.7.0;
older CLIs reject that version. CLI and helper ship as one matched bundle, and
a mismatched pair fails closed with a clear error and matching-bundle remedy:
`native_protocol` (exit 5, HTTP 502), never a plain-text downgrade. The helper's
status event now carries `capabilities`. A request needing a capability that
the helper did not advertise is refused by name as `unsupported_capability`
(exit 5, HTTP 502), a component fault, not a caller mistake.

The subset accepts an object root and closed objects (`additionalProperties`
`false` or omitted, which means `false`); `string`, `integer`, `number` and
`boolean` properties; `required` naming declared properties; string enums;
arrays with `items` and `minItems`/`maxItems`; integral integer bounds and
finite number bounds; printable-ASCII names; at most three object levels,
sixteen schema nodes on any path, 32 properties and 64 KiB normalized.

It also accepts the keywords real tool and API schemas carry. Annotations
(`description`, `title`, `$schema`, `$id`, `$comment`, `default`, `examples`,
`deprecated`, `readOnly`, `writeOnly`, `x-` keys) are ignored for validation,
and `description` or `title` is passed to the model. String constraints
(`format`, `minLength`, `maxLength`, `pattern`, `contentEncoding`,
`contentMediaType`) are accepted and **not enforced**: Hollis checks the type
only. A nullable `anyOf`/`oneOf` property (one schema plus `null`) is treated as
that schema, optional, with `null` accepted. **A `default` is never applied.**
This is a compatible relaxation: every schema accepted before is still accepted
and validated identically.

References, recursion, `allOf`/`not`, other unions, `const`,
`additionalProperties` other than `false`, integer enums, duplicate keys and
contradictions are rejected before any process spawns, with exit 2 naming the
offending JSON pointer. Nothing is inferred in that case.

Guided output is complete-only: `--schema` refuses `--stream`, `--image` and
every route except `local` before generation. A returned object is re-validated
independently, and a failure is an error rather than a partial or repaired
object. Structural validity does not make the content true.

Complete native responses report Apple's measured `usage`, and `models`/`doctor`
report `context_size` when the helper provides it. On 2026-11-01 one Apple
Silicon Mac reported an 8,192-token window for the on-device model. That is a
dated observation of one setup, not a guarantee for other builds, models or
future requests. Streaming usage remains unreported.

## Guided JSON output over HTTP

The same guided path is reachable through `/v1/chat/completions`
`response_format` and `/v1/responses` `text.format`, with OpenAI's `json_schema`
shape. Only `model: "local"` supports it, and there is no silent fallback to
plain text. The schema is validated before capacity is taken or a helper
spawns: an unsupported schema is **400** `invalid_schema`, and `stream: true`,
image input, a non-local model or an unsupported `response_format` type is
**400** `unsupported_parameter`. The returned object is a JSON string, re-validated
by Hollis, and a validation failure is **502** `native_protocol`. Details are in
[guided JSON output](../api.md#guided-json-output).

## Caller-provided tools on local

This source adds `hollis respond --tools` and tools on both HTTP endpoints,
for `local` only. Chat Completions uses nested `function` definitions;
Responses uses flat function definitions. Schemas must fit the supported
subset, and every returned call is validated. The caller executes the tool
and sends back its result; Hollis never executes caller tools.

When the available model supports tools, the helper advertises
`tools: {supported: true, tool_calling_mode: true, native_definitions: true}` and
maps protocol-3 `tool_calling: "allowed"` or `"disallowed"` to SDK generation
options. Unsupported modes, including `"required"`, are refused as
`native_failed`. Definitions now travel as native transcript metadata, with
separate fixed format instructions and no executable SDK tools. A non-generative
status process checks the matched version and capability before each tool
generation. Metadata counts toward byte limits and the single native token
preflight; conversion or capability failures never fall back to text-only
definitions. Plain and `none` paths do not attach definitions or instructions.
Tool requests are complete-only, allow one validation
correction within a three-call loop budget, and refuse images or guided output.
Repairs, final answers and `none` share that budget with visible prior tool-call
turns. Hidden corrections from earlier stateless requests are not reconstructible;
the cap is not a global cross-request quota.
See [tools over HTTP](../api.md#tools-on-local) and
[CLI tools](../native-local.md#tools).

These are unreleased source capabilities, not an upgrade to existing installed
bundles or a guarantee that a model will call the right tool.

## Native helper mismatch over HTTP

At startup `hollis serve` asks the paired helper for its status without
generating anything. When the answer names a different protocol or version,
every `local` request, guided or plain, complete or streamed, is **502**
`native_protocol` with a message that the helper does not match this Hollis
version and a pointer at the matching bundle. A missing or unavailable helper
stays **400** `model_unavailable` at admission; a helper that fails mid-run is
**503** `model_unavailable`. See
[native local helper mismatch](../api.md#native-local-helper-mismatch) and
[failure meanings](../errors.md).

## AI pull-request review

The review workflow sends a bounded unified diff to `gpt-6-luna` with `xhigh`
reasoning effort over the documented Chat Completions shape. Text only, no
tools, no model command executor. A manual smoke run is available from the
Actions tab through
[`ai-review-smoke.yml`](../../.github/workflows/ai-review-smoke.yml); it runs
only on the default branch, makes a single synthetic request, keeps the
response as an artifact and never touches a pull request. See
[the review helper](../../scripts/ai-review/README.md).

## Background API and shell completions

A LaunchAgent template at `packaging/launchd/` runs `hollis serve` at login on
loopback with a token read from a private file; the plist never holds a token
value. [Running the local API in the background](../serve.md) covers loading,
unloading, restart behavior and the log location. Nothing installs or loads it
automatically. The installer's final message now names the completion setup
help for the login shell; it does not write completion files. See
[shell completions](../cli.md#shell-completions).

## Plugin skills

Plugin skills describe the setup, diagnostics and guided-output behavior of the
runtime they ship with. The skill updates for these features land with the
matching plugin and runtime pairing: a plugin installed against an older
runtime does not gain the new commands or capabilities until both are updated
together. See [the plugin guide](../plugin.md).

## Release checklist: README quick start

The README quick start checks native local with `hollis models --model local`
because the installer still pins runtime 0.4.0, which has no `doctor --model`.
When the installer pins this release, make `hollis doctor --model local` the
first command in quick start step 2, keep `models --model local` as the
alternative, and remove the sentence saying the installed runtime lacks it.
Add rows to the README's "What leaves your machine" table only for features
that ship in this release, stating what the shipped code sends and to which route.
[Back to release notes](README.md)
