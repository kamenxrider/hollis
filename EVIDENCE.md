# Capability boundaries and evidence policy

Hollis exposes the separate Cloud and Cloud Pro choices available in Apple's
Shortcuts Use Model action. Existing `on-device` and `auto` routing is unchanged.
The explicit `local` route uses the native Foundation Models SDK independently
of Shortcuts; see [native local](docs/native-local.md).

## What each route promises

- Shortcuts routes return complete responses. They do not expose measured token
  usage, streaming or a native tool channel through Hollis.
- Native local supports incremental text and measured complete-response usage.
  Streaming token usage is not promised.
- A requested route is not an attestation of Apple's private backend version.
- Exact prompt transport does not guarantee a correct answer or acceptance of
  a coding request. The [transport correction](docs/releases/v0.4.0.md) explains
  the scope of the repair.
- Image reference submission does not establish reliable reference-pixel use
  or identity preservation. See [image references](docs/image-references.md).
- Setup and bridge discovery do not prove inference. First-use consent and
  availability depend on the user's Mac and Apple configuration.

![Shortcuts Use Model choices](results/img/use-model-picker-26A5421a.png)

This selected product screenshot shows the Cloud, Cloud Pro, On-Device and
ChatGPT picker. It contains no prompt, response or personal account information.

## What belongs in a release

Public source includes implementation, user documentation, selected product
artwork and synthetic regression tests. Live research harnesses, per-call
records, actual model answers, host transcripts, recorded demo runs and internal
review reports stay private. Publishing any of that material requires separate
content review and authorization.

The [publication gate](scripts/release/README.md) checks an explicitly reviewed
source manifest and the actual package contents. Public runtime checksums and
build provenance describe the artifacts; they do not replace live qualification.
[Testing documentation](docs/testing.md) describes the provider-free checks.

## Related public surfaces

Apple's Foundation Models framework and its Shortcuts automation surface have
different contracts. Framework capabilities must not be attributed to a
Shortcuts route without demonstrating them there. Hollis does not claim to have
invented calling Apple Intelligence through Shortcuts or to be the first Apple
Foundation Models CLI.

- [Apple Foundation Models framework](https://developer.apple.com/documentation/foundationmodels)
- [Apple: Introducing the Third Generation of Apple's Foundation Models](https://machinelearning.apple.com/research/introducing-third-generation-of-apple-foundation-models)
- [Apple Support: Run shortcuts from the command line](https://support.apple.com/guide/shortcuts-mac/run-shortcuts-from-the-command-line-apd455c82f02/mac)
- [Joseph Humfrey: The Shortcut to integrating Private Cloud Compute into my app](https://joethephish.me/blog/the-shortcut-to-integrating-PCC/)
