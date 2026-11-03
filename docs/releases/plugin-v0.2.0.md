# Hollis plugin 0.2.0

**This release was withdrawn.** Its archive is no longer published. Install
from the repository's main branch instead; a versioned plugin release will
follow. The notes below are kept as the record of what 0.2.0 contained.

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="https://raw.githubusercontent.com/kamenxrider/hollis/v0.4.0/docs/assets/hollis-logo-dark.svg">
  <img src="https://raw.githubusercontent.com/kamenxrider/hollis/v0.4.0/docs/assets/hollis-logo.svg" alt="hollis — Your Mac has more to say." width="680">
</picture>

Cloud and Cloud Pro in Claude Code and Codex, now with the complete **Hollis 0.4.0**
runtime and its native-local helper in one package.

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
attestations. Runtime 0.4.0 remains the latest runtime release; plugin versions
are separate.

## Withdrawn

The 0.2.0 archive, its checksum file and its runtime provenance file were
removed from the releases page, and the `plugin-v0.2.0` tag was deleted, so the
steps above no longer apply and no versioned link resolves. Install from the
repository's main branch with
[the current install guide](../../plugins/hollis/README.md#install). Setup from
main downloads the same pinned, checksum-verified runtime 0.4.0 assets, so no
capability described above is lost. The tag name will not be reused; the next
plugin release gets a new version and tag.
