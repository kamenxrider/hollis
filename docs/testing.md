# Testing Hollis

The public source contains ordinary regression tests with synthetic inputs.
They exercise transport, output contracts, terminal safety, context limits,
HTTP streaming, cancellation, storage and installer behaviour without model calls.

```sh
test -z "$(gofmt -l .)"
go vet ./...
go test ./...
go test -race ./...
go build ./cmd/hollis
python3 -m unittest discover -s scripts/image-install-check -p 'test_*.py'
python3 -m unittest discover -s scripts/plugin -p 'test_*.py'
python3 -m unittest discover -s scripts/release -p 'test_*.py'
python3 scripts/release/privacy.py --source
```

Live research harnesses, raw prompts/responses, host transcripts, screenshots
and historical run receipts are maintained separately and are not distributed.
Synthetic privacy canaries in tests are fabricated values used to verify that
errors do not disclose request data.

## Cross-language schema parity

Guided output (see [native local](native-local.md#guided-output)) validates a
schema subset twice: the shipped Swift helper (`native/SchemaSubset.swift`)
parses it before generation, and Hollis re-checks the returned object against
the same subset. Both parsers are compiled and compared over one case table, so
a divergence in acceptance, JSON pointer or rejection reason fails a test:

```sh
HOLLIS_SCHEMA_PARITY=1 go test ./internal/runner -run TestSchemaSwiftParity -count=1 -v
```

The test skips without `HOLLIS_SCHEMA_PARITY=1` because it compiles Swift, and
it skips on hosts without the macOS 27 SDK or on a non-arm64 machine. It is
offline: it builds the checked-in subset parser and a harness from the same
source tree, runs them locally, and calls no model. The payload cases carry the
value inside a declared property so the per-node checks are actually compared,
and one case straddles the 64 KiB gate with solidus characters, because
Foundation escapes `/` and that decision moves the measured size.

Accepted limitation, resolved by a subset rule rather than a dependency: the
two runtimes normalise Unicode differently, so a schema whose property name,
`required` name or `enum` choice was precomposed `é` would be one name in
Foundation (canonical equivalence) and a different byte string in Go. Rather
than adding a normalisation library, the subset holds authoring names and enum
choices to printable ASCII, and both parsers reject anything else with the same
pointer and reason. No two ASCII strings are canonically equivalent unless they
are identical, so the rule removes the divergence instead of documenting it,
and both parity directions are exercised by the precomposed/decomposed cases in
the table.

The same table bounds nesting: three object levels as before, plus at most
sixteen schema nodes on one path counting the root. Object depth alone left array
chains (`items` inside `items`) unbounded, and a document deep enough failed in
the helper's decoder after the request had spawned. `array nesting 14` is
accepted, `array nesting 15` and `array nesting 20` are rejected, and a Go-only
case drives 600 levels through `ParseSchema` and the CLI to prove the rejection
happens before any process exists.

## SQLite migration gates

Storage tests use temporary SQLite fixtures, not user databases. Run the two
acceptance groups independently:

```sh
go test ./internal/store -run 'TestMigration39' -count=1
go test ./internal/store -run 'TestTimestamp16' -count=1
```

The older-executable compatibility test needs the schema-v2 runtime built from
the pinned 0.4.0 source. Without `HOLLIS_SCHEMA_V2_RUNTIME`, only this optional
cross-version test skips. With dependencies cached, build and run it offline:

```sh
old_source=$(mktemp -d)
git archive --format=tar --output="$old_source/source.tar" 8c48203359258ee0537b8db72bbbed2f361785e8
tar -xf "$old_source/source.tar" -C "$old_source"
(cd "$old_source" && GOPROXY=off go build -o "$old_source/hollis-v2" ./cmd/hollis)
HOLLIS_SCHEMA_V2_RUNTIME="$old_source/hollis-v2" GOPROXY=off \
  go test ./internal/store -run 'TestMigration39OlderRuntimeRefusesV3' -count=1 -v
```

The test runs only `chats list --json` against its own temporary state directory.
It requires a clear schema-v3 refusal with exit 10, unchanged logical data and
backups, and retention of a conversation created after migration. It never
restores a backup or calls a model. Executable rollback and explicit
[database recovery](cli.md#database-upgrades-and-recovery) are separate actions.

Before publication, audit the exact commit and each archive using the reviewed
publication file list. Verify checksums and build provenance after publication.
[Back to Hollis](../README.md)
