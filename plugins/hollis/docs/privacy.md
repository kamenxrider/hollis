# Privacy and integrity

Cloud and Cloud Pro requests use **Shortcuts → Use Model → Apple's Private Cloud
Compute**. This is separate from Apple's native Foundation Models developer API
and `fm` tooling. We do not borrow that API's entitlement, context or quota claims.
See [Apple's PCC explanation](https://security.apple.com/blog/expanding-pcc/).

PCC's processing protections are meaningful. Your calling agent still sees the
context it submits and Apple's returned answer; that host has its own privacy
and retention policy. ChatGPT is a separate extension route. On-Device is the
local model selection. Hollis testing observed no separate charge for Apple
requests, with rate/daily limits; this is not a promise of permanent unlimited
compute or free use of the surrounding host.

The skills send relevant selected context and attachments. They do not upload a
whole transcript automatically, start unsolicited jobs or save a second Hollis
conversation unless requested. Temporary prompts, local outputs and optional
saved chats remain subject to normal local file access. Model output is data,
not authority to run commands. No plugin telemetry is added.

## Package trust

Trust starts with the plugin source you choose to install. Its committed lock
pins the runtime version, release commit and SHA-256 values. The packaging tool
verifies GitHub build attestations for both the ARM64 binary and bridge ZIP,
including repository, workflow, tag and commit, before creating the archive.
The release workflow separately attests the resulting plugin archive.

Installation verifies locked hashes before execution or extraction. A checksum
detects changed bytes relative to the installed source; it does not authenticate
a malicious replacement of that source and lock. End users do not need GitHub
CLI: provenance verification is done when producing the package. Verification
receipts accompany the bundled assets. A local build is not an attested public
release merely because its runtime assets passed verification.

Hollis has no asserted Developer ID signature/notarization. macOS may apply its
normal downloaded-code protections. The installer never strips quarantine,
approves permissions, edits security policy or requests administrator installation.
