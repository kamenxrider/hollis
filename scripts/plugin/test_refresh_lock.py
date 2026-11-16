"""Published pin refresh fixtures simulate provenance and never use the network."""
import json
from pathlib import Path
import shutil
import tempfile
import unittest
from unittest.mock import patch

import refresh_lock
import test_manifest


class RefreshLockTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.base = Path(self.temp.name)
        self.assets = test_manifest.make_release_dir(self.base / 'release')
        self.root = self.base / 'root'
        kit = self.root / 'plugins/hollis'
        kit.mkdir(parents=True)
        shutil.copy2(refresh_lock.ROOT / 'plugins/hollis/runtime.lock.json', kit / 'runtime.lock.json')

    def download_fixture(self, _lock, name, target):
        shutil.copyfile(self.assets / name, target)

    def test_schema_two_lock_is_derived_from_authenticated_common_record(self):
        output = self.base / 'new-lock.json'
        with patch.object(refresh_lock, 'ROOT', self.root), \
                patch.object(refresh_lock, 'download', side_effect=self.download_fixture), \
                patch.object(refresh_lock, 'authenticate', return_value=[{'simulated_verification': True}]) as auth:
            refresh_lock.refresh('0.4.0', test_manifest.COMMIT, output)
        self.assertEqual(auth.call_args_list[0].args[0].name, 'release-manifest.json')
        record = json.loads((self.assets / 'release-manifest.json').read_text())
        lock = json.loads(output.read_text())
        self.assertEqual(lock['schema_version'], 2)
        self.assertEqual(lock['source_commit'], test_manifest.COMMIT)
        self.assertEqual(lock['native']['version'], test_manifest.manifest.HELPER_VERSION)
        self.assertEqual(lock['native']['protocol_version'], test_manifest.manifest.HELPER_PROTOCOL)
        for key in ('binary', 'bridges', 'native'):
            self.assertEqual(lock[key]['sha256'], record['assets'][lock[key]['name']]['sha256'])
        self.assertEqual(lock['bridge_files'], [item['name'] for item in record['bridges']['members']])

    def test_manifest_authentication_failure_writes_no_lock(self):
        output = self.base / 'new-lock.json'
        with patch.object(refresh_lock, 'ROOT', self.root), \
                patch.object(refresh_lock, 'download', side_effect=self.download_fixture) as download, \
                patch.object(refresh_lock, 'authenticate', side_effect=ValueError('simulated provenance failure')):
            with self.assertRaisesRegex(ValueError, 'provenance'):
                refresh_lock.refresh('0.4.0', test_manifest.COMMIT, output)
        self.assertEqual(download.call_count, 1)
        self.assertFalse(output.exists())

    def test_disagreeing_identity_is_rejected_after_simulated_authentication(self):
        output = self.base / 'new-lock.json'
        with patch.object(refresh_lock, 'ROOT', self.root), \
                patch.object(refresh_lock, 'download', side_effect=self.download_fixture), \
                patch.object(refresh_lock, 'authenticate', return_value=[{'simulated_verification': True}]):
            with self.assertRaisesRegex(ValueError, 'source commit'):
                refresh_lock.refresh('0.4.0', 'b' * 40, output)
        self.assertFalse(output.exists())

    def test_070_refresh_renders_all_distribution_pins_from_authenticated_assets(self):
        with patch.object(test_manifest, 'VERSION', '0.7.1'):
            self.assets = test_manifest.make_release_dir(self.base / 'release-070')
        output = self.base / 'runtime.lock.json'
        distribution = self.base / 'distribution'
        with patch.object(refresh_lock, 'ROOT', self.root), \
                patch.object(refresh_lock, 'download', side_effect=self.download_fixture), \
                patch.object(refresh_lock, 'authenticate', return_value=[{'simulated_verification': True}]) as auth:
            refresh_lock.refresh('0.7.1', test_manifest.COMMIT, output, distribution)
        self.assertEqual({call.args[0].name for call in auth.call_args_list},
                         refresh_lock.expected_asset_names('0.7.1') | {'release-manifest.json', 'SHA256SUMS'})
        self.assertEqual(auth.call_count, len(refresh_lock.expected_asset_names('0.7.1')) + 2)
        record = json.loads((self.assets / 'release-manifest.json').read_text())
        lock = json.loads(output.read_text())
        self.assertEqual(lock['version'], '0.7.1')
        self.assertEqual(lock['native']['protocol_version'], 4)
        self.assertEqual(len(lock['bridge_files']), 9)
        installer = (distribution / 'install.sh').read_text()
        formula = (distribution / 'hollis.rb').read_text()
        bundle_hash = record['assets']['hollis-0.7.1-darwin-arm64.zip']['sha256']
        for pin in ('version=0.7.1', 'protocol=4', f'source_commit={test_manifest.COMMIT}',
                    f'asset_sha256={bundle_hash}'):
            self.assertIn(pin + '\n', installer)
        self.assertIn(f'sha256 "{bundle_hash}"', formula)
        self.assertIn('releases/download/v0.7.1/', formula)
        for key in ('binary', 'native', 'bridges'):
            self.assertEqual(lock[key]['sha256'], record['assets'][lock[key]['name']]['sha256'])

    def test_late_authentication_failure_writes_no_distribution_or_lock(self):
        output = self.base / 'new-lock.json'
        distribution = self.base / 'distribution'
        def authenticate(path, _lock):
            if path.name == 'hollis-native-darwin-arm64':
                raise ValueError('simulated provenance failure')
            return [{'simulated_verification': True}]
        with patch.object(refresh_lock, 'ROOT', self.root), \
                patch.object(refresh_lock, 'download', side_effect=self.download_fixture), \
                patch.object(refresh_lock, 'authenticate', side_effect=authenticate):
            with self.assertRaisesRegex(ValueError, 'provenance'):
                refresh_lock.refresh('0.4.0', test_manifest.COMMIT, output, distribution)
        self.assertFalse(output.exists())
        self.assertFalse(distribution.exists())

    def test_existing_outputs_are_never_overwritten(self):
        for kind in ('lock', 'distribution'):
            with self.subTest(kind=kind):
                output = self.base / f'{kind}-lock.json'
                distribution = self.base / f'{kind}-distribution'
                target = output if kind == 'lock' else distribution
                target.write_text('preserve fixture')
                with patch.object(refresh_lock, 'download') as download:
                    with self.assertRaises(FileExistsError):
                        refresh_lock.refresh('0.7.1', test_manifest.COMMIT, output, distribution)
                download.assert_not_called()
                self.assertEqual(target.read_text(), 'preserve fixture')


if __name__ == '__main__':
    unittest.main()
