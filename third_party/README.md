# Local MCP SDK replacement

Hollis requires the official `github.com/modelcontextprotocol/go-sdk v1.8.0`,
with a narrowly scoped downstream correction in `go-sdk/`.
Upstream tag commit: `3f3b699b2b67e1ed033a63d6651671dab53c2d32`.
Source: https://github.com/modelcontextprotocol/go-sdk/tree/v1.8.0.

The complete module ZIP is retained, including its tests, documentation and
license notices. `go-sdk.provenance.json` records the official Go module
checksum, ZIP SHA-256, original file hashes, changed/added file hashes and the
patch hash. `go-sdk-structured-content.patch` is the complete downstream diff.
No module-cache files are modified.

## Correction and compatibility

Only `CallToolResult.UnmarshalJSON` changes in production. Its existing bounded
decoder extracts `structuredContent` as raw JSON, then a scoped `UseNumber`
decoder preserves every numeric leaf as `json.Number`. Maps, arrays, strings,
booleans and nulls retain their types. Ordinary numbers also become
`json.Number`; callers must not assert `float64` for numeric structured values.
Valid JSON numbers outside float64 range are accepted without corruption.
Metadata and other SDK decoding retain their original behavior.

The remaining modifications are explicit numeric expectations in three upstream
test files and a new regression test file. Transport/session logic, output-schema
validation, framing, cancellation and size limits are unchanged.

## Verification and upgrades

Run `python3 scripts/release/verify_sdk.py` from the repository root. It checks
the replacement's exact membership and hashes, reconstructs the official module
checksum from upstream file hashes, and reverses the recorded patch in a
temporary copy to prove every upstream byte is restored. `go mod verify` alone
does **not** verify a local replacement.

Run `go test ./...` and `go test -race ./mcp ./internal/json` from `go-sdk/`,
then the Hollis offline checks from the repository root. Release tests enforce
the provenance check; CI explicitly tests the nested SDK module. The reviewed
source manifest includes all replacement/provenance/patch files, so source
archives build with the same dependency and retain the same notices.

On an SDK upgrade, review the upstream change, replace the module from its
verified official ZIP, and deliberately update the pinned identity, file hashes,
patch, test expectations and source manifest. Do not regenerate these pins in CI.
Remove this replacement and its patch when an official pinned release preserves
structured numeric values and all regression tests pass against it. No upstream
acceptance is claimed.

## Licensing and distribution

Retain `go-sdk/LICENSE` in full. Upstream v1.8.0 describes a transition from MIT
to Apache-2.0: consenting contributions and new code are Apache-2.0, while
unrelicensed contributions remain MIT. Nonspecification documentation is
CC-BY-4.0. The upstream file includes both code license texts and the
documentation-license reference. Individual source copyright/license notices
remain intact; modified files carry an explicit Hollis modification notice.

The runtime bundle's existing `LICENSE` member includes the Hollis license
followed by this complete SDK license and its module/version attribution. This
preserves the existing bundle member contract while shipping the dependency's
notices. The release SPDX inventory scans this checked-in source tree; this
provenance manifest identifies the downstream delta rather than presenting it
as an unmodified official release.
