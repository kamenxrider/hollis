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

Before publication, audit the exact commit and each archive using the reviewed
publication file list. Verify checksums and build provenance after publication.
[Back to Hollis](../README.md)
