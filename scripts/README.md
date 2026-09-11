# Build and verification tools

These tools maintain the public product. They do not include live research
harnesses, recorded conversations or machine-specific run evidence.

- `make-bridge.py`, `make-image-bridge.py`, `package-bridges.py` and
  `count_bridges.py` generate and check the installable Shortcuts definitions.
- `build-native.sh` compiles the native-local helper using an Apple SDK >=27.
- `plugin/` verifies manifests, runtime provenance, installation and packaging.
  Its tests use synthetic assets and isolated state.
- `image-install-check/` checks generated image bridge wiring without inference.
- `release/` enforces reviewed file membership and publication privacy.
- `poolside-review/` implements the bounded PR-review workflow and fake-HTTP tests.

See [Testing Hollis](../docs/testing.md) for the standard provider-free checks
and the [plugin package guide](../plugins/hollis/docs/package.md) for packaging.
