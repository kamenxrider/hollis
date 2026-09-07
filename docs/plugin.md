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
- [Recorded gstack and image demonstration](../plugins/hollis/examples/recorded-demo.md)

The source package downloads the pinned runtime during setup; a built archive
bundles the provenance-verified ARM64 Hollis 0.3.2 executable and all five bridges.
New source docs do not update a preserved ZIP; older archives retain their
original runtimes and recorded checksums.

Get [plugin 0.1.0](https://github.com/kamenxrider/hollis/releases/tag/plugin-v0.1.0)
or install from the repository using the guide above. Its validation record
distinguishes completed tests from remaining first-account acceptance work;
see the [validation report](plugin-validation.md).