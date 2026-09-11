# Hollis plugin 0.2.0

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

Download the ZIP and checksum below, verify with
`shasum -a 256 -c hollis-plugin-0.2.0.sha256`, then extract the archive and follow
[the versioned install guide](https://github.com/kamenxrider/hollis/blob/plugin-v0.2.0/plugins/hollis/README.md#install).
Give your host the outer extracted folder. Existing 0.1.0 installations need a
plugin update and a new host session to load the new skills; ask Hollis setup to
check the runtime afterward.

The archive includes both host entries, shared skills, runtime, native helper and
five bridges. It excludes private research, recordings and brand working files.
Checksums, runtime provenance and GitHub build attestations accompany the package.
Runtime 0.4.0 remains the latest runtime release; plugin versions are separate.
