import importlib.util
import pathlib
import tempfile
import unittest
import zipfile
spec = importlib.util.spec_from_file_location('privacy', pathlib.Path(__file__).with_name('privacy.py'))
privacy = importlib.util.module_from_spec(spec)
spec.loader.exec_module(privacy)

class PrivacyTests(unittest.TestCase):
    def test_public_source_and_selected_artwork(self):
        privacy.check_names(['README.md', 'docs/assets/hollis-logo.svg', 'native/main.swift'])
        privacy.check_names(['hollis-0.4.0/README.md', 'hollis-0.4.0/docs/assets/hollis-logo.svg'])
        self.assertEqual(privacy.check_names(['kamenxrider-hollis-abcdef0/README.md']), ['README.md'])

    def test_private_source_rejected_with_or_without_wrapper(self):
        for name in ('docs/dev/report.html', 'assets/brand/hollis-v1/reference.png', '.env', 'plugins/hollis/assets/runtime/provenance.json'):
            for prefix in ('', 'hollis-0.4.0/', 'kamenxrider-hollis-abcdef0/'):
                with self.subTest(name=prefix+name), self.assertRaises(ValueError):
                    privacy.check_names([prefix+'README.md', prefix+name])

    def test_exact_bundle_membership(self):
        privacy.check_names(list(privacy.BUNDLE_FILES), 'bundle')
        for extra in ('docs/dev/raw.json', 'notes.txt', '../prompt.txt'):
            with self.assertRaises(ValueError):
                privacy.check_names(list(privacy.BUNDLE_FILES)+[extra], 'bundle')

    def test_plugin_bundled_runtime_allowed_but_research_not(self):
        privacy.check_names(['hollis-plugin-0.2.0/plugins/hollis/assets/runtime/hollis-native'], 'plugin')
        with self.assertRaises(ValueError):
            privacy.check_names(['hollis-plugin-0.2.0/docs/dev/report.html'], 'plugin')

    def test_zip_symlink_rejected(self):
        with tempfile.TemporaryDirectory() as folder:
            path=pathlib.Path(folder)/'bad.zip'
            with zipfile.ZipFile(path,'w') as archive:
                info=zipfile.ZipInfo('link');info.create_system=3;info.external_attr=0o120777<<16
                archive.writestr(info,'private-file')
            with self.assertRaises(ValueError):privacy.archive_names(path)

class ContentGateTests(unittest.TestCase):
    def source(self):
        import hashlib
        import json
        data = {'README.md': b'Public documentation.\n'}
        reviewed = {k: hashlib.sha256(v).hexdigest() for k, v in data.items()}
        data[privacy.MANIFEST] = json.dumps({'schema_version': 1, 'files': reviewed}).encode()
        return data, reviewed

    def test_reviewed_contents_pass(self):
        data, reviewed = self.source()
        privacy.check_source(data, reviewed)

    def test_new_file_in_public_directory_fails(self):
        data, reviewed = self.source()
        data['docs/innocent-name.md'] = b'private investigation'
        with self.assertRaises(ValueError):
            privacy.check_source(data, reviewed)

    def test_modified_reviewed_file_fails(self):
        data, reviewed = self.source()
        data['README.md'] += b'unreviewed recording'
        with self.assertRaises(ValueError):
            privacy.check_source(data, reviewed)

    def test_missing_file_fails(self):
        data, reviewed = self.source()
        del data['README.md']
        with self.assertRaises(ValueError):
            privacy.check_source(data, reviewed)

    def test_manifest_cannot_substitute_review(self):
        data, reviewed = self.source()
        data[privacy.MANIFEST] = b'{}'
        with self.assertRaises(ValueError):
            privacy.check_source(data, reviewed)

    def test_sensitive_text_rejected_without_echo(self):
        for payload in (b'/' + b'Users/alice/notes', b'ghp_' + b'A' * 32,
                        b'codex:' + b'//threads/private-id',
                        b'-----BEGIN ' + b'PRIVATE KEY-----'):
            with self.subTest(payload_type=payload[:3]):
                with self.assertRaises(ValueError) as caught:
                    privacy.check_content('README.md', payload)
                self.assertNotIn(payload.decode(), str(caught.exception))

    def test_synthetic_redaction_canary_allowed(self):
        privacy.check_content('redaction_test.go', b'/' + b'Users/private/customer.txt token=TEST_SECRET_CANARY')
        with self.assertRaises(ValueError):
            privacy.check_content('redaction_test.go', b'/' + b'Users/alice/customer.txt')

    def test_old_research_paths_rejected(self):
        for name in ('scripts/live-suite/host_matrix.py', 'scripts/image-suite/image_suite.py',
                     'plugins/hollis/examples/recorded/results.json',
                     'plugins/hollis/docs/review-evidence.json',
                     'docs/releases/v0.3.2-validation.json', 'internal/cli/image_live_test.go'):
            with self.subTest(name=name), self.assertRaises(ValueError):
                privacy.check_names([name])

    def test_duplicate_archive_member_rejected(self):
        import io
        import warnings
        data = io.BytesIO()
        with warnings.catch_warnings():
            warnings.simplefilter('ignore')
            with zipfile.ZipFile(data, 'w') as archive:
                archive.writestr('README.md', 'one')
                archive.writestr('README.md', 'two')
        data.seek(0)
        with self.assertRaises(ValueError):
            privacy.archive_contents(data)

    def test_nested_archive_is_inspected(self):
        import io
        bridges = io.BytesIO()
        with zipfile.ZipFile(bridges, 'w') as archive:
            for name in privacy.BRIDGE_FILES:
                archive.writestr(name, 'synthetic bridge')
            archive.writestr('private-notes.txt', 'must never ship')
        bundle = io.BytesIO()
        with zipfile.ZipFile(bundle, 'w') as archive:
            for name in privacy.BUNDLE_FILES:
                archive.writestr(name, bridges.getvalue() if name.endswith('.zip') else b'fixture')
        bundle.seek(0)
        with self.assertRaises(ValueError):
            privacy.check_archive(bundle, 'bundle')

    def test_binary_personal_path_rejected(self):
        with self.assertRaises(ValueError):
            privacy.check_content('hollis', b'\x00\x01/' + b'Users/alice/build\x00')

    def test_valid_nested_bundle_passes(self):
        import io
        bridges = io.BytesIO()
        with zipfile.ZipFile(bridges, 'w') as archive:
            for name in privacy.BRIDGE_FILES:
                archive.writestr(name, 'synthetic bridge')
        bundle = io.BytesIO()
        with zipfile.ZipFile(bundle, 'w') as archive:
            for name in privacy.BUNDLE_FILES:
                archive.writestr(name, bridges.getvalue() if name.endswith('.zip') else b'fixture')
        bundle.seek(0)
        privacy.check_archive(bundle, 'bundle')

class ReleaseWorkflowGateTests(unittest.TestCase):
    def test_sbom_cannot_upload_before_privacy_gate(self):
        workflow = (privacy.ROOT / '.github/workflows/release.yml').read_text()
        sbom = workflow.split('      - name: Generate SPDX SBOM\n', 1)[1].split('      - name:', 1)[0]
        for setting in ('upload-artifact: false', 'upload-release-assets: false', 'dependency-snapshot: false'):
            self.assertIn(setting, sbom)
        self.assertLess(workflow.index('- name: Inspect every publication asset'),
                        workflow.index('- name: Attest release artifacts'))
        self.assertLess(workflow.index('- name: Inspect every publication asset'),
                        workflow.index('- name: Create release and upload assets'))
