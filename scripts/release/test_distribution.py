from pathlib import Path
import json
import tempfile
import unittest

import test_manifest
from prepare_distribution import render_formula


class DistributionPreparationTests(unittest.TestCase):
    def test_formula_uses_matched_bundle_hash_and_never_overwrites(self):
        with tempfile.TemporaryDirectory() as temp:
            base = Path(temp)
            dist = test_manifest.make_release_dir(base / 'dist')
            output = base / 'formula/hollis.rb'
            render_formula(dist, output)
            formula = output.read_text()
            bundle_hash = json.loads((dist / 'release-manifest.json').read_text())[
                'assets']['hollis-0.4.0-darwin-arm64.zip']['sha256']
            self.assertIn('version "0.4.0"', formula)
            self.assertIn(f'sha256 "{bundle_hash}"', formula)
            self.assertIn('hollis-0.4.0-darwin-arm64.zip', formula)
            self.assertNotIn('{{', formula)
            with self.assertRaises(FileExistsError):
                render_formula(dist, output)

    def test_unmatched_release_bytes_block_formula_render(self):
        with tempfile.TemporaryDirectory() as temp:
            base = Path(temp)
            dist = test_manifest.make_release_dir(base / 'dist')
            (dist / 'hollis-0.4.0-darwin-arm64.zip').write_bytes(b'not the pinned bundle')
            with self.assertRaisesRegex(ValueError, 'asset mismatch'):
                render_formula(dist, base / 'out.rb')


if __name__ == '__main__':
    unittest.main()
