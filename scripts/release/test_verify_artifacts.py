from pathlib import Path
import hashlib
import json
import tempfile
import unittest

import test_manifest
import verify_artifacts


class ArtifactVerificationTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.dist = test_manifest.make_release_dir(Path(self.temp.name) / 'dist')

    def test_complete_offline_release_set_verifies_without_claiming_provenance(self):
        data = verify_artifacts.verify_release_dir(
            self.dist, 'refs/tags/v0.4.0', test_manifest.COMMIT)
        self.assertEqual(data['identity']['runtime_version'], '0.4.0')
        with self.assertRaisesRegex(ValueError, 'source commit'):
            verify_artifacts.verify_release_dir(self.dist, expected_commit='b' * 40)

    def test_asset_mutation_is_rejected(self):
        path = self.dist / 'hollis-darwin-arm64'
        path.write_bytes(path.read_bytes() + b' changed')
        with self.assertRaisesRegex(ValueError, 'asset mismatch'):
            verify_artifacts.verify_release_dir(self.dist)

    def test_manifest_and_sum_membership_are_exact(self):
        (self.dist / 'release-manifest.json').write_text('{}\n')
        with self.assertRaisesRegex(ValueError, 'schema'):
            verify_artifacts.verify_release_dir(self.dist)
        self.dist = test_manifest.make_release_dir(Path(self.temp.name) / 'fresh')
        (self.dist / 'SHA256SUMS').write_text('')
        with self.assertRaisesRegex(ValueError, 'membership'):
            verify_artifacts.verify_release_dir(self.dist)

    def test_bridge_member_hash_mismatch_is_rejected(self):
        manifest_path = self.dist / 'release-manifest.json'
        data = json.loads(manifest_path.read_text())
        data['bridges']['members'][0]['sha256'] = '0' * 64
        manifest_path.write_text(json.dumps(data, indent=2, sort_keys=True) + '\n')
        sums = (self.dist / 'SHA256SUMS').read_text().splitlines()
        sums = [line for line in sums if not line.endswith('release-manifest.json')]
        sums.append(f'{hashlib.sha256(manifest_path.read_bytes()).hexdigest()}  release-manifest.json')
        (self.dist / 'SHA256SUMS').write_text('\n'.join(sorted(sums, key=lambda line: line.split('  ', 1)[1])) + '\n')
        with self.assertRaisesRegex(ValueError, 'members disagree'):
            verify_artifacts.verify_release_dir(self.dist)

    def test_boolean_numeric_fields_are_rejected(self):
        manifest_path = self.dist / 'release-manifest.json'
        original = json.loads(manifest_path.read_text())
        for field, expected_error in (
                ('schema_version', 'schema'), ('protocol_version', 'protocol'),
                ('size_bytes', 'asset size')):
            with self.subTest(field=field):
                data = json.loads(json.dumps(original))
                if field == 'schema_version':
                    data[field] = True
                elif field == 'protocol_version':
                    data['helper'][field] = True
                else:
                    data['assets']['hollis-darwin-arm64'][field] = True
                manifest_path.write_text(json.dumps(data) + '\n')
                with self.assertRaisesRegex(ValueError, expected_error):
                    verify_artifacts.verify_release_dir(self.dist)


if __name__ == '__main__':
    unittest.main()
