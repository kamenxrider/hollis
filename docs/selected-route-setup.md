# Selected-route setup and diagnostics

Use a concrete route to inspect only the capability you need:

```sh
hollis doctor --model cloud-pro --json
hollis doctor --model local --agent
hollis doctor --model image --json
hollis setup --model cloud-pro --agent
```

These checks never request inference, import Shortcuts, download artifacts,
change permissions or change Apple settings. `auto` is a routing policy and
is not a setup route. Image is a setup capability, not a text model ID.

`doctor --model local` checks only native availability. It does not list
Shortcuts or read bridge configuration. A selected Shortcut route does not
check the native helper or fail because unrelated bridges are absent.
Unscoped `hollis doctor` retains its existing full-report exit behavior.
Selected doctor and setup JSON support `--select` under the same declared
result schemas and strict unknown-field rules as other data commands.

## Evidence and verification

Availability is not artifact authentication or successful inference.
Selected doctor reports `artifact_verification: "unknown"` and
`inference_tested: false`, even when setup prerequisites appear ready.

The setup planner runs only after the plugin installation owner supplies a
fresh verified-runtime record. Without that record, standalone, source,
`go install`, direct and Homebrew invocations return an action-needed
`runtime_unverified` result. They can still use selected doctor for independent
read-only availability diagnostics. Standalone receipt verification is not
implemented by this change.

The internal plugin adapter selects its local runtime-record producer explicitly.
The CLI invokes that producer through an argument array, with a deadline and
bounded stdout, and checks its contract version, exit status, executable path
and build version. It ignores the producer's stderr. No persisted record or
environment override grants verification.

This is a local trust boundary: the caller selects the producer. It protects
against stale or mismatched records, not a malicious local user or a compromised
plugin. It is not independent authentication of arbitrary producer code.

The internal repeatable `--pending-import` flag is a caller assertion for a
selected route. It can only require approval, never establish readiness or
artifact verification.

## Plan results

Plans carry `schema_version: 1`, `source`, `route`, `status`, `reason_code`,
`message`, explicit `actions`, bridge facts and `inference_tested`.

| Status | Meaning |
| --- | --- |
| `ready` | Observed setup prerequisites are ready; inference is still untested |
| `import_required` | Successful discovery established that the selected bridge is absent |
| `approval_required` | An explicit pending import needs user approval, or a discovered image bridge needs explicit configuration |
| `model_not_ready` | A valid native availability check reported that the model is unavailable |
| `unknown` | Verification, support, configuration or discovery could not establish readiness |

`source: planner-v1` identifies a plan using a verified-runtime record.
`source: observation-v1` identifies independent doctor observations without an
artifact authentication claim. `source: bootstrap` means runtime verification
is required before planning.

A failed listing does not prove a bridge is missing or that an Apple approval
is pending. Configured names and UUIDs are preserved when discovery cannot
verify them. No repair or duplicate import happens automatically.

For native model-not-ready results, check Apple Intelligence enablement, required
model downloads, and Siri/macOS language consistency. A language mismatch is one
possible cause, not a diagnosis. Hollis neither reads an invented settings key
nor changes language settings.

Setup effects remain separate, explicit plugin install/import operations.
Image generation requires an unlocked session. A successful first inference
must be separately authorized; this change performs none.

## Exit status

Selected doctor/setup use exit 0 for ready prerequisites, 2 for invalid
selection, 3 for a confirmed missing selected bridge, 5 for transport/discovery
failure and 10 for unknown, unsupported or action-needed configuration.
Native probe timeouts and protocol failures retain their existing classifications.
JSON/agent diagnostic errors remain top-level even when result fields are selected.

Current-Mac offline fixtures verify these contracts. They do not establish
fresh-account onboarding, authenticated published downloads or live inference
qualification.
