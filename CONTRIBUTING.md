# Contributing to Hollis

Hollis is a small project. Bug reports, fixes and documentation corrections are
welcome. For a larger change, open a feature request first to agree on the
approach before you write it.

## Build

You need macOS, Go 1.27 or later (see `go.mod`) and Python 3 for the
packaging and protocol checks.

```sh
go build -o "$(go env GOPATH)/bin/hollis" ./cmd/hollis
```

The native helper behind the `local` route needs an Apple macOS SDK 27 or later
and Apple silicon:

```sh
bash scripts/build-native.sh dist/hollis-native-darwin-arm64
```

## Offline checks

Every check below is provider-free: none calls an Apple model, opens a
Shortcuts import dialog or reads your chats. Run them from the repository root
before opening a pull request. The `test` job in `.github/workflows/ci.yml`
runs this set:

```sh
test -z "$(gofmt -l .)"
go vet ./...
go test ./...
go test -race ./...
python3 scripts/release/verify_sdk.py
(cd third_party/go-sdk && go test ./... && go test -race ./mcp ./internal/json)
python3 -m unittest discover -s scripts/native -p 'test_*.py'
python3 -m unittest discover -s scripts/ai-review -p 'test_*.py'
python3 -m unittest discover -s scripts/image-install-check -p 'test_*.py'
python3 -m unittest discover -s scripts/plugin -p 'test_*.py'
python3 scripts/plugin/package_plugin.py --validate-only
for script in plugins/hollis/scripts/*.sh; do bash -n "$script"; done
python3 scripts/release/privacy.py --source
git archive --format=tar HEAD > /tmp/hollis-source.tar
python3 scripts/release/privacy.py --archive /tmp/hollis-source.tar --kind source
python3 -m unittest discover -s scripts/release -p 'test_*.py'
python3 scripts/make-bridge.py --os 27 /tmp/bridges27
python3 scripts/count_bridges.py /tmp/bridges27 4
python3 scripts/make-bridge.py --os 26 /tmp/bridges26
python3 scripts/count_bridges.py /tmp/bridges26 3
```

The source-archive check reads committed files, so commit your change first.

For documentation changes, also run `python3 scripts/check-doc-links.py`.
It checks local links and heading anchors without network access.

On Apple silicon with macOS 27 and SDK 27 or later, the `scripts/native` suite
builds the helper from source and runs its golden event cases as well as the
source contract. On other hosts the build and event cases skip, and only the
contract runs.

CI's `native-package` job also runs on macOS 27 with SDK 27. It builds the
release runtime and checks the packages, then runs the native suite against the
helper it built:

```sh
mkdir -p dist
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags '-s -w -X github.com/kamenxrider/hollis/internal/cli.version=0.7.1' -o dist/hollis-darwin-arm64 ./cmd/hollis
bash scripts/build-native.sh dist/hollis-native-darwin-arm64
python3 scripts/package-bridges.py dist
python3 scripts/plugin/package_runtime.py dist --version 0.7.1
python3 scripts/release/privacy.py --archive dist/hollis-0.7.1-darwin-arm64.zip --kind bundle
python3 scripts/release/privacy.py --archive dist/hollis-bridges.zip --kind bridges
HOLLIS_NATIVE_HELPER="$PWD/dist/hollis-native-darwin-arm64" \
  python3 -m unittest discover -s scripts/native -p 'test_*.py'
```

`HOLLIS_NATIVE_HELPER` selects that prebuilt helper instead of building one.
With it set, an unsupported host or a missing or non-executable helper fails
the suite instead of skipping it. [Native helper protocol harness](scripts/native/README.md)
explains how to add a case, and [Testing Hollis](docs/testing.md) covers the
schema parity and SQLite migration gates.

## No live calls in CI

Both CI jobs run only the checks above. A test must never reach a model,
Shortcuts inference or the network on a CI host. When a change needs a live
check, put it behind an opt-in environment variable that CI never sets, the way
`scripts/native/test_protocol_live.py` requires `HOLLIS_NATIVE_LIVE=1`, and
keep its prompts synthetic. Describe any live check you ran in the pull request
instead of committing its output.

Fixtures are fabricated. Never commit a real prompt, response, document, image,
screenshot, transcript or machine path, including inside test data.

## Privacy gate

Every published file is listed with its SHA-256 in
`scripts/release/public-source.json`. Adding a file or changing one fails
`privacy.py --source` until its entry is updated. Update the entry only after
reading the new content as something that will be published; it is a review
decision, not a way to silence the check. Entries are sorted by path, and the
file uses two-space indentation.

The same content rules can check text that leaves through another channel, such
as a commit message or a pull-request description:

```sh
git log -1 --format=%B | python3 scripts/release/privacy.py --text -
```

[Publication privacy gate](scripts/release/README.md) describes the rules and
the archive checks run at release.

## Pull requests

Keep a pull request to one change. Describe the behavior it changes, update the
relevant guide in `docs/` and add a line to the next release's notes in `docs/releases/` for
anything a user would notice. Match the style of the surrounding code and docs.

By contributing, you agree that your work is licensed under the
[Apache-2.0 license](LICENSE).
