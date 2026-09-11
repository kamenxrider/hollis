"""Exercise the CLI installer with synthetic assets and no network or Apple calls."""
import hashlib
import io
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest
import zipfile

ROOT = Path(__file__).resolve().parents[2]


class CLIInstallTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.base = Path(self.temp.name).resolve()
        self.home = self.base / 'home'; self.home.mkdir()
        self.tmp = self.base / 'temp'; self.tmp.mkdir()
        self.bin = self.base / 'commands'; self.bin.mkdir()
        self.assets = self.base / 'assets'; self.assets.mkdir()
        self.env = dict(os.environ, HOME=str(self.home), TMPDIR=str(self.tmp),
                        PATH=f'{self.bin}:/usr/bin:/bin:/usr/sbin:/sbin',
                        FIXTURE_ASSETS=str(self.assets), FIXTURE_LOG=str(self.base / 'calls'),
                        HOLLIS_STATE_DIR=str(self.base / 'state'))
        self.command('uname', '[[ "$1" == -s ]] && echo Darwin || echo "${FIXTURE_ARCH:-arm64}"')
        self.command('sw_vers', 'echo "${FIXTURE_OS:-27.0}"')
        self.command('gh', '''echo "gh $*" >> "$FIXTURE_LOG"
if [[ "$1" == attestation ]]; then exit "${FIXTURE_ATTEST_EXIT:-0}"; fi
while [[ $# -gt 0 ]]; do
 if [[ "$1" == --dir ]]; then cp "$FIXTURE_ASSETS/"* "$2/"; exit; fi
 shift
done
exit 1''')
        self.command('codesign', 'exit 0')
        self.command('shortcuts', '''echo "shortcuts $*" >> "$FIXTURE_LOG"
[[ "$1" == sign && "$4" == --input && "$6" == --output ]] || exit 12
cp "$5" "$7"''')
        self.command('open', 'echo "open $*" >> "$FIXTURE_LOG"')
        self.command('plutil', 'echo "${FIXTURE_IMAGE_BRIDGE:-}"')
        self.build_assets()

    def command(self, name, source):
        p = self.bin / name
        p.write_text('#!/bin/bash\nset -e\n' + source + '\n'); p.chmod(0o700)

    def build_assets(self, helper_version='0.4.0'):
        bridges = io.BytesIO()
        with zipfile.ZipFile(bridges, 'w') as z:
            for n in ('Cloud', 'Cloud Pro', 'On-Device', 'ChatGPT', 'Image'):
                z.writestr(n + '.shortcut', 'synthetic bridge')
        cli = '''#!/bin/bash
case "$1" in
--version) echo 'hollis 0.4.0';;
config) echo "config $*" >> "$FIXTURE_LOG"; [[ "$2" != show ]] || echo '{"image_bridge":""}';;
*) exit 99;;
esac
'''
        helper = f'''#!/bin/bash
case "$1" in
--version) echo 'hollis-native {helper_version}';;
--protocol-version) echo 1;;
*) exit 99;;
esac
'''
        bundle = self.assets / 'hollis-0.4.0-darwin-arm64.zip'
        with zipfile.ZipFile(bundle, 'w') as z:
            for n, data in [('hollis', cli), ('hollis-native', helper)]:
                info = zipfile.ZipInfo(n); info.create_system = 3; info.external_attr = 0o100755 << 16
                z.writestr(info, data)
            z.writestr('hollis-bridges.zip', bridges.getvalue())
        self.sumline = hashlib.sha256(bundle.read_bytes()).hexdigest() + '  ' + bundle.name + '\n'
        (self.assets / 'SHA256SUMS').write_text(self.sumline)

    def run_install(self, success=True):
        result = subprocess.run(['/bin/bash', str(ROOT / 'scripts/install.sh')],
                                env=self.env, text=True, capture_output=True, timeout=15)
        if success:
            self.assertEqual(result.returncode, 0, result.stderr)
        else:
            self.assertNotEqual(result.returncode, 0)
            self.assertFalse((self.home / '.local/bin/hollis').exists())
        self.assertEqual(list(self.tmp.iterdir()), [], 'temporary downloads must be removed')
        return result

    def test_matched_pair_imports_permissions_and_default(self):
        self.run_install()
        for n in ('hollis', 'hollis-native'):
            self.assertEqual((self.home / '.local/bin' / n).stat().st_mode & 0o777, 0o755)
        log = (self.base / 'calls').read_text()
        self.assertIn('--source-digest 1906721a1cd5594ce47be6d4d306d7daa1e6891f', log)
        self.assertIn('--deny-self-hosted-runners', log)
        self.assertIn('config config set image-bridge Hollis Image - Reference Input v2', log)
        self.assertEqual(sum(s.startswith('open ') for s in log.splitlines()), 5)
        imports = self.base / 'state/bridge-imports/0.4.0'
        self.assertTrue(all(p.stat().st_mode & 0o777 == 0o600 for p in imports.iterdir()))

    def test_existing_custom_image_setting_is_preserved(self):
        self.env['FIXTURE_IMAGE_BRIDGE'] = 'Custom Image'
        self.run_install()
        self.assertNotIn('config set', (self.base / 'calls').read_text())

    def test_only_current_imports_are_opened(self):
        imports = self.base / 'state/bridge-imports/0.4.0'; imports.mkdir(parents=True)
        (imports / 'unrelated.shortcut').write_text('preserve')
        self.run_install()
        self.assertNotIn('unrelated.shortcut', (self.base / 'calls').read_text())

    def test_checksum_failure_precedes_execution(self):
        (self.assets / 'hollis-0.4.0-darwin-arm64.zip').write_bytes(b'corruption')
        self.run_install(False)
        self.assertNotIn('shortcuts', (self.base / 'calls').read_text())

    def test_duplicate_checksum_is_rejected(self):
        (self.assets / 'SHA256SUMS').write_text(self.sumline * 2)
        self.run_install(False)

    def test_provenance_failure_precedes_install(self):
        self.env['FIXTURE_ATTEST_EXIT'] = '1'; self.run_install(False)

    def test_helper_version_mismatch_precedes_install(self):
        self.build_assets('0.3.3'); self.run_install(False)

    def test_intel_and_old_os_stop_before_network(self):
        for key, value in [('FIXTURE_ARCH', 'x86_64'), ('FIXTURE_OS', '26.0')]:
            self.env[key] = value; self.run_install(False); del self.env[key]
        self.assertFalse((self.base / 'calls').exists())

    def test_destination_symlink_is_preserved(self):
        target = self.base / 'existing'; target.write_text('keep')
        dest = self.home / '.local/bin'; dest.mkdir(parents=True)
        (dest / 'hollis-native').symlink_to(target)
        self.run_install(False)
        self.assertEqual(target.read_text(), 'keep')


if __name__ == '__main__':
    unittest.main()
