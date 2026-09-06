# Apple inside the conversation

The Hollis plugin brings Apple's contribution into Claude Code or Codex: ask for
an answer, document comparison, image assessment or illustration, then continue
the conversation. It is designed to work alongside gstack while remaining useful
for everyday writing, research synthesis and creative work.

Start with the [plugin guide](../plugins/hollis/README.md), which covers installation,
first-use Apple approvals, supported capabilities and privacy. The source package
contains both host manifests and shared skills; the downloadable archive also
bundles the pinned ARM64 Hollis 0.3.1 runtime and five bridges.

Plugin 0.1.0 is prepared locally for review. Its validation record distinguishes
tests completed from remaining release acceptance work; see the
[validation report](plugin-validation.md). Repository installation commands apply once this package is
published on the selected ref.

## Maintainer build

The developer packaging environment requires Python 3 and authenticated GitHub
CLI. Those are **not** end-user prerequisites.

```sh
python3 -m unittest discover -s scripts/plugin -p 'test_*.py'
python3 scripts/plugin/package_plugin.py --validate-only
python3 scripts/plugin/package_plugin.py --output dist/plugin
```

The packager refuses corrupted assets, failed provenance, unsafe bridge members
and an existing output archive. `--assets-dir` reuses cached release bytes while
still verifying provenance. The archive includes both marketplace entries, so
either host installs the same `plugins/hollis` directory.

`.github/workflows/plugin-release.yml` runs checks, packages and attests the
archive. A manual workflow run uploads review artifacts; a separately authorized
`plugin-v0.1.0` tag publishes the release. No RC-branded release is needed.