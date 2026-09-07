# Testing Hollis

Run these commands from the repository root. They are developer checks, not end-user installation requirements.

See the [developer tools index](../scripts/README.md) for each harness's purpose,
dependencies, and distinction between offline checks and live model calls.

```bash
test -z "$(gofmt -l .)"
go vet ./...
go test ./...
go test -race ./...
go build ./cmd/hollis
python3 -m unittest discover -s scripts/image-install-check -p 'test_*.py'
python3 -m venv .venv
.venv/bin/python -m pip install --only-binary=:all: -r scripts/image-suite/requirements.txt
.venv/bin/python -m unittest discover -s scripts/image-suite -p 'test_*.py'
```

The default suite is provider-free: subprocess tests inject deterministic runners without a production backdoor, HTTP uses `httptest`, and bridge generation is checked for both macOS profiles. CI runs the race suite on an official macOS Go 1.27 runner before packaging.

Non-draft pull requests from branches in this repository to the default branch
also receive an automated Poolside review of a bounded PR diff. The reviewer
receives no repository tools and does not execute checks; normal provider-free
tests run in CI.

The separately gated live suite needs the exact built binary and invokes real Shortcuts models, so run it only when those calls are intended:

```bash
HOLLIS_LIVE=1 HOLLIS_BIN=/absolute/path/to/hollis \
  go test -tags=hollis_live ./internal/integration -run TestLiveRealSystem -v
```

It uses a temporary absolute `HOLLIS_STATE_DIR`, quiet prompts, an ephemeral loopback port, and cleans up only the conversation it creates. Its six Cloud Pro calls are serialized with at least 45 seconds between them, with no retries; any rate limit stops that lane.

[Back to Hollis](../README.md)
