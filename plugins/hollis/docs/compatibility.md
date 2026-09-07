# Compatibility

| Component | Supported path / limit |
|---|---|
| Execution host | Local, physical Apple-silicon Mac with Apple Intelligence |
| macOS | Initially macOS 27; other versions receive an explanation rather than an assumed match |
| Rosetta | Detect physical ARM support even when the shell reports x86_64; runtime remains ARM64 |
| Apple readiness | Enable Apple Intelligence and complete Apple's downloads/first-use approvals; inference is the final check |
| Text | Shortcuts Cloud, Cloud Pro, On-Device, ChatGPT |
| Image understanding | Cloud, Cloud Pro, ChatGPT; not On-Device |
| Generation | Animation, Illustration, Sketch, Genmoji, Any Style; unlocked session required |
| Host integration | Claude Code first; Codex shares the same source skills and scripts |
| Documents | UTF-8 `.txt`/`.md`; no implied PDF/Office parser |
| Network | Required for Cloud, Cloud Pro, ChatGPT and source-install downloads |
| Remote agents | Must execute on the eligible Mac; installing a skill on Linux does not provide Apple access |

The existing runtime enforces prompt and attachment limits. The plugin discovers
the installed command contract rather than inventing new parameters. A model's
internal context window is not inferred from a different developer API.

Generation returns square native output; requested aspect ratio/size uses
explicit local crop/pad processing. Any Style can produce different aesthetics,
and a reference is not an exact-identity guarantee. The native Image Playground
app can do things its tested Shortcut action cannot, including the tested
ChatGPT generation route. We do not use desktop automation as a fallback.

macOS builds, host versions and completed versus unproven setup scenarios are
listed in the [validation record](validation.md). Temporary test directories establish
installer behavior; they do **not** establish clean-account readiness, Rosetta
execution or behavior on an untested OS.

Plugin 0.1.0 bundles the released runtime 0.3.2 after verification of its
GitHub provenance and checksums. Earlier source and archive tests retain their
original version labels in the validation record. Fresh-account and first-consent
testing remain deferred.

## Image reference evidence

The repeated reference controls did not establish reliable use of the supplied
pixels: fully described scenes generated with and without references, while
subject-omission controls missed their attachments' subjects. Treat reference
delivery and visual fidelity as separate checks. All five styles generated in
the round; the coffee photo conditions were declined, without an identified cause.

With the subject omitted from an otherwise identical prompt, the café attachment
produced a male portrait, the turtle attachment produced flowers, and no
attachment produced another male portrait. Each condition was observed once.
These results do not establish impossibility or isolate the failing layer;
the installed bridge's current action graph could not be read in that round.
The earlier corrected-bridge echo test established byte delivery through that
tested bridge, not that the generator conditions on those pixels.
