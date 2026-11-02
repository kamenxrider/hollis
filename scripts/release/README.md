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
binaries, `release-manifest.json`, provenance JSON and the SBOM, before upload or
attestation. The release manifest is generated after final artifact bytes and the
SBOM, excludes itself and `SHA256SUMS`, and is included in the checksum file. Content
checks reject recognizable personal home paths, private task links and credential
patterns without echoing matched data. Synthetic privacy-test canaries are
explicitly distinguished from real data; tests receive no blanket exemption.

Content rules also cover the delivery process and its working files. The exact
patterns and their comments are in `privacy.py`; the categories are:

- A personal home path is any home-directory name other than the documented
  placeholders `private`, `example`, `fixture`, `runner`, `test`, `you`,
  `name`, `<name>` and `Shared`, with or without a trailing slash.
- Private tool state and worktree roots: the two private tool-state directory
  names, any path using them, and hidden tool directories containing a
  worktrees directory.
- Private delivery-process terms as whole words: the process and its
  coordinator, the product phrase naming it, and path-like references to its
  handoff, ledger and planning documents. The ordinary word "plans" in prose is
  not a finding.
- Role labels: `Agent` immediately followed by a digit, or a bare agent label
  next to an agent or verification word. A label alone in ordinary prose is not
  a finding.

The patterns are generic. No real account name or personal string is stored in
this repository, and the placeholder list is the only home-path exemption. Each
rule is split across string literals where needed so the gate does not match its
own source, and the whole tracked tree is expected to pass unchanged.

To check text that leaves through another channel, such as a commit message or a
pull-request body, before posting it:

```sh
python3 scripts/release/privacy.py --text notes.md
git log -1 --format=%B | python3 scripts/release/privacy.py --text -
```

Text mode applies the content rules only. It does not review archive membership
or update the manifest, and it does not replace human review.

The sidecar-based pin refresh requires a release that publishes this record.
Do not synthesize metadata for older published assets or change existing pins
as part of local distribution preparation.

The no-install-time-effects contract applies to plugin runtime installation and
the prepared Homebrew formula: neither runs inference or opens bridge import
dialogs during installation. It does not apply universally to every installer.
The legacy `scripts/install.sh` remains unchanged and is a known remaining
side-effecting path: it can open bridge import dialogs and select an image
default. It is outside this distribution-preparation scope.

These automated checks supplement a human content review. They do not prove
that arbitrary data is non-sensitive or erase previous commits and releases.
After publication, download and inspect GitHub's actual source archives and
release assets as a separate acceptance step. Until that is done, report only
local archive verification. Do not publish or rewrite old history implicitly.
