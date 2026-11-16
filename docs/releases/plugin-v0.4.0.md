# Hollis plugin 0.4.0

**Published 2026-11-16.** The plugin pins published runtime 0.7.1 and its
matching native helper; the authenticated asset pins are checked in.

This update targets runtime 0.7.1, its matching native helper (protocol 4)
and nine bridges: four text, four JSON and one image. It adds guided setup,
selected-route diagnostics and instructions for structured output and native
capabilities. Setup and imports do not prove successful inference.

Install or update through your host using the
[plugin quick start](../../plugins/hollis/docs/quickstart.md). No automatic
install hook runs. Existing configuration, conversations and imported
Shortcuts are preserved. Runtime rollback does not undo database migrations;
read [database upgrades and recovery](../cli.md#database-upgrades-and-recovery)
before changing runtimes.

The helper is ad-hoc signed, not Developer ID signed or notarized.
Source setup checks pinned downloads; release packaging authenticates runtime
provenance before bundling. Published downloads and first-account approvals
still require separate verification.

[Back to release notes](README.md)
