# Hollis unreleased changes

These notes describe the next release. No version number or date is announced
yet. Runtime and plugin versions stay separate, and each ships with its own
archive and evidence.

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

## Native local: protocol 3, helper 0.6.0 and guided output

`hollis respond --schema` asks the on-device model for a JSON object that
conforms to a supported schema subset. Guided output needs the `local` route, a
protocol-3 helper (`hollis-native` 0.6.0 matches this source) and
`capabilities.guided_output: true` from `hollis models --model local --json`.

Protocol 3 is one coordinated bump for the capabilities landing in this release
— document extraction, image attachments and native tool calling — so released
users move through one compatibility matrix instead of three. The helper accepts
protocols 1, 2 and 3: a newer helper keeps serving an older CLI, while this CLI
speaks protocol 3 only and refuses another helper with `native_protocol` (exit
5, HTTP 502) rather than degrading to plain text. The helper's status event now
carries `capabilities`, and a request that needs a capability the helper did not
advertise is refused by name as `unsupported_capability` (exit 5, HTTP 502) —
a component fault, not a caller mistake.

The subset accepts an object root with `additionalProperties: false` on every
object; `string`, `integer`, `number` and `boolean` properties; `required`
naming declared properties; string enums; arrays with `items` and
`minItems`/`maxItems`; integral integer bounds and finite number bounds;
printable-ASCII names; at most three object levels, sixteen schema nodes on any
path, 32 properties and 64 KiB normalized. References, recursion, composition
keywords, patterns, formats, string bounds, defaults, descriptions, integer
enums, duplicate keys and contradictions are rejected before any process
spawns, with exit 2 naming the offending JSON pointer. Nothing is inferred in
that case.

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

## Plugin skills

Plugin skills describe the setup, diagnostics and guided-output behavior of the
runtime they ship with. The skill updates for these features land with the
matching plugin and runtime pairing: a plugin installed against an older
runtime does not gain the new commands or capabilities until both are updated
together. See [the plugin guide](../plugin.md).

[Back to release notes](README.md)
