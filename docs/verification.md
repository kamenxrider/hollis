# Verification and trust

The binaries are not Developer ID signed or notarized. Hollis does not claim
Gatekeeper approval.

The installer checks the bundle against release `SHA256SUMS`. Without `gh`,
it checks file fingerprints (SHA-256) only, including the hash pinned in the
installer. Only `gh` verifies the source commit: it checks GitHub build
provenance against the pinned commit, release tag and release workflow.
A checksum alone does not prove which source commit produced a file.
Homebrew checks file hashes; it does not run this attestation check.

The installer retains the complete verified bundle in
`~/.local/share/hollis/<version>/` and links both executables into
`~/.local/bin`. It checks the native signature, both versions and the helper
protocol before installation. These checks make no model calls. Its source
is [scripts/install.sh](../scripts/install.sh).

For the smallest trust chain, build from source
([other install routes](compatibility.md#other-install-routes)). To run the
installer’s checks yourself:

<a id="verified-install-step-by-step"></a>
<!-- updated at release tagging -->

<details>
<summary>Verify manually</summary>

These commands verify and install a matched published release bundle. Set
`HOLLIS_VERSION` to `0.7.1` and `HOLLIS_SOURCE_DIGEST` to the full commit selected
by the authenticated [`v0.7.1` release tag](https://github.com/kamenxrider/hollis/releases/tag/v0.7.1),
as shown on GitHub's release or tag page. Independently authenticate that tag
and commit before using them. The commands below require those values; they
do not discover or authenticate the tag's commit for you. The attestation check
must match both that tag and the full commit. Do not substitute a candidate
build or a branch head. The import command preserves a custom image bridge.
Skip it if you only want Local. Do not install a bundle that fails either
the checksum or the attestation check.

```bash
(
set -euo pipefail
umask 077
: "${HOLLIS_VERSION:?Set the version from the authenticated release tag}"
: "${HOLLIS_SOURCE_DIGEST:?Set the full source commit from the authenticated release tag}"
HOLLIS_BUNDLE="hollis-$HOLLIS_VERSION-darwin-arm64.zip"
temporary=$(mktemp -d)
trap 'rm -r "$temporary"' EXIT
cd "$temporary"
gh release download "v$HOLLIS_VERSION" --repo kamenxrider/hollis \
  --pattern "$HOLLIS_BUNDLE" --pattern SHA256SUMS
awk -v asset="$HOLLIS_BUNDLE" '$2 == asset { n++; print } END { if (n != 1) exit 1 }' \
  SHA256SUMS > SELECTED_SHA256SUMS
shasum -a 256 -c SELECTED_SHA256SUMS
gh attestation verify "$HOLLIS_BUNDLE" --repo kamenxrider/hollis \
  --signer-workflow kamenxrider/hollis/.github/workflows/release.yml \
  --source-ref "refs/tags/v$HOLLIS_VERSION" \
  --source-digest "$HOLLIS_SOURCE_DIGEST" \
  --deny-self-hosted-runners
unzip -q "$HOLLIS_BUNDLE" -d runtime
codesign --verify --strict runtime/hollis-native
[[ $(runtime/hollis --version) == "hollis $HOLLIS_VERSION" ]]
[[ $(runtime/hollis-native --version) == "hollis-native $HOLLIS_VERSION" ]]
[[ $(runtime/hollis-native --protocol-version) == 4 ]]
# Choose a new directory. Do not overwrite an existing installation.
destination="$HOME/.local/share/hollis/$HOLLIS_VERSION"
[[ ! -e $destination && ! -L $destination ]]
mkdir -p "$HOME/.local/bin" "$HOME/.local/share/hollis"
mv runtime "$destination"
# ln refuses an existing file or link; move an older install aside first.
ln -s "$destination/hollis" "$HOME/.local/bin/hollis"
ln -s "$destination/hollis-native" "$HOME/.local/bin/hollis-native"
"$destination/hollis" setup --import
)
export PATH="$HOME/.local/bin:$PATH"
```

</details>

`doctor` and `setup` check setup and availability, never inference. Verified
downloads and existing-account tests do not establish fresh-account onboarding.
Dated live checks and what they qualify are in [Evidence](../EVIDENCE.md), which
also compares Hollis with Apple’s own [`fm` tool](../EVIDENCE.md#why-not-fm).
