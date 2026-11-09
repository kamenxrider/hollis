# Hollis plugin 0.3.0

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="https://raw.githubusercontent.com/kamenxrider/hollis/v0.7.0/docs/assets/hollis-logo-dark.svg">
  <img src="https://raw.githubusercontent.com/kamenxrider/hollis/v0.7.0/docs/assets/hollis-logo.svg" alt="hollis — Your Mac has more to say." width="680">
</picture>

Cloud, Cloud Pro and the on-device model in Claude Code and Codex, now with the
complete **Hollis 0.7.0** runtime and its matching native helper in one package.
It replaces the withdrawn plugin 0.2.0.

- The skills describe runtime 0.7.0: guided output (`--schema`) on `local`, the
  widened schema subset, `doctor --model <route>`, and the measured
  `context_size` diagnostic. They still check `agent-context` and
  `models --model local --json` before offering a feature, so a newer or older
  Hollis already on PATH is used only for the capabilities it reports.
- Setup verifies the matched CLI and helper, preserves configuration and
  conversations, and keeps verified rollback candidates. Plugin 0.3.0 refuses to
  install an older runtime under its version.
- Downloads identify themselves as `hollis-plugin/0.3.0`.
- Packaging checks that the portable, Claude, Codex and marketplace versions agree.
- Cloud, Cloud Pro, Shortcuts On-Device, ChatGPT and image generation remain
  available through their existing bridges.

Runtime 0.7.0 upgrades the conversation database to schema v3. Runtime 0.4.0
cannot open it, so rolling the plugin back to a 0.4.0 runtime afterwards stops
with a newer-schema error (exit 10) and does not restore the old database. See
[the database upgrade and recovery steps](../cli.md#database-upgrades-and-recovery).

Apple controls cloud limits and availability. Explicit routes do not silently
switch models. Apple silicon, macOS 27 and Apple Intelligence are required;
fresh-account onboarding remains unqualified. Binaries are not Developer ID
signed or notarized.

## Install

The release publishes a ZIP, a checksum file and a runtime-provenance file.
Verify the archive with `shasum -a 256 -c hollis-plugin-0.3.0.sha256`, extract
it, and follow [the install guide](../../plugins/hollis/README.md#install),
giving the host the outer extracted folder. Installations from the main branch
or from 0.1.0 need a plugin update and a new host session to load the new
skills; check Hollis setup afterwards.

The archive includes both host entries, the shared skills, the runtime, the
native helper and nine bridges. It excludes private research, recordings and
brand working files, and is published with checksums, runtime provenance and
GitHub build attestations. Plugin and runtime versions are separate; runtime
0.7.0 remains the latest runtime release.

[Back to release notes](README.md)
