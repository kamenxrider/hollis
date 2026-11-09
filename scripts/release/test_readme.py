"""Keep manual verification version-neutral without dropping provenance checks."""
from pathlib import Path
import unittest

ROOT = Path(__file__).resolve().parents[2]


class ReadmeVerificationTests(unittest.TestCase):
    def test_manual_verification_uses_one_release_and_source_pin(self):
        readme = (ROOT / 'README.md').read_text()
        block = readme.split('<summary>Verify manually</summary>', 1)[1].split('</details>', 1)[0]
        self.assertIn('https://github.com/kamenxrider/hollis/releases/tag/v0.7.0', block)
        self.assertIn('do not discover or authenticate', block)
        self.assertNotIn('pinned release notes', block)
        self.assertIn('${HOLLIS_VERSION:?', block)
        self.assertIn('${HOLLIS_SOURCE_DIGEST:?', block)
        self.assertIn('HOLLIS_BUNDLE="hollis-$HOLLIS_VERSION-darwin-arm64.zip"', block)
        self.assertIn('gh release download "v$HOLLIS_VERSION"', block)
        self.assertIn('shasum -a 256 -c SELECTED_SHA256SUMS', block)
        self.assertIn('gh attestation verify "$HOLLIS_BUNDLE"', block)
        self.assertIn('--signer-workflow kamenxrider/hollis/.github/workflows/release.yml', block)
        self.assertIn('--source-ref "refs/tags/v$HOLLIS_VERSION"', block)
        self.assertIn('--source-digest "$HOLLIS_SOURCE_DIGEST"', block)
        self.assertIn('--deny-self-hosted-runners', block)
        self.assertNotIn('0.4.0', block)

    def test_auto_falls_back_only_for_missing_bridge(self):
        readme = (ROOT / 'README.md').read_text()
        self.assertIn('once only after a confirmed missing bridge', readme)
        self.assertIn('Provider limits stop without retry or fallback.', readme)
        self.assertNotIn('after a recognized Cloud limit', readme)

    def test_extraction_and_pending_installer_are_not_guarantees(self):
        readme = (ROOT / 'README.md').read_text()
        self.assertIn('Extraction and model answers can', readme)
        self.assertIn('check important numbers against the original', readme)
        self.assertNotIn("document's text exactly", readme)
        self.assertIn('curl installer stays unavailable and refuses installation', readme)
        self.assertIn('fingerprint and source', readme)
        self.assertIn('nine imports in 0.7.0', readme)


if __name__ == '__main__':
    unittest.main()
