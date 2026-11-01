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


if __name__ == '__main__':
    unittest.main()
