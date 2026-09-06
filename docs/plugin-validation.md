# Plugin 0.1.0 validation

The canonical [validation report](../plugins/hollis/docs/validation.md) ships
inside the plugin, alongside the [recorded demonstration](../plugins/hollis/examples/recorded-demo.md).
It distinguishes successful live calls, installer fixtures, remaining clean-account
acceptance work and the separate archive-attestation/publication step.

Private raw receipts and host transcripts from this run remain under
`docs/dev/plugin-validation-2026-09-06/`, which Git ignores. The packaged
`examples/recorded/results.json` contains selected actual outputs and sanitized
timings. It contains no host configuration, credentials or unrelated conversation.

Maintainers can repeat provider-free checks with
`python3 -m unittest discover -s scripts/plugin -p 'test_*.py'`.
`scripts/plugin/live_validate.py` requires explicit `--live`, runs one serial
phase at a time and records dispatch before execution. It stops on the first
error and refuses automatic repeats of failed or uncertain records.
`scripts/plugin/host_demo.py` records real Claude/Codex turns; follow the demo
steps and serialize it with every other Apple test.
