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
