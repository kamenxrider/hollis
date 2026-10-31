"""Fresh plugin metadata and executable rollback fixtures, with no Apple calls."""
import json
from pathlib import Path
import shutil
import sqlite3
import unittest

import package_plugin
import test_native_package
import test_plugin


def snapshot(root):
    if not root.exists():
        return {}
    return {str(path.relative_to(root)): (
        path.stat().st_mode, path.stat().st_mtime_ns,
        path.read_bytes() if path.is_file() else None)
        for path in root.rglob('*')}


class RuntimeRecordTests(unittest.TestCase):
    def setUp(self):
        self.fixture = test_native_package.NativeInstallerTests('runTest')
        self.fixture.setUp()
        self.addCleanup(self.fixture.doCleanups)

    def record(self):
        result = self.fixture.call('runtime-record')
        self.assertEqual(result.stderr, '', result.stderr)
        payload = json.loads(result.stdout)
        self.assertEqual(payload['contract_version'], 1)
        self.assertEqual(result.returncode, 0 if payload['state'] == 'verified' else 10)
        return payload

    def update_receipt_hash(self, installed, key, filename):
        path = installed / 'runtime.lock.json'
        receipt = json.loads(path.read_text())
        receipt[key]['sha256'] = package_plugin.digest(installed / filename)
        path.write_text(json.dumps(receipt))

    def test_absent_runtime_is_action_required_without_creating_state(self):
        payload = self.record()
        self.assertEqual(payload['state'], 'action_required')
        self.assertEqual(payload['reason_code'], 'runtime_not_installed')
        self.assertFalse(self.fixture.home.exists())

    def test_verified_record_is_fresh_read_only_and_does_not_reenter_setup_lock(self):
        installed = self.fixture.install()
        state = Path(self.fixture.env['HOLLIS_STATE_DIR'])
        state.mkdir()
        (state / 'hollis.db').write_bytes(b'synthetic database must remain unchanged')
        lock = self.fixture.home / 'setup.lock'
        lock.mkdir()
        (lock / 'pid').write_text('fixture-parent')
        before_home, before_state = snapshot(self.fixture.home), snapshot(state)
        calls = self.fixture.base / 'record-cli-calls'
        self.fixture.env['CALL_LOG'] = str(calls)

        payload = self.record()

        self.assertEqual(payload['state'], 'verified')
        self.assertEqual(payload['runtime']['executable_path'], str(installed / 'hollis'))
        self.assertEqual(payload['runtime']['install_root'], str(installed))
        self.assertEqual(payload['runtime']['owner'], 'plugin')
        self.assertEqual(payload['artifact']['verification'], 'verified')
        self.assertEqual(payload['helper']['verification'], 'verified')
        self.assertEqual(payload['bridges']['verification'], 'verified')
        self.assertEqual(len(payload['bridges']['members']), 5)
        self.assertTrue(all(len(item['sha256']) == 64 for item in payload['bridges']['members']))
        self.assertEqual(calls.read_text().splitlines(), ['--version'])
        self.assertEqual(snapshot(self.fixture.home), before_home)
        self.assertEqual(snapshot(state), before_state)

        (installed / 'hollis').write_text('corrupted fixture CLI')
        payload = self.record()
        self.assertEqual(payload['state'], 'action_required')
        self.assertEqual(payload['reason_code'], 'runtime_integrity_failed')
        self.assertEqual(payload['artifact']['verification'], 'failed')
        self.assertEqual(calls.read_text().splitlines(), ['--version'])

    def test_helper_absence_or_corruption_does_not_downgrade_verified_cli(self):
        installed = self.fixture.install()
        helper = installed / 'hollis-native'
        original = helper.read_bytes()
        helper.unlink()
        payload = self.record()
        self.assertEqual(payload['state'], 'verified')
        self.assertEqual(payload['helper']['verification'], 'missing')
        helper.write_bytes(original + b'\n# corruption\n')
        helper.chmod(0o700)
        payload = self.record()
        self.assertEqual(payload['state'], 'verified')
        self.assertEqual(payload['helper']['verification'], 'failed')
        self.assertNotEqual(self.fixture.call('check').returncode, 0)

    def test_helper_identity_and_protocol_mismatch_are_independent(self):
        installed = self.fixture.install()
        helper = installed / 'hollis-native'
        helper.write_text(helper.read_text().replace('0.3.0', '0.9.0'))
        self.update_receipt_hash(installed, 'native', 'hollis-native')
        payload = self.record()
        self.assertEqual(payload['state'], 'verified')
        self.assertEqual(payload['helper']['reason_code'], 'helper_version_mismatch')
        helper.write_text(helper.read_text().replace('0.9.0', '0.3.0').replace('echo 1;;', 'echo 2;;'))
        self.update_receipt_hash(installed, 'native', 'hollis-native')
        payload = self.record()
        self.assertEqual(payload['helper']['reason_code'], 'helper_protocol_mismatch')

    def test_schema_one_receipt_reports_unpinned_helper_and_missing_bridges(self):
        installed = self.fixture.install()
        receipt_path = installed / 'runtime.lock.json'
        receipt = json.loads(receipt_path.read_text())
        receipt['schema_version'] = 1
        receipt.pop('native')
        receipt_path.write_text(json.dumps(receipt))
        (installed / 'hollis-bridges.zip').unlink()
        payload = self.record()
        self.assertEqual(payload['state'], 'verified')
        self.assertEqual(payload['helper']['verification'], 'missing')
        self.assertEqual(payload['helper']['reason_code'], 'helper_not_pinned')
        self.assertEqual(payload['bridges']['verification'], 'missing')

    def test_explicit_helper_version_is_independent_of_runtime_version(self):
        installed = self.fixture.install()
        helper = installed / 'hollis-native'
        helper.write_text(helper.read_text().replace('0.3.0', '0.4.0'))
        receipt_path = installed / 'runtime.lock.json'
        receipt = json.loads(receipt_path.read_text())
        receipt['native']['version'] = '0.4.0'
        receipt['native']['sha256'] = package_plugin.digest(helper)
        receipt_path.write_text(json.dumps(receipt))
        payload = self.record()
        self.assertEqual(payload['runtime']['version'], '0.3.0')
        self.assertEqual(payload['helper']['version'], '0.4.0')
        self.assertEqual(payload['helper']['verification'], 'verified')

    def test_malformed_receipt_bridge_pattern_is_rejected(self):
        installed = self.fixture.install()
        receipt_path = installed / 'runtime.lock.json'
        receipt = json.loads(receipt_path.read_text())
        receipt['bridge_files'][0] = '*.shortcut'
        receipt_path.write_text(json.dumps(receipt))
        payload = self.record()
        self.assertEqual(payload['state'], 'action_required')
        self.assertEqual(payload['reason_code'], 'runtime_integrity_failed')

    def external_runtime_fixture(self, version):
        external = self.fixture.base / 'external with spaces'
        external.mkdir(exist_ok=True)
        binary = external / 'caller-runtime'
        binary.write_text('#!/bin/bash\n[[ "$1" == --version ]] && echo "hollis %s"\n' % version)
        binary.chmod(0o700)
        link = external / 'hollis'
        if not link.is_symlink():
            link.symlink_to(binary.name)
        self.fixture.env['HOLLIS_CALLER_PATH'] = str(external)
        return binary

    def assert_external_unverified(self, payload, binary, version):
        self.assertEqual(payload['state'], 'unknown')
        self.assertEqual(payload['reason_code'], 'external_runtime_unverified')
        self.assertEqual(payload['runtime']['owner'], 'external')
        self.assertEqual(payload['runtime']['version'], version)
        self.assertEqual(payload['runtime']['executable_path'], str(binary))
        self.assertNotIn('name', payload['runtime'])
        self.assertNotIn('install_root', payload['runtime'])
        self.assertEqual(payload['platform'], {})
        self.assertEqual(payload['artifact']['verification'], 'unknown')
        self.assertNotIn('sha256', payload['artifact'])
        self.assertEqual(payload['helper']['verification'], 'unknown')
        self.assertEqual(payload['helper']['reason_code'], 'helper_unverified')
        self.assertEqual(payload['bridges']['verification'], 'unknown')
        self.assertEqual(payload['receipt']['owner'], 'external')
        self.assertNotIn('current_version', payload['receipt'])

    def test_external_without_receipt_is_unverified_at_same_or_older_version(self):
        for version in ('0.2.0', '0.3.0'):
            with self.subTest(version=version):
                binary = self.external_runtime_fixture(version)
                self.assert_external_unverified(self.record(), binary, version)
                self.assertFalse(self.fixture.home.exists())
                self.assertTrue(binary.exists())

    def test_unparseable_external_version_is_still_unverified_external(self):
        # A CLI whose --version does not end in an x.y.z token is still an
        # external runtime, never a missing managed installation.
        binary = self.external_runtime_fixture('dev-build')
        self.assert_external_unverified(self.record(), binary, 'dev-build')
        self.assertFalse(self.fixture.home.exists())

    def test_verified_plugin_install_is_preserved_over_same_or_older_external(self):
        installed = self.fixture.install()
        for version in ('0.2.0', '0.3.0', 'dev-build'):
            with self.subTest(version=version):
                self.external_runtime_fixture(version)
                payload = self.record()
                self.assertEqual(payload['state'], 'verified')
                self.assertEqual(payload['runtime']['owner'], 'plugin')
                self.assertEqual(payload['runtime']['executable_path'], str(installed / 'hollis'))
                self.assertEqual(payload['artifact']['verification'], 'verified')
                self.assertEqual(payload['helper']['verification'], 'verified')

    def test_newer_external_symlink_is_unknown_and_preserved(self):
        binary = self.external_runtime_fixture('9.0.0')
        self.assert_external_unverified(self.record(), binary, '9.0.0')
        self.assertFalse(self.fixture.home.exists())
        self.assertTrue(binary.exists())

    def test_spaces_are_supported_but_unsafe_managed_pointer_is_rejected(self):
        self.fixture.home = self.fixture.base / 'runtime with spaces'
        self.fixture.env['HOLLIS_PLUGIN_HOME'] = str(self.fixture.home)
        installed = self.fixture.install()
        self.assertEqual(self.record()['runtime']['executable_path'], str(installed / 'hollis'))
        current = self.fixture.home / 'current'
        current.unlink()
        target = self.fixture.base / 'untrusted-pointer'
        target.write_text('0.3.0\n')
        current.symlink_to(target)
        payload = self.record()
        self.assertEqual(payload['state'], 'action_required')
        self.assertEqual(payload['reason_code'], 'runtime_integrity_failed')
        self.assertEqual(target.read_text(), '0.3.0\n')


class ExecutableRollbackTests(unittest.TestCase):
    def test_pointer_switch_changes_only_executable_selection_not_database_or_config(self):
        fixture = test_plugin.InstallerTests('runTest')
        fixture.setUp()
        self.addCleanup(fixture.doCleanups)
        installed = fixture.install()
        retained = fixture.home / 'versions/0.2.0'
        shutil.copytree(installed, retained)
        old_cli = retained / 'hollis'
        old_cli.write_text(old_cli.read_text().replace('hollis 0.3.0', 'hollis 0.2.0'))
        receipt_path = retained / 'runtime.lock.json'
        receipt = json.loads(receipt_path.read_text())
        receipt.update(version='0.2.0', source_ref='refs/tags/v0.2.0')
        receipt['binary']['sha256'] = package_plugin.digest(old_cli)
        receipt_path.write_text(json.dumps(receipt))
        (fixture.home / 'previous').write_text('0.2.0\n')
        state = Path(fixture.env['HOLLIS_STATE_DIR'])
        state.mkdir()
        (state / 'config.json').write_text('{"bridges":{"cloud":"Preserved Custom Bridge"}}')
        database = state / 'hollis.db'
        with sqlite3.connect(database) as connection:
            connection.execute('PRAGMA user_version = 3')
            connection.execute('CREATE TABLE turns (body TEXT NOT NULL)')
            connection.execute('INSERT INTO turns VALUES (?)', ('synthetic post-migration turn',))
        before = snapshot(state)
        selected_before = fixture.call('path').stdout.strip()

        result = fixture.call('rollback')

        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertNotEqual(fixture.call('path').stdout.strip(), selected_before)
        self.assertEqual((fixture.home / 'current').read_text(), '0.2.0\n')
        self.assertEqual((fixture.home / 'previous').read_text(), '0.3.0\n')
        self.assertEqual(snapshot(state), before)
        self.assertIn('executables only', json.loads(result.stdout)['message'])
        with sqlite3.connect(database) as connection:
            self.assertEqual(connection.execute('PRAGMA user_version').fetchone()[0], 3)
            self.assertEqual(connection.execute('SELECT body FROM turns').fetchone()[0],
                             'synthetic post-migration turn')


if __name__ == '__main__':
    unittest.main()
