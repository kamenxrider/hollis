from pathlib import Path
import json
import tempfile
import unittest
from unittest.mock import patch

import test_manifest
from prepare_distribution import render_formula, render_installer


class DistributionPreparationTests(unittest.TestCase):
    def test_installer_pins_share_verified_release_identity_and_bundle(self):
        with tempfile.TemporaryDirectory() as temp:
            base = Path(temp)
            with patch.object(test_manifest, 'VERSION', '0.7.1'):
                dist = test_manifest.make_release_dir(base / 'dist')
            output = base / 'install.sh'
            render_installer(dist, output)
            installer = output.read_text()
            record = json.loads((dist / 'release-manifest.json').read_text())
            self.assertIn('version=0.7.1\n', installer)
            self.assertIn('protocol=4\n', installer)
            self.assertIn(f'source_commit={test_manifest.COMMIT}\n', installer)
            self.assertIn('asset_sha256=' + record['assets']['hollis-0.7.1-darwin-arm64.zip']['sha256'] + '\n',
                          installer)
            self.assertIn('--deny-self-hosted-runners', installer)
            with self.assertRaises(FileExistsError):
                render_installer(dist, output)
            (dist / 'hollis-0.7.1-darwin-arm64.zip').write_bytes(b'altered fixture')
            with self.assertRaisesRegex(ValueError, 'asset mismatch'):
                render_installer(dist, base / 'bad.sh')
            self.assertFalse((base / 'bad.sh').exists())

    def test_formula_uses_matched_bundle_hash_and_never_overwrites(self):
        with tempfile.TemporaryDirectory() as temp:
            base = Path(temp)
            dist = test_manifest.make_release_dir(base / 'dist')
            output = base / 'formula/hollis.rb'
            render_formula(dist, output)
            formula = output.read_text()
            bundle_hash = json.loads((dist / 'release-manifest.json').read_text())[
                'assets']['hollis-0.4.0-darwin-arm64.zip']['sha256']
            self.assertIn('releases/download/v0.4.0/', formula)
            self.assertIn(f'sha256 "{bundle_hash}"', formula)
            self.assertIn('hollis-0.4.0-darwin-arm64.zip', formula)
            self.assertIn('native_protocol_version"] != "4".to_i', formula)
            self.assertNotIn('{{', formula)
            with self.assertRaises(FileExistsError):
                render_formula(dist, output)

    def test_formula_renders_release_tracking_and_user_caveats(self):
        with tempfile.TemporaryDirectory() as temp:
            base = Path(temp)
            dist = test_manifest.make_release_dir(base / 'dist')
            output = render_formula(dist, base / 'hollis.rb')
            formula = output.read_text()
            self.assertIn('livecheck do\n    url :stable\n    strategy :github_latest\n  end', formula)
            self.assertIn('https://github.com/kamenxrider/hollis/releases/download/v0.4.0/', formula)
            self.assertIn('def caveats', formula)
            self.assertIn('hollis respond --model local "hello"', formula)
            self.assertIn('Shortcuts imports are optional.', formula)
            self.assertIn('For Cloud, Cloud Pro, On-Device, ChatGPT or images, run:', formula)
            self.assertIn('hollis setup --import', formula)
            self.assertIn('brew services start hollis', formula)
            self.assertIn('run [opt_bin/"hollis", "serve", "--addr", "127.0.0.1:1978", "--no-auth"]',
                          formula)
            self.assertIn('close any running hollis serve or chat', formula)
            self.assertIn('one-way database migration (schema v2 to v3)', formula)
            self.assertIn('Going back to v0.4.0', formula)
            self.assertIn('requires the backup-recovery procedure', formula)
            self.assertIn('brew cleanup removes the old version', formula)
            self.assertIn('bundle-internal hashes only, not GitHub build attestations', formula)
            self.assertIn('Use the curl installer with gh installed to verify GitHub build attestations.',
                          formula)
            root = Path(__file__).resolve().parents[2]
            for path, anchor in (
                    ('docs/cli.md', 'database-upgrades-and-recovery'),
                    ('packaging/homebrew/README.md', 'quick-start')):
                self.assertIn(f'https://github.com/kamenxrider/hollis/blob/main/{path}#{anchor}', formula)
                heading = anchor.replace('-', ' ')
                self.assertIn(heading, (root / path).read_text().lower())
            self.assertNotIn('{{', formula)

    def test_unmatched_release_bytes_block_formula_render(self):
        with tempfile.TemporaryDirectory() as temp:
            base = Path(temp)
            dist = test_manifest.make_release_dir(base / 'dist')
            (dist / 'hollis-0.4.0-darwin-arm64.zip').write_bytes(b'not the pinned bundle')
            with self.assertRaisesRegex(ValueError, 'asset mismatch'):
                render_formula(dist, base / 'out.rb')


if __name__ == '__main__':
    unittest.main()
