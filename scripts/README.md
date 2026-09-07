# Developer tools and test harnesses

These are maintainer tools, **not end-user installation dependencies**. To install
and use Hollis, start with the [CLI](../README.md#quickstart) or
[agent plugin](../plugins/hollis/README.md). Run the commands below from the
repository root.

| Tool | Question it answers | Dependencies and execution |
| --- | --- | --- |
| [live-suite](live-suite/README.md) | What did each controlled Apple call return through the plugin, and did pacing, runtime identity and input bytes stay consistent? | `host_matrix.py`: Python 3 standard library; live runs need a configured plugin and supported Mac. The older image-generation suite also needs Pillow. Unit tests use fixtures. |
| [image-suite](image-suite/README.md) | Does image understanding consume the pixels, and do CLI/chat/API image workflows preserve their contracts? | Python 3.11+. `image_suite.py` needs pinned [Pillow](image-suite/requirements.txt); `acceptance_suite.py` is standard-library only. Planning/tests are offline; run modes call Apple. Live checks need Hollis, macOS Shortcuts and process-inspection access; building Hollis from source additionally needs Go. |
| [image-install-check](image-install-check/test_make_image_bridge.py) | Does the generated bridge connect decoded reference bytes to Photo, and does its ZIP contain the five intended bridges? | Python 3 standard library. Offline plist/ZIP checks; no Shortcut import, Apple call or proof of model reference use. |
| [plugin](../plugins/hollis/docs/package.md#validate-and-build-from-a-source-checkout) | Do setup, recovery, manifests, locked assets and archive extraction work? | Python 3 standard library plus shell/macOS utilities for installation fixtures; native extraction checks use `ditto`. Tests are offline. Real packaging uses network access and authenticated GitHub CLI for provenance. `live_validate.py` calls Apple; `host_demo.py` launches an authenticated Claude/Codex session and may consume host usage. |
| [poolside-review](poolside-review/README.md) | Is the PR diff complete and bound to the expected repository and commit identities before review? | Python 3 standard library. Unit tests use fake HTTP. The workflow client contacts GitHub and Poolside using separately scoped credentials; it is not an Apple test. |

For any test directory, run `python3 -m unittest discover -s scripts/DIRECTORY
-p 'test_*.py'` after installing its listed dependencies. For just the standard-
library host-matrix regressions:

```sh
python3 -m unittest discover -s scripts/live-suite -p 'test_host_matrix.py'
```

## Bridge build helpers

[`make-bridge.py`](make-bridge.py) and [`make-image-bridge.py`](make-image-bridge.py)
generate unsigned Shortcut source files. [`package-bridges.py`](package-bridges.py)
assembles the five installable bridges into a ZIP; [`count_bridges.py`](count_bridges.py)
checks the expected bridge-file count. These use Python 3's standard library;
generation and packaging do not import a Shortcut or call Apple. Signing and
first-use approval remain separate macOS steps.

## Live runs and evidence

Agree the questions, call budget and stopping conditions before a live run.
Use isolated state and one coordinator across all harnesses: their locks do not
automatically coordinate different tools or output directories. Harness pacing
differs; use the host matrix for the 0.3.2 programme's 10-second completion gap
and 45-second wait before Cloud Pro. Never automatically repeat an uncertain call.

The [evidence guide](../EVIDENCE.md#evidence-storage-and-sharing) explains which
records belong in Git, which remain private, and how a reviewed release bundle
differs from a backup. Repeating a protocol tests its claims; it does not promise
identical model answers or images.
