# Native helper protocol harness

Model-free checks for the `hollis-native` JSON protocol. The helper is built
with `scripts/build-native.sh` and driven the way the Go runner drives it: one
NDJSON request on stdin, an event stream on stdout.

- `test_protocol_contract.py` compares the helper and Go sources: version and
  protocol pins, request and event field names, accepted operations and events,
  error kinds, and the rules that prompt text travels only on stdin and error
  events carry no free text. It needs no Apple SDK or model, so it runs on
  every CI host.
- `test_protocol_events.py` builds the helper and runs golden request cases from
  `testdata/protocol/`: protocol and operation guards, malformed and
  non-canonical JSON, unsupported schemas, the request-size cap, the status
  event shape, and prompt guards. Every case fails before a session exists or
  asks only for the non-generative status event.
- `test_protocol_live.py` is the only file that generates text. It is skipped
  unless `HOLLIS_NATIVE_LIVE=1`, is never run by CI, and makes three on-device
  calls with synthetic prompts. It asserts protocol behavior, not answer
  quality or any reliability rate.

Run the offline suites with:

```sh
python3 -m unittest discover -s scripts/native -p 'test_*.py'
```

The event cases skip themselves where a helper built for macOS 27 cannot
execute (the release image compiles on a macOS 26 host). The build itself is
still checked there. Golden files are exact: an added or renamed event field
fails the case, because the Go decoder rejects unknown event fields.

Adding a case means adding one JSON file to `testdata/protocol/` with a
`stdin` string (or a `stdin_repeat` object for size cases) and the exact
`events` the helper must emit, including the `protocol`, `version` and `model`
stamps. Never put a real document, image, prompt or machine path in a fixture.
