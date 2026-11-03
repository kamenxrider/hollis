## Summary

What this changes and why.

## Checks

- [ ] The offline checks listed under "Offline checks" in `CONTRIBUTING.md` pass: Go format, vet, tests and race tests; the Python suites; plugin validation and script syntax; the source and source-archive privacy checks; and both bridge generators
- [ ] For changes to the native helper, packaging or release scripts: the `native-package` steps in `CONTRIBUTING.md` pass on macOS 27 with SDK 27
- [ ] New or changed files are entered in `scripts/release/public-source.json`, and their content is fit to publish
- [ ] Tests make no live model calls and use only synthetic fixtures
- [ ] User-visible behavior changes are reflected in `docs/` and `docs/releases/unreleased.md`

## Live verification

If you checked the change against a real route, name the route, the macOS version and build, and what you observed. Leave out prompts and answers. Write "none" if you did not.
