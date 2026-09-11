# Publication privacy gate

A release contains public implementation, synthetic regression tests, user
instructions and selected product artwork. Private research, real prompts and
responses, internal reviews, recordings and working brand assets must not ship.

`public-source.json` lists every reviewed source file and its SHA-256. The
manifest itself has a fixed schema and contains only those paths and hashes.
Adding a file or changing reviewed content fails until a maintainer explicitly
reviews that change for publication and updates its manifest entry. CI never
regenerates the manifest. A hash update is a publication-review decision, not a
routine way to dismiss a failed check.

Run from the repository root:

```sh
python3 scripts/release/privacy.py --source
git archive --format=tar HEAD > /tmp/hollis-source.tar
python3 scripts/release/privacy.py --archive /tmp/hollis-source.tar --kind source
python3 -m unittest discover -s scripts/release -p 'test_*.py'
```

The archive check requires exact source membership and reviewed bytes. Runtime
bundles contain exactly five named files; bridge archives contain exactly five
production bridges. Plugin archives have their own explicit file list, and
embedded source must match the reviewed files. Nested bridge ZIPs are inspected.
Symlinks, duplicate members, unsafe paths and oversized inspection inputs fail.

The release workflows also inspect the final asset directory, including standalone
binaries, provenance JSON and the SBOM, before upload or attestation. Content
checks reject recognizable personal home paths, private task links and credential
patterns without echoing matched data. Synthetic privacy-test canaries are
explicitly distinguished from real data; tests receive no blanket exemption.

These automated checks supplement a human content review. They do not prove
that arbitrary data is non-sensitive or erase previous commits and releases.
After publication, download and inspect GitHub's actual source archives and
release assets as a separate acceptance step. Until that is done, report only
local archive verification. Do not publish or rewrite old history implicitly.
