# AGENTS.md

Hollis is a Go CLI and local OpenAI-compatible server for Apple Intelligence on
Apple-silicon Macs with macOS 27. Cloud, Cloud Pro, On-Device and ChatGPT run
through Shortcuts bridges (`/usr/bin/shortcuts`). `local` runs through a Swift
helper (`native/`, Foundation Models) that talks to Go over a JSON line
protocol (`internal/runner/native.go`).

JSON requests use separate `AFM Bridge JSON - <Route>` Dictionary bridges with
`public.json`; text bridges keep `public.plain-text`. `--format json` and HTTP
`json_object` return one validated JSON object on success; invalid output fails
explicitly with `invalid_json_output`, without repair or retry. A request can
still fail or return wrong data. Shortcut routes do not enforce requested
fields, types or values: models may add keys, use `false` or `"null"` instead
of null, nest answers or change punctuation. Local `--schema` constrains
structure and types to the supported schema subset, not factual correctness.
Missing JSON bridges fall
back to text with an import hint; discovery and imports never run inference.

## Where things are

- `internal/cli`: commands and flags. `internal/runner`: routes, Shortcuts and
  native runners. `internal/server`: the HTTP API.
- `internal/runner/error_contract.go`: every error kind, with its exit code
  and HTTP status; `internal/runner/retry_contract.go` classifies retryability.
  Add new kinds to the error contract, not ad hoc.
  `docs/errors.md` mirrors it.
- `plugins/hollis`: the Claude Code / Codex / Agent Plugins package. Its
  `skills/hollis/SKILL.md` tells other agents how to call the CLI.
- `third_party/go-sdk`: vendored MCP SDK with its own AGENTS.md. Avoid changes.

## Build and check

`CONTRIBUTING.md` ("Build", "Offline checks") has the full list. The core:

    test -z "$(gofmt -l .)" && go vet ./... && go test ./... && go test -race ./...
    bash scripts/build-native.sh dist/hollis-native-darwin-arm64   # needs SDK 27
    python3 -m unittest discover -s scripts/native -p 'test_*.py'
    python3 scripts/release/privacy.py --source

For a manual run, set `HOLLIS_STATE_DIR` to a temporary folder. Never use the
real state in `~/Library/Application Support/hollis`.

## Rules you would not guess

- Never call Cloud, Cloud Pro, ChatGPT, Shortcuts inference or image generation
  from tests or while debugging unless the maintainer asks and sets a budget.
  Apple rate-limits each Mac without publishing the limit; about 115 Cloud calls
  in 34 minutes blocked one Mac for a day. Never add automatic retries or
  fallbacks for these routes.
- Local model calls are free but must run one at a time. Parallel calls cause
  busy errors and distort timings.
- Tests and CI never reach a model, Shortcuts or the network. Put a live check
  behind an opt-in variable, as `scripts/native/test_protocol_live.py` does with
  `HOLLIS_NATIVE_LIVE=1`.
- Changing or adding a published file means updating its SHA-256 in
  `scripts/release/public-source.json`, after reading the file as public
  content. Fixtures are fabricated: no real prompts, answers or machine paths.
- A native protocol change (field, event or operation) needs both the Go and
  Swift sides plus `scripts/native/test_protocol_contract.py`.
- Rate-limit detection matches Apple's exact sentences
  (`isAppleRateLimitOutput` in `internal/runner/shortcut.go`). Do not loosen it
  to keyword matching.
- Golden files in `internal/*/testdata/goldens` change only when the output
  change is intended. Explain every golden diff.
- A behaviour change updates its doc: `docs/cli.md` (flags), `docs/api.md`
  (HTTP), `docs/releases/` (release notes), and the plugin skill when commands
  change.
- Commit messages: `feat(cli): ...`, `fix(server): ...`, `docs: ...`,
  describing the change only.
