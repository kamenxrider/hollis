import hashlib
import json
from pathlib import Path
import tempfile
import unittest
import zipfile

import manifest

VERSION = '0.4.0'
COMMIT = 'a' * 40


def _zip_bytes(entries):
    from io import BytesIO
    output = BytesIO()
    with zipfile.ZipFile(output, 'w', zipfile.ZIP_DEFLATED) as archive:
        for name, content in entries.items():
            archive.writestr(name, content)
    return output.getvalue()


def make_release_dir(folder):
    folder = Path(folder)
    folder.mkdir(parents=True, exist_ok=True)
    bridge_contents = {name: ('fixture:' + name).encode()
                       for name in manifest.BRIDGE_MEMBERS}
    bridges = _zip_bytes(bridge_contents)
    core = {
        'hollis': b'synthetic arm64 CLI',
        'hollis-native': b'synthetic helper',
        'hollis-bridges.zip': bridges,
        'LICENSE': b'Apache-2.0 fixture',
    }
    inner = {
        'version': VERSION,
        'native_protocol_version': manifest.HELPER_PROTOCOL,
        'native_minimum_macos': '27.0',
        'architecture': 'arm64',
        'sha256': {name: hashlib.sha256(content).hexdigest() for name, content in core.items()},
    }
    bundle_entries = dict(core)
    bundle_entries['manifest.json'] = (json.dumps(inner, sort_keys=True) + '\n').encode()
    bundle_name = f'hollis-{VERSION}-darwin-arm64.zip'
    payload = {
        'hollis-darwin-arm64': core['hollis'],
        'hollis-darwin-amd64': b'synthetic release CLI x86_64',
        'hollis-native-darwin-arm64': core['hollis-native'],
        'hollis-bridges.zip': bridges,
        'hollis.spdx.json': b'{"fixture":true}\n',
        bundle_name: _zip_bytes(bundle_entries),
    }
    for name, content in payload.items():
        (folder / name).write_bytes(content)

    def macho(_path, architecture):
        return {'architecture': architecture, 'deployment_target': '27.0', 'sdk_version': '27.2'}

    signatures = {
        'hollis-darwin-arm64': 'ad_hoc',
        'hollis-darwin-amd64': 'unsigned',
        'hollis-native-darwin-arm64': 'ad_hoc',
    }
    record = manifest.create_manifest(
        folder, VERSION, f'refs/tags/v{VERSION}', COMMIT,
        macho_inspector=macho,
        signature_inspector=lambda path: signatures[path.name],
    )
    manifest_path = folder / 'release-manifest.json'
    manifest_path.write_text(json.dumps(record, indent=2, sort_keys=True) + '\n')
    sums = payload | {'release-manifest.json': manifest_path.read_bytes()}
    lines = [f'{hashlib.sha256(content).hexdigest()}  {name}' for name, content in sorted(sums.items())]
    (folder / 'SHA256SUMS').write_text('\n'.join(lines) + '\n')
    return folder


class ManifestTests(unittest.TestCase):
    def test_helper_version_and_protocol_are_explicit_source_pins(self):
        source = (Path(__file__).resolve().parents[2] / 'native/HollisNative.swift').read_text()
        self.assertIn(f'static let version = "{manifest.HELPER_VERSION}"', source)
        self.assertIn(f'static let protocolVersion = {manifest.HELPER_PROTOCOL}', source)

    def test_manifest_records_final_bytes_and_preserves_bundle_contract(self):
        with tempfile.TemporaryDirectory() as temp:
            dist = make_release_dir(Path(temp) / 'dist')
            raw = json.loads((dist / 'release-manifest.json').read_text())
            self.assertEqual(raw['schema_version'], 1)
            self.assertEqual(raw['identity']['source_ref'], 'refs/tags/v0.4.0')
            self.assertEqual(raw['identity']['source_commit'], COMMIT)
            self.assertEqual(raw['helper'], {'version': manifest.HELPER_VERSION,
                                              'protocol_version': manifest.HELPER_PROTOCOL})
            self.assertEqual(raw['platform']['minimum_macos'], '27.0')
            self.assertEqual(set(raw['macho']), {
                'hollis-darwin-arm64', 'hollis-darwin-amd64', 'hollis-native-darwin-arm64'})
            self.assertEqual(raw['signing']['artifacts']['hollis-native-darwin-arm64'], 'ad_hoc')
            self.assertEqual(raw['signing']['artifacts']['hollis-darwin-arm64'], 'ad_hoc')
            self.assertFalse(raw['signing']['developer_id'])
            self.assertFalse(raw['signing']['notarized'])
            self.assertEqual(set(raw['assets']), set(manifest.ASSET_FIXED) | {
                'hollis-0.4.0-darwin-arm64.zip'})
            with zipfile.ZipFile(dist / 'hollis-0.4.0-darwin-arm64.zip') as bundle:
                self.assertEqual(set(bundle.namelist()), manifest.BUNDLE_MEMBERS)
                inner = json.loads(bundle.read('manifest.json'))
                self.assertEqual(set(inner), {
                    'version', 'native_protocol_version', 'native_minimum_macos', 'architecture', 'sha256'})
            self.assertEqual(raw['bridges']['members'], sorted(
                raw['bridges']['members'], key=lambda item: item['name']))
            self.assertEqual((dist / 'release-manifest.json').exists(), True)

    def test_source_identity_and_signing_changes_fail_closed(self):
        with tempfile.TemporaryDirectory() as temp:
            dist = make_release_dir(Path(temp) / 'dist')
            with self.assertRaisesRegex(ValueError, 'source identity'):
                manifest.create_manifest(dist, VERSION, 'refs/heads/main', COMMIT)

            with self.assertRaisesRegex(ValueError, 'ad-hoc'):
                manifest.create_manifest(
                    dist, VERSION, f'refs/tags/v{VERSION}', COMMIT,
                    macho_inspector=lambda _path, arch: {
                        'architecture': arch, 'deployment_target': '27.0', 'sdk_version': '27.2'},
                    signature_inspector=lambda path: 'unsigned' if path.name != 'hollis-native-darwin-arm64' else 'signed',
                )

    def test_bridge_archive_must_have_exact_members(self):
        with self.assertRaisesRegex(ValueError, 'exactly the five'):
            manifest.inspect_bridges(_zip_bytes({'only.shortcut': b'fixture'}))

    def test_stale_bundle_is_rejected_against_final_assets(self):
        with tempfile.TemporaryDirectory() as temp:
            dist = make_release_dir(Path(temp) / 'dist')
            (dist / 'hollis-darwin-arm64').write_bytes(b'new final signed bytes')
            with self.assertRaisesRegex(ValueError, 'differs from final release asset'):
                manifest.create_manifest(dist, VERSION, f'refs/tags/v{VERSION}', COMMIT)


if __name__ == '__main__':
    unittest.main()
