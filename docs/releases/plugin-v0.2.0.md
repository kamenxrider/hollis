# Hollis plugin 0.2.0

**This release was withdrawn.** Its archive is no longer published. Use the
published [plugin 0.1.0](https://github.com/kamenxrider/hollis/releases/tag/plugin-v0.1.0),
which bundles runtime 0.3.3 without native local. Plugin 0.4.0 will follow after
the runtime 0.7.0 pin update. The notes below record what 0.2.0 contained.

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="https://raw.githubusercontent.com/kamenxrider/hollis/v0.4.0/docs/assets/hollis-logo-dark.svg">
  <img src="https://raw.githubusercontent.com/kamenxrider/hollis/v0.4.0/docs/assets/hollis-logo.svg" alt="hollis — Your Mac has more to say." width="680">
</picture>

The package includes Cloud and Cloud Pro in Claude Code and Codex, along with
the complete **Hollis 0.4.0** runtime and its native-local helper.

- Explicit `local` selects Apple’s on-device SDK without a Shortcut import.
- Human terminal text streams; agent and JSON output stays complete. Complete local responses report measured token usage; streamed usage is not promised.
- The runtime includes exact-byte text transport and cancellation cleanup.
- Setup verifies the matched CLI/helper, preserves configuration and conversations, and retains verified rollback candidates.
- Cloud, Cloud Pro, Shortcuts On-Device, ChatGPT and image generation remain available through their existing bridges.

Apple controls cloud limits and availability; testing encountered hours-long
interruptions. Explicit routes do not silently switch models. Apple-silicon/macOS
27 and Apple Intelligence are required; fresh-account onboarding remains unqualified.
Binaries are not Developer ID signed or notarized.

The release published a ZIP, a checksum file and a runtime-provenance file.
Installing meant verifying the checksum with
`shasum -a 256 -c hollis-plugin-0.2.0.sha256`, extracting the archive and
following the versioned install guide, then giving the host the outer extracted
folder. Existing 0.1.0 installations needed a plugin update and a new host
session to load the new skills, with Hollis setup checked afterward. Those steps
describe the withdrawn release and do not apply now.

The archive included both host entries, shared skills, runtime, native helper and
five bridges. It excluded private research, recordings and brand working files,
and was published with checksums, runtime provenance and GitHub build
attestations. It targeted runtime 0.4.0; plugin and runtime versions are separate.

## Withdrawn

The 0.2.0 archive, its checksum file and its runtime provenance file were
removed from the releases page, and the `plugin-v0.2.0` tag was deleted, so the
steps above no longer apply and no 0.2.0 versioned link resolves.
Use the [0.1.0 release](https://github.com/kamenxrider/hollis/releases/tag/plugin-v0.1.0)
and its versioned instructions instead. Current plugin 0.4.0 source setup is
blocked by the old runtime lock, not an available replacement for 0.2.0.
See [the pending plugin guide](../../plugins/hollis/README.md).
The withdrawn tag name will not be reused.
