"""Native packaging checks use synthetic files, never Apple or installed state."""
import json
import importlib.util
from pathlib import Path
import shutil
import tempfile
import unittest
from unittest.mock import patch
import zipfile

import package_plugin as packaging
import package_runtime
import test_plugin as installer_fixture
import test_package_archive as archive_fixture

_privacy_spec = importlib.util.spec_from_file_location('release_privacy', Path(__file__).resolve().parents[1] / 'release/privacy.py')
privacy = importlib.util.module_from_spec(_privacy_spec)
_privacy_spec.loader.exec_module(privacy)


class NativeInstallerTests(unittest.TestCase):
    def setUp(self):
        installer_fixture.InstallerTests.setUp(self)
        self.lock['schema_version'] = 2
        self.helper = self.assets / 'hollis-native-darwin-arm64'
        self.helper.write_text('#!/bin/bash\ncase "$1" in\n--version) echo "hollis-native 0.3.0";;\n--protocol-version) echo 1;;\n*) exit 88;;\nesac\n')
        self.helper.chmod(0o700)
        self.lock['native'] = {'name': self.helper.name, 'protocol_version': 1,
                               'sha256': packaging.digest(self.helper)}
        self.relock()

    call = installer_fixture.InstallerTests.call
    relock = installer_fixture.InstallerTests.relock
    install = installer_fixture.InstallerTests.install

    def test_install_pairs_helper_and_verifies_repeatedly(self):
        installed = self.install()
        self.assertEqual((installed / 'hollis-native').read_bytes(), self.helper.read_bytes())
        self.assertEqual((installed / 'hollis-native').stat().st_mode & 0o777, 0o700)
        self.install()
        (installed / 'hollis-native').write_text('# corruption')
        for command in ('check', 'path', 'install'):
            result = self.call(command)
            self.assertNotEqual(result.returncode, 0, command)
            self.assertIn('Integrity', result.stderr)

    def test_pending_source_install_does_not_install_old_runtime(self):
        manifest_path = self.kit / 'plugin.json'
        manifest = json.loads(manifest_path.read_text()); manifest['version'] = '0.2.0'
        manifest_path.write_text(json.dumps(manifest))
        result = self.call('install')
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('awaits verified', result.stderr)
        self.assertFalse((self.home / 'current').exists())

    def test_newer_external_runtime_keeps_own_helper(self):
        self.install()
        external = self.base / 'external'; external.mkdir()
        executable = external / 'hollis'
        executable.write_text('#!/bin/bash\nif [[ "$1" == --version ]]; then echo "hollis 9.0.0"; fi\n')
        executable.chmod(0o700)
        helper = external / 'hollis-native'; helper.write_text('caller-owned helper')
        self.env['HOLLIS_CALLER_PATH'] = str(external)
        result = self.call('path')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout.strip(), str(executable))
        self.assertEqual(helper.read_text(), 'caller-owned helper')
        self.assertEqual(self.call('install').returncode, 0)
        self.assertEqual(helper.read_text(), 'caller-owned helper')

    def test_wrong_protocol_refuses_before_pointer_switch(self):
        self.helper.write_text(self.helper.read_text().replace('echo 1;;', 'echo 2;;'))
        self.lock['native']['sha256'] = packaging.digest(self.helper)
        self.relock()
        result = self.call('install')
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('protocol', result.stderr)
        self.assertFalse((self.home / 'current').exists())
        self.assertFalse(list((self.home / 'versions').glob('.stage.*')))

    def test_wrong_helper_version_refuses_install(self):
        self.helper.write_text(self.helper.read_text().replace('0.3.0', '0.4.0'))
        self.lock['native']['sha256'] = packaging.digest(self.helper)
        self.relock()
        self.assertNotEqual(self.call('install').returncode, 0)
        self.assertFalse((self.home / 'current').exists())

    def test_rollback_corrupt_helper_leaves_current_unchanged(self):
        installed = self.install()
        previous = self.home / 'versions/0.2.0'
        shutil.copytree(installed, previous)
        receipt = json.loads((previous / 'runtime.lock.json').read_text())
        receipt['version'] = '0.2.0'
        (previous / 'runtime.lock.json').write_text(json.dumps(receipt))
        (previous / 'hollis-native').write_text('corruption')
        (self.home / 'previous').write_text('0.2.0\n')
        result = self.call('rollback')
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual((self.home / 'current').read_text(), '0.3.0\n')
        self.assertEqual(json.loads(result.stdout)['rollback']['status'], 'unavailable')

    def test_local_status_never_lists_shortcuts_or_imports(self):
        self.install()
        log = self.base / 'calls'
        self.env['CALL_LOG'] = str(log)
        # Any accidental use of the absolute Shortcuts helper fails the test.
        bridge = self.kit / 'scripts/bridges.sh'
        bridge.write_text(bridge.read_text().replace('/usr/bin/shortcuts', '/nonexistent-shortcuts'))
        result = self.call('status', 'local')
        self.assertEqual(result.returncode, 0, result.stderr)
        calls = log.read_text().splitlines()
        self.assertIn('models --model local --json', calls)
        self.assertFalse(any(c.startswith('config') or c.startswith('doctor') for c in calls))
        self.assertNotEqual(self.call('import', 'local').returncode, 0)


class NativeArchiveTests(unittest.TestCase):
    def test_pending_pin_prevents_releasing_old_runtime_under_new_plugin(self):
        fixture = archive_fixture.PackageArchiveTests(methodName='runTest')
        fixture.setUp()
        self.addCleanup(fixture.doCleanups)
        for name in ('plugin.json', '.claude-plugin/plugin.json', '.codex-plugin/plugin.json'):
            path = fixture.kit / name
            data = json.loads(path.read_text()); data['version'] = '0.2.0'
            path.write_text(json.dumps(data))
        with self.assertRaisesRegex(ValueError, 'awaits verified'):
            packaging.package(fixture.fixture_root, fixture.output)

    def test_native_asset_authenticated_and_bundled_private_extras_excluded(self):
        fixture = archive_fixture.PackageArchiveTests(methodName='runTest')
        fixture.setUp()
        self.addCleanup(fixture.doCleanups)
        lock = fixture.lock
        lock.update(schema_version=2, version='0.4.0', source_ref='refs/tags/v0.4.0',
                    release_url='https://github.com/kamenxrider/hollis/releases/download/v0.4.0')
        helper = fixture.assets / 'hollis-native-darwin-arm64'
        helper.write_bytes(b'synthetic helper')
        lock['native'] = {'name': helper.name, 'sha256': packaging.digest(helper), 'protocol_version': 1}
        (fixture.kit / 'runtime.lock.json').write_text(json.dumps(lock))
        for name in ('plugin.json', '.claude-plugin/plugin.json', '.codex-plugin/plugin.json'):
            path = fixture.kit / name
            data = json.loads(path.read_text()); data['version'] = '0.2.0'
            path.write_text(json.dumps(data))
        private = fixture.kit / 'docs/dev/private.md'
        private.parent.mkdir(); private.write_text('private canary')
        unexpected = fixture.kit / 'docs/research-notes.md'; unexpected.write_text('private canary')
        with patch.object(packaging, 'authenticate', return_value=[{'test_fixture': True}]) as auth:
            output = packaging.package(fixture.fixture_root, fixture.output, fixture.assets)
        self.assertEqual(auth.call_count, 3)
        # Check the real archive and sidecars with the publication gate. The
        # review authority here covers only these explicitly synthetic fixtures.
        packager = fixture.fixture_root / 'scripts/plugin/package_plugin.py'
        packager.parent.mkdir(parents=True)
        shutil.copy2(Path(packaging.__file__), packager)
        reviewed = {rel.as_posix(): packaging.digest(path)
                    for rel, path in packaging.source_files(fixture.fixture_root)}
        review_manifest = fixture.fixture_root / privacy.MANIFEST
        review_manifest.parent.mkdir(parents=True)
        review_manifest.write_text(json.dumps({'schema_version': 1, 'files': reviewed}))
        privacy.check_archive(output, 'plugin', fixture.fixture_root)
        # The legacy fixture's earlier package belongs in a different output
        # directory, so inspect the current package's three publication files.
        publication = fixture.base / 'publication'
        publication.mkdir()
        for suffix in ('.zip', '.sha256', '.runtime-provenance.json'):
            name = 'hollis-plugin-0.2.0' + suffix
            shutil.copy2(fixture.output / name, publication / name)
        privacy.check_release_dir(publication, 'plugin', fixture.fixture_root)
        (publication / 'unreviewed.txt').write_text('private synthetic canary')
        with self.assertRaises(ValueError):
            privacy.check_release_dir(publication, 'plugin', fixture.fixture_root)
        with zipfile.ZipFile(output) as archive:
            self.assertTrue(any(n.endswith('/hollis-native-darwin-arm64') for n in archive.namelist()))
            for name in archive.namelist():
                self.assertNotIn('private canary', archive.read(name).decode(errors='ignore'))
            entry = next(i for i in archive.infolist() if i.filename.endswith('/hollis-native-darwin-arm64'))
            self.assertEqual(entry.external_attr >> 16 & 0o777, 0o755)

    def test_runtime_bundle_exact_members_hashes_and_no_checkout_walk(self):
        with tempfile.TemporaryDirectory() as temp:
            dist = Path(temp)
            for name in ('hollis-darwin-arm64', 'hollis-native-darwin-arm64', 'hollis-bridges.zip'):
                (dist / name).write_text(name)
            (dist / 'private.txt').write_text('private canary')
            output = package_runtime.package(dist, '0.4.0')
            with zipfile.ZipFile(output) as archive:
                self.assertEqual(set(archive.namelist()), {'hollis', 'hollis-native', 'hollis-bridges.zip', 'LICENSE', 'manifest.json'})
                manifest = json.loads(archive.read('manifest.json'))
                self.assertEqual(manifest['native_protocol_version'], 1)
                for name, expected in manifest['sha256'].items():
                    import hashlib
                    self.assertEqual(hashlib.sha256(archive.read(name)).hexdigest(), expected)
            with self.assertRaises(FileExistsError):
                package_runtime.package(dist, '0.4.0')
