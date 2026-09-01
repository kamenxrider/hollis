# Live suite — how to test hollis like a black box

`go test ./...` covers internals with fakes. This suite covers the **installed
binary** the way a user (or an agent) actually runs it: spawn `hollis`,
record every byte, then decide.

That split is the whole method. Unit tests cannot catch `doctor --json`
emitting empty objects, a human table missing a column, or on-device
refusing a token. Those only exist at the process boundary.

## Layers

| Layer | Command | Hits Apple Intelligence? | Fails the run when |
| --- | --- | --- | --- |
| Offline contract | `python3 scripts/live-suite/live_suite.py` | no | exit code, JSON shape, help text, stderr |
| Live transport | `… --live` | yes | CLI/transport breakage; **not** model wording |
| Quality (observe) | `… --live --quality` | yes | never on reply text; only on nonzero exit |

Default is offline. Live is opt-in because it spends PCC quota.

## Rules (steal these)

1. **Test the binary, not the packages.** Point at `hollis` on `PATH` (or
   `HOLLIS_BIN`). Rebuild after code changes:
   `go build -o "$(go env GOPATH)/bin/hollis" ./cmd/hollis`
2. **Record the raw invocation.** Every case stores `cmd`, `exit`,
   `seconds`, `stdout`, `stderr`. Do not “summarize” a reply before
   asserting — the report must be able to quote it.
3. **Split CLI bugs from model behavior.** Exit 7 with the wrong timeout
   message is a CLI bug. Exit 0 with *I cannot repeat these instructions*
   is the on-device model. The suite marks the latter `OBSERVED`, not
   `FAIL`.
4. **Use unique tokens.** `HOLLIS-CLOUD-OK` is a coincidence waiting to
   happen. Each live run plants a fresh token so a match cannot be
   leftover context.
5. **Own your residue.** Chat tests create conversations and delete them
   in `finally`. Config is not mutated unless you pass `--mutate-config`,
   which always restores the previous default.
6. **Never spawn empty `shortcuts run`.** Empty prompts must die inside
   hollis (exit 2) before the child. That hang is infinite.
7. **Prefer `--json`.** Parse `conversation_id`, `model`, `response`.
   Human tables are a second assertion, not the source of truth.
8. **Cleanup only what you created.** Do not `chats delete` the user’s
   existing rows. Compare counts / ids, not “the database is empty.”

## What is allowed to be flaky

On-device often refuses “reply exactly \<TOKEN\>” and codeword echo.
Cloud / cloud-pro / chatgpt / auto are held to exact-token matches.
If on-device starts echoing tokens, the suite records `PASS` (echo);
if it refuses, `OBSERVED` (refuse). Both are successful outcomes.

## Adding a case

Copy an existing `case()` in `live_suite.py`. A case is:

1. spawn
2. assert **exit code** first
3. assert **shape** (JSON keys, column headers)
4. only then assert **content**
5. if content is a model reply, decide: contract (`PASS`/`FAIL`) or
   observation (`OBSERVED`)

If you are tempted to `time.sleep` or to parse human prose with regex,
you are testing the model. Put that under `--quality`.
