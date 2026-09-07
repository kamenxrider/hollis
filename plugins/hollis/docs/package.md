# Package layout and maintenance

Hollis keeps the runtime and plugin in one repository, with separate versions
and release artifacts. This directory is the plugin's source of truth. Claude
and Codex consume the same skills and scripts through their native manifests.

## Folder map

```text
plugins/hollis/                 Plugin root: hosts load this directory
├── README.md                  Installation and first useful request
├── plugin.json                Portable Agent Plugins manifest
├── .claude-plugin/plugin.json Claude identity and namespace
├── .codex-plugin/plugin.json  Codex identity and skill discovery
├── runtime.lock.json          Released runtime version, commit and hashes
├── LICENSE
├── skills/
│   ├── hollis/
│   │   ├── SKILL.md          Ask, continue and show results
│   │   └── references/       Capability workflows and gstack context
│   └── hollis-setup/SKILL.md  Install, discover bridges and recover
├── scripts/
│   ├── setup.sh              Runtime install/check/path/rollback entry point
│   ├── run.sh                Verified executable, serialization and pacing
│   ├── bridges.sh            Bridge discovery and individual imports
│   └── common.sh             Shared installation and integrity helpers
├── docs/                     Usage, setup, compatibility, privacy and evidence
├── examples/                 Sample plan, demo steps and recorded results
└── assets/runtime/           Added to built archives; absent from source
    ├── hollis-darwin-arm64
    ├── hollis-bridges.zip     All five bridges
    └── provenance.json       Runtime-asset verification receipt
```

At the **repository or extracted archive root**, `.claude-plugin/marketplace.json`
and `.agents/plugins/marketplace.json` both point to `./plugins/hollis`. That
outer directory is the marketplace installation path. The inner plugin directory
is the path for Claude's `--plugin-dir` development loading. These two paths
serve different purposes; neither requires a separate repository.

## Portable core and native hosts

| Contract | Entry point | Boundary |
| --- | --- | --- |
| Agent Plugins 1.0.0 | `plugin.json`, `skills/` | Shared package identity and skill layout |
| Claude Code | `.claude-plugin/plugin.json` | Claude loading, namespace and installation |
| Codex | `.codex-plugin/plugin.json` | Codex loading, display metadata and installation |

The native manifests are compatibility adapters, not replacements for the
portable manifest or standardized Agent Plugins extension namespaces. Keep
host-only fields out of root `plugin.json`. Maintain matching plugin names and
versions across all three manifests; reuse the shared skills instead of copying
them per host. New host support needs a real installation and conversation check.

The plugin currently calls Hollis's CLI. There is no MCP server, automatic install
hook, streaming interface or background job. Portable packaging does not give a
remote host access to a Mac, establish Apple consent or guarantee identical host
permissions and image rendering.

Format references: [Agent Plugins specification](https://agent-plugins.org/specification),
[Claude plugins](https://code.claude.com/docs/en/plugins),
[Claude marketplace paths](https://code.claude.com/docs/en/plugin-marketplaces#relative-paths),
[OpenAI plugin packaging](https://developers.openai.com/plugins/build/plugins).
The Codex terminal commands in this guide were also checked against the local
CLI's `plugin --help`, `plugin marketplace add --help` and `plugin add --help`
on 7 September 2026; older hosts may expose different capabilities.

## What belongs where

- `README.md` is the human starting point. The plugin formats do not require it
  for loading, but Hollis's own packaging requires and includes it. Keep it at
  the plugin root; use `SKILL.md` for agent behavior. Do not add an unsupported
  `readme` manifest field. The format references above describe loading rules,
  not a requirement to omit user documentation.
- Conversation behavior: shared `skills/` and their references.
- Installation and dispatch: `scripts/`, preserving macOS utilities as the only
  end-user installation dependencies.
- User instructions and safe examples: `README.md`, `docs/` and `examples/`.
  Keep local documentation links inside this plugin so they survive packaging.
- Runtime behavior: the parent Hollis Go project. Avoid duplicating its routing,
  error handling, validation or storage logic in the plugin.
- Build tooling and tests: repository-level `scripts/plugin/`; release automation:
  `.github/workflows/plugin-release.yml`. These are maintainer tools and are not
  installed on the user's Mac.

## Validate and build from a source checkout

Run from the **Hollis repository root**. Maintainer tests use Python 3; real
packaging also needs authenticated GitHub CLI to verify build attestations.

```sh
python3 -m unittest discover -s scripts/plugin -p 'test_*.py'
python3 scripts/plugin/package_plugin.py --validate-only
for script in plugins/hollis/scripts/*.sh; do bash -n "$script"; done
```

These checks do not call Apple. Archive tests use explicitly synthetic fixtures;
they do not produce publishable provenance. Live host and first-consent checks
remain separate in [validation](validation.md).

To build a new review archive after checks, choose an unused output directory:

```sh
python3 scripts/plugin/package_plugin.py --output dist/plugin-review
```

Packaging downloads the locked assets, verifies their hashes and GitHub provenance,
and bundles both marketplace entries, this plugin directory, the ARM64 binary
and all five bridges. It also produces a checksum and runtime-provenance receipt.
`--assets-dir /absolute/cached-assets` reuses cached assets while still checking
provenance. An existing archive is never overwritten.

## Versions and release sequence

Plugin **0.1.0** pins released runtime **0.3.3** in `runtime.lock.json`.
Editing skills or docs does not upgrade the bundled binary, and preserved
review archives do not change with source. The plugin release is explicitly
excluded from GitHub's “Latest” label so the CLI installer continues to find
the latest runtime.

When a new runtime is released, verify its provenance, then update the lock and
build a new plugin archive. Recheck
both hosts against that exact archive before replacing the candidate. Preserve
previous archives and receipts, and record unresolved acceptance gaps.

For a plugin release, keep the portable/native manifest versions and Claude's
marketplace version aligned; use the matching `plugin-v<version>` tag and release
notes. Once public, bump the plugin version for updates so host caches can detect
them. A manual workflow run produces review artifacts; pushing a plugin release
tag publishes it.

[Install and use](../README.md) · [Setup and state paths](setup.md) ·
[Recorded validation](validation.md)
