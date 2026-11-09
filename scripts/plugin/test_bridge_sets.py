"""Exact legacy/current bridge sets using fabricated assets and local stand-ins."""
import json
from pathlib import Path
import shlex
import unittest
import warnings
import zipfile

import package_plugin
import test_native_package


TEXT_ROUTES = (('cloud', 'Cloud'), ('cloud-pro', 'Cloud Pro'),
               ('on-device', 'On-Device'), ('chatgpt', 'ChatGPT'))
LEGACY = [
    'AFM Bridge - Cloud.shortcut', 'AFM Bridge - Cloud Pro.shortcut',
    'AFM Bridge - On-Device.shortcut', 'AFM Bridge - ChatGPT.shortcut',
    'Hollis Image - Reference Input v2.shortcut',
]
CURRENT = LEGACY + [
    f'AFM Bridge JSON - {route}.shortcut'
    for route in ('Cloud', 'Cloud Pro', 'On-Device', 'ChatGPT')
]


class BridgeSetTests(unittest.TestCase):
    def setUp(self):
        self.fixture = test_native_package.NativeInstallerTests('runTest')
        self.fixture.setUp()
        self.addCleanup(self.fixture.doCleanups)
        self.configure(CURRENT, '0.7.0')

    def archive(self, names):
        with warnings.catch_warnings():
            warnings.simplefilter('ignore', UserWarning)
            with zipfile.ZipFile(self.fixture.zip, 'w') as archive:
                for name in names:
                    archive.writestr(name, 'synthetic shortcut fixture')

    def configure(self, names, version):
        fixture = self.fixture
        previous = fixture.lock['version']
        fixture.binary.write_text(fixture.binary.read_text().replace(
            'hollis ' + previous, 'hollis ' + version))
        protocol = 4 if version == '0.7.0' else 1
        fixture.helper.write_text(
            '#!/bin/bash\ncase "$1" in\n'
            f'--version) echo "hollis-native {version}";;\n'
            f'--protocol-version) echo {protocol};;\n*) exit 88;;\nesac\n')
        fixture.lock.update(
            version=version, source_ref='refs/tags/v' + version,
            source_commit='a' * 40,
            release_url='https://github.com/kamenxrider/hollis/releases/download/v' + version,
            bridge_files=list(names))
        fixture.lock['native'].update(
            protocol_version=protocol, sha256=package_plugin.digest(fixture.helper))
        manifest_path = fixture.kit / 'plugin.json'
        manifest = json.loads(manifest_path.read_text())
        manifest['version'] = '0.4.0' if version == '0.7.0' else '0.1.0'
        manifest_path.write_text(json.dumps(manifest))
        self.archive(names)
        fixture.relock()

    def install(self):
        result = self.fixture.call('install')
        self.assertEqual(result.returncode, 0, result.stderr)
        return self.fixture.home / 'versions' / self.fixture.lock['version']

    def record(self, expected=0):
        result = self.fixture.call('runtime-record')
        self.assertEqual(result.returncode, expected, result.stderr)
        return json.loads(result.stdout)

    def test_nine_bridge_install_stages_all_members_and_emits_verified_receipt(self):
        installed = self.install()
        self.assertEqual(sorted(p.name for p in (installed / 'bridges').iterdir()), sorted(CURRENT))
        payload = self.record()
        self.assertEqual(payload['bridges']['verification'], 'verified')
        self.assertEqual(sorted(p['name'] for p in payload['bridges']['members']), sorted(CURRENT))
        self.assertEqual(payload['helper']['protocol'], 4)
        self.assertFalse(list((self.fixture.home / 'versions').glob('.stage.*')))

    def test_legacy_five_bridge_install_and_receipt_remain_accepted(self):
        self.configure(LEGACY, '0.4.0')
        self.install()
        payload = self.record()
        self.assertEqual(payload['bridges']['verification'], 'verified')
        self.assertEqual(len(payload['bridges']['members']), 5)

    def test_receipt_uses_its_own_bridge_set_not_the_package_lock(self):
        self.install()
        self.fixture.lock['bridge_files'] = LEGACY
        self.fixture.relock()
        self.assertEqual(len(self.record()['bridges']['members']), 9)

    def test_lock_rejects_six_eight_and_ten_entries_before_staging(self):
        for names in (CURRENT[:6], CURRENT[:8], CURRENT + ['Extra.shortcut']):
            with self.subTest(count=len(names)):
                self.fixture.lock['bridge_files'] = names
                self.fixture.relock()
                result = self.fixture.call('install')
                self.assertEqual(result.returncode, 10, result.stderr)
                self.assertFalse(self.fixture.home.exists())

    def test_receipt_rejects_six_eight_and_ten_entries(self):
        installed = self.install()
        path = installed / 'runtime.lock.json'
        receipt = json.loads(path.read_text())
        for names in (CURRENT[:6], CURRENT[:8], CURRENT + ['Extra.shortcut']):
            with self.subTest(count=len(names)):
                path.write_text(json.dumps(dict(receipt, bridge_files=names)))
                self.assertEqual(self.record(10)['reason_code'], 'runtime_integrity_failed')

    def test_lock_rejects_duplicate_unsafe_or_noncanonical_names(self):
        for invalid in (CURRENT[0], '../outside.shortcut', '*.shortcut', 'Other.shortcut',
                        'line\nbreak.shortcut', 'tab\tname.shortcut', 'back\\slash.shortcut'):
            with self.subTest(name=invalid):
                self.fixture.lock['bridge_files'] = CURRENT[:-1] + [invalid]
                self.fixture.relock()
                self.assertEqual(self.fixture.call('install').returncode, 10)
                self.assertFalse(self.fixture.home.exists())

    def test_receipt_rejects_duplicate_and_nonarray_members(self):
        installed = self.install()
        path = installed / 'runtime.lock.json'
        receipt = json.loads(path.read_text())
        for names in (CURRENT[:-1] + [CURRENT[0]], dict(enumerate(LEGACY))):
            with self.subTest(names=names):
                path.write_text(json.dumps(dict(receipt, bridge_files=names)))
                self.assertEqual(self.record(10)['state'], 'action_required')

    def test_staging_rejects_trailing_sixth_eighth_or_tenth_archive_member(self):
        for version, names, count in (('0.4.0', LEGACY, 6), ('0.4.0', LEGACY, 8),
                                      ('0.7.0', CURRENT, 10)):
            with self.subTest(count=count):
                self.configure(names, version)
                extras = [f'Extra-{i}.shortcut' for i in range(count - len(names))]
                self.archive(names + extras)
                self.fixture.relock()
                result = self.fixture.call('install')
                self.assertEqual(result.returncode, 10, result.stderr)
                self.assertIn('exactly the pinned bridges', result.stderr)
                self.assertFalse((self.fixture.home / 'current').exists())
                self.assertFalse(list((self.fixture.home / 'versions').glob('.stage.*')))

    def test_staging_and_receipt_reject_duplicate_archive_entries(self):
        self.archive(CURRENT + [CURRENT[0]])
        self.fixture.relock()
        self.assertEqual(self.fixture.call('install').returncode, 10)
        self.configure(CURRENT, '0.7.0')
        installed = self.install()
        self.archive(CURRENT + [CURRENT[0]])
        target = installed / 'hollis-bridges.zip'
        target.write_bytes(self.fixture.zip.read_bytes())
        path = installed / 'runtime.lock.json'
        receipt = json.loads(path.read_text())
        receipt['bridges']['sha256'] = package_plugin.digest(target)
        path.write_text(json.dumps(receipt))
        self.assertEqual(self.record()['bridges']['verification'], 'mismatch')

    def route_helpers(self):
        fixture = self.fixture
        fixture.env['OPEN_LOG'] = str(fixture.base / 'opened')
        helper = fixture.base / 'shortcut-stand-in'
        helper.write_text(
            '#!/bin/bash\ncase "$1" in\n'
            'list) printf "%s\\n" "${FIXTURE_LIST:-}";;\n'
            'sign) shift; previous=; input=; output=; for arg; do\n'
            '  case "$previous" in --input) input=$arg;; --output) output=$arg;; esac\n'
            '  previous=$arg; done; cp "$input" "$output";;\n*) exit 97;;\nesac\n')
        helper.chmod(0o700)
        opener = fixture.base / 'open-stand-in'
        opener.write_text('#!/bin/bash\nprintf "%s\\n" "${1##*/}" >> "$OPEN_LOG"\n')
        opener.chmod(0o700)
        path = fixture.kit / 'scripts/bridges.sh'
        path.write_text(path.read_text().replace('/usr/bin/shortcuts', shlex.quote(str(helper)))
                        .replace('/usr/bin/open', shlex.quote(str(opener))))

    def opened(self):
        path = Path(self.fixture.env['OPEN_LOG'])
        return path.read_text().splitlines() if path.exists() else []

    def prepare_discovery(self):
        self.fixture.binary.write_text(self.fixture.binary.read_text().replace(
            '"verified":true', '"verified":false'))
        self.fixture.relock()
        self.route_helpers()
        self.install()

    def test_signed_json_is_discovered_without_reopening_imports(self):
        self.prepare_discovery()
        for route, title in TEXT_ROUTES:
            with self.subTest(route=route):
                self.fixture.env['FIXTURE_LIST'] = (
                    f'AFM Bridge - {title}\nAFM Bridge JSON - {title}.signed')
                result = self.fixture.call('import', route)
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(json.loads(result.stdout)['status'], 'discovered')
                self.assertEqual(self.opened(), [])
                self.assertFalse((self.fixture.home / 'imports/pending').exists())

    def test_bare_json_is_discovered_without_reopening_imports(self):
        self.prepare_discovery()
        for route, title in TEXT_ROUTES:
            with self.subTest(route=route):
                self.fixture.env['FIXTURE_LIST'] = (
                    f'AFM Bridge - {title}\nAFM Bridge JSON - {title}')
                result = self.fixture.call('import', route)
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(json.loads(result.stdout)['status'], 'discovered')
                self.assertEqual(self.opened(), [])

    def test_missing_json_imports_the_bare_archive_member(self):
        self.prepare_discovery()
        for route, title in TEXT_ROUTES:
            with self.subTest(route=route):
                self.fixture.env['FIXTURE_LIST'] = f'AFM Bridge - {title}'
                before = len(self.opened())
                result = self.fixture.call('import', route)
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(json.loads(result.stdout)['status'], 'permission_pending')
                self.assertEqual(self.opened()[before:], [
                    f'AFM Bridge JSON - {title}.shortcut'])

    def test_signed_text_and_json_are_discovered_together(self):
        self.prepare_discovery()
        for route, title in TEXT_ROUTES:
            with self.subTest(route=route):
                text = f'AFM Bridge - {title}.signed'
                self.fixture.env['FIXTURE_LIST'] = text + f'\nAFM Bridge JSON - {title}.signed'
                result = self.fixture.call('import', route)
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(json.loads(result.stdout)['status'], 'discovered')
                self.assertEqual(self.opened(), [])
                discovered = self.fixture.home / 'imports' / (route + '.discovered')
                self.assertEqual(discovered.read_text().strip(), text)

    def test_each_text_route_selects_text_and_json_without_importing_other_routes(self):
        self.fixture.binary.write_text(self.fixture.binary.read_text().replace(
            '"verified":true', '"verified":false'))
        self.fixture.relock()
        self.route_helpers()
        self.install()
        for route, title in (('cloud', 'Cloud'), ('cloud-pro', 'Cloud Pro'),
                             ('on-device', 'On-Device'), ('chatgpt', 'ChatGPT')):
            with self.subTest(route=route):
                before = len(self.opened())
                result = self.fixture.call('import', route)
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(self.opened()[before:], [
                    f'AFM Bridge - {title}.shortcut', f'AFM Bridge JSON - {title}.shortcut'])
                self.assertEqual(json.loads(result.stdout)['status'], 'permission_pending')

    def test_route_skips_each_discovered_variant_and_clears_completed_pending(self):
        self.route_helpers()
        self.install()
        text, json_name = 'AFM Bridge - Cloud Pro', 'AFM Bridge JSON - Cloud Pro'
        first = self.fixture.call('import', 'cloud-pro')
        self.assertEqual(first.returncode, 0, first.stderr)
        before = self.opened()
        waiting = self.fixture.call('import', 'cloud-pro')
        self.assertEqual(json.loads(waiting.stdout)['status'], 'permission_pending')
        self.assertEqual(self.opened(), before)
        self.fixture.env['FIXTURE_LIST'] = text
        reopened = self.fixture.call('import', 'cloud-pro', '--reopen')
        self.assertEqual(reopened.returncode, 0, reopened.stderr)
        self.assertEqual(self.opened()[len(before):], [json_name + '.shortcut'])
        self.fixture.env['FIXTURE_LIST'] = text + '\n' + json_name
        completed = self.fixture.call('import', 'cloud-pro')
        self.assertEqual(json.loads(completed.stdout)['status'], 'discovered')
        self.assertFalse((self.fixture.home / 'imports/pending').exists())
        self.fixture.env['FIXTURE_LIST'] = json_name
        before = len(self.opened())
        result = self.fixture.call('import', 'cloud-pro')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.opened()[before:], [text + '.shortcut'])

    def test_verified_custom_text_is_preserved_while_missing_json_is_imported(self):
        self.route_helpers()
        self.install()
        self.fixture.env['FIXTURE_CONFIG'] = '{"bridges":{"cloud":"Custom Cloud"},"image_bridge":""}'
        result = self.fixture.call('import', 'cloud')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.opened(), ['AFM Bridge JSON - Cloud.shortcut'])

    def test_image_and_legacy_text_routes_still_select_one_bridge(self):
        self.configure(LEGACY, '0.4.0')
        self.route_helpers()
        self.install()
        for route, name in (('cloud-pro', 'AFM Bridge - Cloud Pro'),
                             ('image', 'Hollis Image - Reference Input v2')):
            with self.subTest(route=route):
                before = len(self.opened())
                result = self.fixture.call('import', route)
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(self.opened()[before:], [name + '.shortcut'])


if __name__ == '__main__':
    unittest.main()
