# Apple inside the conversation

The Hollis plugin brings Apple's contribution into Claude Code or Codex: ask for
an answer, document comparison, image assessment or illustration, then continue
the conversation. It is designed to work alongside gstack while remaining useful
for everyday writing, research synthesis and creative work.

Start with the [plugin guide](../plugins/hollis/README.md) for installation and
first use. The plugin stays in this repository with its own versioned archive:
one set of skills and scripts, a portable manifest and native Claude/Codex adapters.

- [Install in Claude Code or Codex](../plugins/hollis/README.md#install)
- [Everyday requests and follow-ups](../plugins/hollis/docs/usage.md)
- [Setup, state locations and recovery](../plugins/hollis/docs/setup.md)
- [Folder map, formats and maintainer build](../plugins/hollis/docs/package.md)
- [Fictional workflow example](../plugins/hollis/examples/demo.md)

The source package downloads the pinned runtime during setup; a built archive
is intended to bundle the provenance-verified ARM64 Hollis 0.7.0 executable, matching native helper and all five bridges.
New source docs do not update a preserved ZIP; older archives retain their
original runtimes and recorded checksums.

Plugin 0.4.0 targets runtime 0.7.0. The checked-in lock currently pins runtime
0.4.0, so source setup and packaging refuse it until published 0.7.0 assets are
verified and pinned. Nothing in these docs upgrades that lock. Plugin 0.2.0 was withdrawn. The
plugin's validation record
distinguishes completed tests from remaining first-account acceptance work;
see the [validation report](plugin-validation.md).

Maintainer checks, runtime provenance and release sequencing are documented in
the plugin's [package guide](../plugins/hollis/docs/package.md).
